package cmd

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sbam/pkg/fronius"
	"sbam/pkg/mqtt"
	"sbam/pkg/optimize"
	pw "sbam/pkg/power"
	u "sbam/src/utils"
	"strings"
	"sync/atomic"
	"time"
)

const (
	runnerIntentQueueSize = 16
	chargeCooldownMinutes = 5
)

var (
	errRunnerIntentQueueFull = errors.New("runner intent queue is full")
	errRunnerPaused          = errors.New("command rejected while paused")
	errSetReserveUnsupported = errors.New("set_reserve is not supported in v2.0.0")
	errUnsupportedIntent     = errors.New("unsupported runner intent")
)

// RunnerConfig describes runtime values used by the schedule runner.
// Values are sourced from CLI/Viper and normalized before constructing Runner.
type RunnerConfig struct {
	APIKey             string
	URL                string
	FroniusIP          string
	PWConsumption      float64
	MaxCharge          float64
	PWBattReserve      float64
	StartHR            string
	EndHR              string
	BattReserveStartHR string
	BattReserveEndHR   string
	PWLWT              float64
	PWUPT              float64
	CacheForecast      bool
	CacheFilePrefix    string
	CacheTime          int32
	Defaults           bool
	ForecastHorizon    string
	ConsumptionHorizon string
	SchedulerMode      string
	Windows            []pw.Window
	WeekdayFeature     bool
	SunSpecType        fronius.SunSpecType
	MQTT               mqtt.Config
	Now                func() time.Time
	// Optimize and OptimizeDeps are used only when SchedulerMode is
	// "optimize" (SPEC-0001); schedule.go validates and builds them.
	Optimize     OptimizeConfig
	OptimizeDeps optimizeDeps
}

type batteryWriter interface {
	ForceCharge(froniusIP string, targetPct int16) error
	SetDefaults(froniusIP string) error
}

// froniusBatteryWriter writes through the fronius package using the selected
// sunspec_type. port overrides the default Modbus port (502) and is only set
// by tests.
type froniusBatteryWriter struct {
	sunspecType fronius.SunSpecType
	port        string
}

func (w froniusBatteryWriter) ports() []string {
	if w.port == "" {
		return nil
	}
	return []string{w.port}
}

func (w froniusBatteryWriter) ForceCharge(froniusIP string, targetPct int16) error {
	return fronius.ApplyForceCharge(w.sunspecType, froniusIP, targetPct, w.ports()...)
}

func (w froniusBatteryWriter) SetDefaults(froniusIP string) error {
	return fronius.ApplyDefaults(w.sunspecType, froniusIP, w.ports()...)
}

var newBatteryWriter = func(sunspecType fronius.SunSpecType) batteryWriter {
	return froniusBatteryWriter{sunspecType: sunspecType}
}

// newTimerFunc is a package-level factory for one-shot timers. Tests may
// override it to inject deterministic timer behavior.
var newTimerFunc = time.AfterFunc

// latest state caching was removed; the runner publishes state directly.

// Runner owns serialized schedule and command execution.
// Single-writer invariant: all Fronius Modbus write operations must be executed by this runner.
type Runner struct {
	cfg           RunnerConfig
	client        mqtt.Client
	writer        batteryWriter
	intents       chan mqtt.Intent
	paused        atomic.Pointer[time.Time]
	tickerCh      <-chan time.Time // nil when not in windows mode
	defaultsTimer *time.Timer
	boundaryTimer *time.Timer
	defaultsFired atomic.Bool
	activeWindow  string // name of active window for change detection
	// opt holds the optimize-mode state; nil in the other scheduler modes.
	opt *optRunnerState
}

// NewRunner constructs a Runner with the provided configuration and MQTT
// client. If `cfg.Now` is nil it defaults to `time.Now`. The runner is
// initialized with a buffered intent queue and a battery writer.
func NewRunner(cfg RunnerConfig, client mqtt.Client) *Runner {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	r := &Runner{
		cfg:     cfg,
		client:  client,
		writer:  newBatteryWriter(cfg.SunSpecType),
		intents: make(chan mqtt.Intent, runnerIntentQueueSize),
	}
	if cfg.SchedulerMode == schedulerModeOptimize {
		r.opt = newOptRunnerState(cfg)
	}
	return r
}

// Submit enqueues an intent for the runner to process. It returns true on
// success or false if the internal intent queue is full, in which case an
// error is logged and published to MQTT.
func (r *Runner) Submit(intent mqtt.Intent) bool {
	select {
	case r.intents <- intent:
		return true
	default:
		err := fmt.Errorf("%w: %s", errRunnerIntentQueueFull, intent.Kind)
		u.Log.Warn(err)
		r.publishError(context.Background(), "runner", err)
		return false
	}
}

// Run is the runner's main event loop. It consumes intents from the
// internal queue and dispatches them to handlers until a shutdown intent
// is processed or the provided context is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		select {
		case <-ctx.Done():
			r.optimizeShutdown()
			return ctx.Err()
		case <-r.tickerCh:
			if !r.Submit(mqtt.Intent{Kind: mqtt.IntentTick}) {
				u.Log.Warn("windows tick dropped because runner queue is full")
			}
		case intent := <-r.intents:
			r.handleIntent(ctx, intent)
			if intent.Kind == mqtt.IntentShutdown {
				r.optimizeShutdown()
				return nil
			}
		}
	}
}

// StartWindowsTicker initializes the internal ticker for windows mode.
// It computes the tick interval from the active window's tick_minutes (or 60
// min default), creates a time.Ticker, and schedules a set_defaults timer if
// the active window has defaults enabled. Callers in crontab mode must not
// call this method — it is only valid when SchedulerMode is "windows".
func (r *Runner) StartWindowsTicker(now time.Time) {
	if !r.Submit(mqtt.Intent{Kind: mqtt.IntentTick}) {
		u.Log.Warn("immediate tick dropped because runner queue is full")
	}
	r.startWindowsTicker(now)
}

func (r *Runner) startWindowsTicker(now time.Time) {
	r.stopWindowsTicker()

	active := pw.ResolveActiveWindow(r.cfg.Windows, now, r.cfg.WeekdayFeature)
	if active != nil {
		r.activeWindow = active.Name
	} else {
		r.activeWindow = ""
	}

	interval := 60 * time.Minute
	if active != nil && active.TickMinutes != nil {
		interval = time.Duration(*active.TickMinutes) * time.Minute
	}

	t := time.NewTicker(interval)
	r.tickerCh = t.C

	// Schedule per-window set_defaults if enabled.
	if active != nil && active.Defaults != nil && *active.Defaults {
		r.scheduleDefaults(active, now)
	}

	// Schedule a one-shot timer at the next window's start boundary.
	r.scheduleBoundaryTick(now)
}

func (r *Runner) stopWindowsTicker() {
	r.tickerCh = nil
	// We cannot stop the underlying ticker without storing a reference, but
	// setting tickerCh to nil prevents further selects from firing. The GC
	// will collect the orphaned ticker after its next fire (within interval).
	if r.defaultsTimer != nil {
		r.defaultsTimer.Stop()
		r.defaultsTimer = nil
	}
	if r.boundaryTimer != nil {
		r.boundaryTimer.Stop()
		r.boundaryTimer = nil
	}
	r.defaultsFired.Store(false)
	r.activeWindow = ""
}

// scheduleDefaults parses the active window's end time and schedules a
// one-shot time.AfterFunc that fires set_defaults at
// (window.end - before_end_defaults). The timer is tracked in r.defaultsTimer
// so it can be cancelled on window change.
func (r *Runner) scheduleDefaults(active *pw.Window, now time.Time) {
	beforeEnd := 5 // default minutes
	if active.BeforeEndDefaults != nil {
		beforeEnd = *active.BeforeEndDefaults
	}

	endTime, err := parseScheduleClock(active.End, "end")
	if err != nil {
		u.Log.Warnw("cannot parse window end for set_defaults timer", "window", active.Name, "end", active.End, "error", err)
		return
	}

	endAt := time.Date(now.Year(), now.Month(), now.Day(),
		endTime.Hour(), endTime.Minute(), 0, 0, now.Location())

	// If endAt is before or equal to now, advance by 24h (cross-midnight window).
	if !endAt.After(now) {
		endAt = endAt.Add(24 * time.Hour)
	}

	resetAt := endAt.Add(-time.Duration(beforeEnd) * time.Minute)
	delay := resetAt.Sub(now)
	if delay < 0 {
		delay = 0 // fire immediately if reset time already passed
	}

	u.Log.Infow("scheduling set_defaults for window",
		"window", active.Name, "end", active.End, "before_end_minutes", beforeEnd,
		"reset_at", resetAt.Format(time.RFC3339), "delay", delay.Round(time.Second))

	r.defaultsTimer = time.AfterFunc(delay, func() {
		u.Log.Infow("set_defaults timer fired", "window", active.Name)
		r.defaultsFired.Store(true)
		if !r.Submit(mqtt.Intent{Kind: mqtt.IntentSetDefaults}) {
			u.Log.Warn("set_defaults dropped because runner queue is full")
		}
	})
}

// nextWindowStart resolves the next upcoming charge window and the instant
// it starts. It scans every configured window for its next start strictly
// after now — advancing a day at a time to honour per-window weekday
// filters — and returns the one whose start is earliest.
//
// Selecting by nearest instant, rather than by position in the ordered
// list, makes the result correct regardless of how the windows are ordered
// in the config, how many there are, or how their weekday sets differ. The
// currently-active window's own next start naturally falls after any window
// that recurs sooner, so it is only chosen when it genuinely is the next
// one (e.g. a single configured window, which wraps to its own next start).
//
// ok is false only when no window is configured or none has a parseable
// start; a single malformed window is skipped rather than abandoning the
// whole resolution.
func (r *Runner) nextWindowStart(now time.Time) (next pw.Window, nextAt time.Time, ok bool) {
	windows := r.cfg.Windows
	if len(windows) == 0 {
		return pw.Window{}, time.Time{}, false
	}

	var best *pw.Window
	var bestStart time.Time
	for i := range windows {
		startTime, err := parseScheduleClock(windows[i].Start, "start")
		if err != nil {
			u.Log.Warnw("cannot parse window start",
				"window", windows[i].Name, "start", windows[i].Start, "error", err)
			continue
		}
		startAt := time.Date(now.Year(), now.Month(), now.Day(),
			startTime.Hour(), startTime.Minute(), 0, 0, now.Location())
		// Only future starts count; a start at or before now is that
		// window's next occurrence tomorrow.
		if !startAt.After(now) {
			startAt = startAt.Add(24 * time.Hour)
		}
		if r.cfg.WeekdayFeature && windows[i].Weekdays != "" {
			if wdSet, err := pw.ParseWeekdays(windows[i].Weekdays); err == nil && len(wdSet) > 0 {
				startAt = nextMatchingWeekday(startAt, wdSet)
			}
		}
		if best == nil || startAt.Before(bestStart) {
			best = &windows[i]
			bestStart = startAt
		}
	}

	if best == nil {
		return pw.Window{}, time.Time{}, false
	}

	return *best, bestStart, true
}

// scheduleBoundaryTick schedules a one-shot timer that fires an IntentTick
// at the start of the next upcoming window, as resolved by nextWindowStart
// (the nearest window start after now across all configured windows,
// honouring weekday filters). This is what makes a later-same-day window
// activate on time even while an earlier window is still running.
func (r *Runner) scheduleBoundaryTick(now time.Time) {
	next, nextAt, ok := r.nextWindowStart(now)
	if !ok {
		return
	}

	delay := nextAt.Sub(now)

	u.Log.Infow("scheduling boundary tick",
		"next_window", pw.WindowNameOrDefault(next, 0),
		"at", nextAt.Format(time.RFC3339),
		"delay", delay.Round(time.Second))

	r.boundaryTimer = newTimerFunc(delay, func() {
		u.Log.Infow("boundary timer fired, submitting tick")
		if !r.Submit(mqtt.Intent{Kind: mqtt.IntentTick}) {
			u.Log.Warn("boundary tick dropped because runner queue is full")
		}
	})
}

// HandleCommand parses an MQTT command payload into an Intent and submits
// it to the runner. It returns false when parsing fails or when the intent
// queue is full; in both cases an MQTT ack is published with the error.
func (r *Runner) HandleCommand(ctx context.Context, topic string, payload []byte) bool {
	intent, err := mqtt.ParseIntent(topic, payload)
	if err != nil {
		if ackErr := mqtt.PublishAck(ctx, r.client, topic, intent, err); ackErr != nil {
			u.HandleError(ackErr, "mqtt command ack publish failed")
		}
		return false
	}

	if !r.Submit(intent) {
		rejectedErr := fmt.Errorf("%w", errRunnerIntentQueueFull)
		if ackErr := mqtt.PublishAck(ctx, r.client, topic, intent, rejectedErr); ackErr != nil {
			u.HandleError(ackErr, "mqtt command ack publish failed")
		}
		return false
	}

	return true
}

// Tick performs one scheduling cycle: it checks windows, reads storage
// state, optionally retrieves the forecast, invokes the Fronius decision
// classifier, and publishes the resulting state payload. It is safe to
// call concurrently but the runner serializes Modbus writes.
func (r *Runner) Tick(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		now = r.now()
	}

	// Resolve active window and derive effective charge parameters.
	// When no windows are configured, falls back to legacy StartHR/EndHR.
	inChargeWindow, effectiveMaxCharge, effectiveForecastHorizon, effectiveConsumptionHorizon, activeWindowName := r.resolveActiveWindow(now)

	// In windows mode, restart the ticker when the active window changes.
	if r.cfg.SchedulerMode == "windows" {
		prevWindow := r.activeWindow
		if activeWindowName != prevWindow {
			u.Log.Infow("active window changed", "from", prevWindow, "to", activeWindowName)
			r.startWindowsTicker(now)
		}
	}

	reserveWindowActive, err := checkTimeRangeAt(now, r.cfg.BattReserveStartHR, r.cfg.BattReserveEndHR)
	if err != nil {
		r.publishError(ctx, "tick", err)
		return err
	}

	storageHandler := newStorage()
	capacityToCharge, capacityMax, socPct, storageErr := storageHandler.Handler(r.cfg.FroniusIP)
	if storageErr != nil {
		u.HandleError(storageErr, "storage handler failed, skipping schedule run")

		payload := makeBasePayload(fronius.DecisionSkip.String(), fmt.Sprintf("storage read failed: %v", storageErr), inChargeWindow, reserveWindowActive, r.cfg.SchedulerMode)
		paused, pauseUntil := r.pauseStateAt(now)
		payload.Paused = paused
		payload.NextRun = pauseUntil
		r.publishState(payload)
		r.publishError(ctx, "storage", storageErr)
		return storageErr
	}

	if paused, pauseUntil := r.pauseStateAt(now); paused {
		payload := makeBasePayload("paused", "Forecast Charging execution skipped because sbam is paused", inChargeWindow, reserveWindowActive, r.cfg.SchedulerMode)
		payload.BatterySOCPct = &socPct
		payload.BatteryCapacityWh = &capacityMax
		payload.Paused = true
		payload.NextRun = pauseUntil
		r.publishState(payload)
		return nil
	}

	if !inChargeWindow {
		u.Log.Infof("The current time is outside all configured charge windows")
		capMax := capacityMax

		payload := makeBasePayload(fronius.DecisionIdle.String(), "current time outside configured charging window", inChargeWindow, reserveWindowActive, r.cfg.SchedulerMode)
		payload.BatterySOCPct = &socPct
		payload.BatteryCapacityWh = &capMax
		r.publishState(payload)
		return nil
	}

	// Cooldown check: in windows mode use the active window's end time and
	// before_end_defaults (or default 5 min); in crontab mode use legacy params.
	var inCooldown bool
	var cooldownErr error
	if r.cfg.SchedulerMode == "windows" && len(r.cfg.Windows) > 0 {
		active := pw.ResolveActiveWindow(r.cfg.Windows, now, r.cfg.WeekdayFeature)
		if active != nil {
			cooldownDuration := chargeCooldownMinutes
			if active.Defaults != nil && *active.Defaults && active.BeforeEndDefaults != nil {
				cooldownDuration = *active.BeforeEndDefaults
			}
			inCooldown, cooldownErr = isInCooldown(now, active.Start, active.End, cooldownDuration)
		}
	} else {
		inCooldown, cooldownErr = isInCooldown(now, r.cfg.StartHR, r.cfg.EndHR, chargeCooldownMinutes)
	}
	if cooldownErr != nil {
		r.publishError(ctx, "tick", cooldownErr)
		return cooldownErr
	}
	if inCooldown {
		u.Log.Info("cooldown active — charge decisions suppressed near window end")
		capMax := capacityMax
		payload := makeBasePayload("cooldown", "charge decisions suppressed near window end", inChargeWindow, reserveWindowActive, r.cfg.SchedulerMode)
		payload.BatterySOCPct = &socPct
		payload.BatteryCapacityWh = &capMax
		r.publishState(payload)
		return nil
	}

	// to_next_window scales consumption to the span until the next window
	// start; resolve that instant only when the mode needs it.
	var nextWindowStartAt time.Time
	if effectiveConsumptionHorizon == pw.ConsumptionHorizonToNextWindow {
		if _, nextAt, ok := r.nextWindowStart(now); ok {
			nextWindowStartAt = nextAt
		} else {
			u.Log.Warn("consumption_horizon to_next_window needs a resolvable next window; falling back to remaining_today")
			effectiveConsumptionHorizon = pw.ConsumptionHorizonRemainingToday
		}
	}

	effectiveConsumption := pw.ResolveConsumption(
		effectiveConsumptionHorizon, r.cfg.PWConsumption, now, nextWindowStartAt)

	var solarPowerProduction float64
	var forecastRetrieved bool
	var forecastErr error

	if r.cfg.ForecastHorizon != pw.ForecastHorizonOff {
		powerHandler := newPower()
		solarPowerProduction, forecastRetrieved, forecastErr = powerHandler.Handler(
			r.cfg.APIKey, r.cfg.URL, r.cfg.CacheForecast,
			r.cfg.CacheFilePrefix, r.cfg.CacheTime,
			effectiveForecastHorizon, now,
		)
		if forecastErr != nil {
			u.HandleError(forecastErr, "power forecast retrieval failed; disabling forecast for this run")
			r.publishError(ctx, "power", forecastErr)
			solarPowerProduction = 0.0
			forecastRetrieved = false
		}
	}

	u.Log.Infof("your Daily consumption is:%d Wh", int(effectiveConsumption))

	froniusHandler := newFronius(r.cfg.SunSpecType)
	chargePct, decision, reason, powerState, froniusErr := froniusHandler.Handler(
		solarPowerProduction,
		capacityToCharge,
		capacityMax,
		effectiveConsumption,
		r.cfg.MaxCharge,
		r.cfg.PWBattReserve,
		r.cfg.StartHR,
		r.cfg.EndHR,
		r.cfg.FroniusIP,
		reserveWindowActive,
		r.cfg.PWLWT,
		r.cfg.PWUPT,
		forecastRetrieved,
	)
	if froniusErr != nil {
		u.HandleError(froniusErr, "fronius handler failed, skipping schedule run")

		payload := makeBasePayload(fronius.DecisionSkip.String(), fmt.Sprintf("fronius handler failed: %v", froniusErr), inChargeWindow, reserveWindowActive, r.cfg.SchedulerMode)
		payload.BatterySOCPct = &socPct
		payload.BatteryCapacityWh = &capacityMax
		payload.Paused = false
		r.publishState(payload)
		r.publishError(ctx, "fronius", froniusErr)
		return froniusErr
	}

	payload := makeBasePayload(decision.String(), reason.String(), inChargeWindow, reserveWindowActive, r.cfg.SchedulerMode)
	payload.BatterySOCPct = &socPct
	payload.BatteryCapacityWh = &capacityMax
	payload.ForecastTodayWh = &solarPowerProduction
	payload.PwNetWh = &powerState.Net
	payload.ForecastHorizon = effectiveForecastHorizon
	payload.ConsumptionHorizon = effectiveConsumptionHorizon
	if activeWindowName != "" {
		awName := activeWindowName
		payload.ActiveWindow = &awName
		awMaxCharge := effectiveMaxCharge
		payload.ActiveWindowMaxCharge = &awMaxCharge
		awFH := effectiveForecastHorizon
		payload.ActiveWindowForecastHorizon = &awFH
	}
	payload.ChargePct = &chargePct
	payload.Paused = false
	r.publishState(payload)
	return nil
}

// handleIntent processes a single Intent from the queue and dispatches to
// the appropriate handler. It also acknowledges MQTT commands where
// required.
func (r *Runner) handleIntent(ctx context.Context, intent mqtt.Intent) {
	if r.opt != nil && r.handleOptimizeIntent(ctx, intent) {
		return
	}
	switch intent.Kind {
	case mqtt.IntentShutdown:
		return
	case mqtt.IntentTick:
		if err := r.Tick(ctx, r.now()); err != nil {
			u.HandleError(err, "runner tick failed")
		}
		return
	case mqtt.IntentTriggerNow:
		err := r.Tick(ctx, r.now())
		r.publishIntentAck(ctx, intent, err)
		if err != nil {
			u.HandleError(err, "runner trigger_now failed")
		}
		return
	case mqtt.IntentPause:
		r.setPause(intent.PauseUntil)
		payload := r.newCommandPayload("paused", "schedule paused by command", r.now())
		payload.Paused = true
		if intent.PauseUntil != nil {
			until := intent.PauseUntil.UTC()
			payload.NextRun = &until
		}
		r.publishState(payload)
		r.publishIntentAck(ctx, intent, nil)
		return
	case mqtt.IntentResume:
		r.clearPause()
		payload := r.newCommandPayload("resume", "schedule resumed by command", r.now())
		payload.Paused = false
		r.publishState(payload)
		r.publishIntentAck(ctx, intent, nil)
		return
	case mqtt.IntentForceCharge:
		err := r.handleForceCharge(ctx, intent)
		r.publishIntentAck(ctx, intent, err)
		return
	case mqtt.IntentSetDefaults:
		err := r.handleSetDefaults(ctx, intent)
		r.publishIntentAck(ctx, intent, err)
		return
	case mqtt.IntentSetReserve:
		r.publishError(ctx, "runner", errSetReserveUnsupported)
		r.publishIntentAck(ctx, intent, errSetReserveUnsupported)
		return
	default:
		err := fmt.Errorf("%w: %s", errUnsupportedIntent, intent.Kind)
		r.publishError(ctx, "runner", err)
		r.publishIntentAck(ctx, intent, err)
		return
	}
}

// handleForceCharge executes a forced charge command by validating the
// payload and writing the requested target via the battery writer. It
// respects pause state and returns an error on failure.
func (r *Runner) handleForceCharge(ctx context.Context, intent mqtt.Intent) error {
	paused, _ := r.pauseStateAt(r.now())
	if paused {
		err := fmt.Errorf("%w: %s", errRunnerPaused, intent.Kind)
		r.publishError(ctx, "force_charge", err)
		return err
	}

	targetPct, err := r.forceChargeTargetPct(ctx, intent)
	if err != nil {
		return err
	}

	if err := r.writer.ForceCharge(r.cfg.FroniusIP, targetPct); err != nil {
		r.publishError(ctx, "force_charge", err)
		return err
	}

	payload := r.newCommandPayload(string(mqtt.IntentForceCharge), "force charge command executed", r.now())
	payload.Paused = false
	payload.ChargePct = &targetPct
	r.publishState(payload)
	return nil
}

// forceChargeTargetPct validates a force_charge intent and resolves the
// percentage to write. Validation errors are published to the error topic.
func (r *Runner) forceChargeTargetPct(ctx context.Context, intent mqtt.Intent) (int16, error) {
	if intent.TargetPct < 0 || intent.TargetPct > 100 {
		err := fmt.Errorf("%w: target_pct must be between 0 and 100", mqtt.ErrInvalidPayload)
		r.publishError(ctx, "force_charge", err)
		return 0, err
	}

	var (
		targetPct int16
		err       error
	)

	switch {
	case intent.IgnoreMaxCharge:
		if intent.TargetPct != 100 {
			err = fmt.Errorf("%w: ignore_max_charge requires target_pct 100", mqtt.ErrInvalidPayload)
			r.publishError(ctx, "force_charge", err)
			return 0, err
		}
		targetPct = 100
	case intent.TargetPct == 0:
		targetPct = 0
	default:
		targetPct, err = r.resolveForceChargeTargetPct(intent.TargetPct)
		if err != nil {
			r.publishError(ctx, "force_charge", err)
			return 0, err
		}
	}

	return targetPct, nil
}

// resolveForceChargeTargetPct caps any requested force charge percentage
// by configured max_charge and current battery max capacity.
//
// Effective target is:
// min(requestedPct, max_charge*100/pw_batt_max)
//
// When max_charge is 0 the effective target becomes 0 (no charge).
func (r *Runner) resolveForceChargeTargetPct(requestedPct int16) (int16, error) {
	if r.cfg.MaxCharge < 0 {
		return 0, fmt.Errorf("invalid max_charge %.2f: must be greater than or equal to 0", r.cfg.MaxCharge)
	}

	storageHandler := newStorage()
	_, capacityMax, _, err := storageHandler.Handler(r.cfg.FroniusIP)
	if err != nil {
		return 0, fmt.Errorf("unable to resolve force_charge target from battery capacity: %w", err)
	}
	if capacityMax <= 0 {
		return 0, fmt.Errorf("invalid battery capacity %.2f: must be greater than 0", capacityMax)
	}

	requestedLoad := capacityMax * float64(requestedPct) / 100.0
	targetPct := fronius.SetChargePower(capacityMax, requestedLoad, r.cfg.MaxCharge)

	return targetPct, nil
}

// handleSetDefaults triggers the battery writer to restore inverter
// defaults. It respects pause state and publishes the resulting state.
func (r *Runner) handleSetDefaults(ctx context.Context, intent mqtt.Intent) error {
	paused, _ := r.pauseStateAt(r.now())
	if paused {
		err := fmt.Errorf("%w: %s", errRunnerPaused, intent.Kind)
		r.publishError(ctx, "set_defaults", err)
		return err
	}

	if err := r.writer.SetDefaults(r.cfg.FroniusIP); err != nil {
		r.publishError(ctx, "set_defaults", err)
		return err
	}

	payload := r.newCommandPayload(string(mqtt.IntentSetDefaults), "set defaults command executed", r.now())
	payload.Paused = false
	r.publishState(payload)
	return nil
}

// newCommandPayload builds a basic StatePayload for command responses,
// computing current window flags and returning a payload ready for
// callers to fill in additional telemetry fields.
func (r *Runner) newCommandPayload(lastDecision, reason string, now time.Time) mqtt.StatePayload {
	if r.opt != nil {
		// Charge and reserve windows have no meaning in optimize mode (UX §2.5).
		return makeBasePayload(lastDecision, reason, false, false, r.cfg.SchedulerMode)
	}
	inChargeWindow, _, _, _, _ := r.resolveActiveWindow(now)

	reserveWindowActive, reserveErr := checkTimeRangeAt(now, r.cfg.BattReserveStartHR, r.cfg.BattReserveEndHR)
	if reserveErr != nil {
		u.HandleError(reserveErr, "unable to compute reserve window status")
		reserveWindowActive = false
	}

	return makeBasePayload(lastDecision, reason, inChargeWindow, reserveWindowActive, r.cfg.SchedulerMode)
}

// publishState publishes the provided state payload via MQTT using the
// configured MQTT client and topic prefix.
func (r *Runner) publishState(payload mqtt.StatePayload) {
	if r.opt != nil {
		r.opt.mergeLast(&payload)
	}
	publishStateSnapshot(r.client, r.cfg.MQTT, payload)
}

// publishError sends an error payload to the MQTT error topic when MQTT
// is enabled. It returns immediately when err is nil or MQTT is disabled.
func (r *Runner) publishError(ctx context.Context, source string, err error) {
	if err == nil || !r.cfg.MQTT.Enabled {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	opCtx, cancel := context.WithTimeout(ctx, const_mqtt_op_timeout)
	defer cancel()

	mqtt.PublishError(opCtx, r.client, r.cfg.MQTT.TopicPrefix, mqtt.ErrorPayload{
		Error:     err.Error(),
		Source:    source,
		Timestamp: r.now().UTC(),
	})
}

// publishIntentAck publishes an acknowledgement for a parsed MQTT command
// intent, including any parse error.
func (r *Runner) publishIntentAck(ctx context.Context, intent mqtt.Intent, parseErr error) {
	if strings.TrimSpace(intent.CommandTopic) == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := mqtt.PublishAck(ctx, r.client, intent.CommandTopic, intent, parseErr); err != nil {
		u.HandleError(err, "mqtt command ack publish failed")
	}
}

// now returns the configured clock function for the runner (used for
// testing/time injection).
func (r *Runner) now() time.Time {
	return r.cfg.Now()
}

// setPause sets the runner pause deadline. A nil pointer clears the pause
// (indefinite); a zero Time value represents an indefinite pause.
func (r *Runner) setPause(pauseUntil *time.Time) {
	if pauseUntil == nil {
		indefinite := time.Time{}
		r.paused.Store(&indefinite)
		u.Log.Info("schedule paused indefinitely")
		return
	}
	untilUTC := pauseUntil.UTC()
	r.paused.Store(&untilUTC)
	localUntil := pauseUntil.In(r.now().Location())
	u.Log.Infof("schedule paused until %s", localUntil.Format(time.RFC3339))
}

// clearPause removes any pause deadline.
func (r *Runner) clearPause() {
	r.paused.Store(nil)
	u.Log.Info("schedule resumed")
}

// resolveActiveWindow determines whether the current time falls within any
// configured charge window and returns the effective charge parameters.
// When windows are configured it delegates to power.ResolveActiveWindow;
// otherwise it falls back to the legacy checkTimeRangeAt call using
// RunnerConfig.StartHR / EndHR.
func (r *Runner) resolveActiveWindow(now time.Time) (inWindow bool, maxCharge float64, forecastHorizon, consumptionHorizon, windowName string) {
	maxCharge = r.cfg.MaxCharge
	forecastHorizon = r.cfg.ForecastHorizon
	consumptionHorizon = r.cfg.ConsumptionHorizon

	if len(r.cfg.Windows) > 0 {
		active := pw.ResolveActiveWindow(r.cfg.Windows, now, r.cfg.WeekdayFeature)
		if active == nil {
			return false, maxCharge, forecastHorizon, consumptionHorizon, ""
		}
		maxCharge = active.MaxCharge
		if active.ForecastHorizon != "" {
			forecastHorizon = active.ForecastHorizon
		}
		if active.ConsumptionHorizon != "" {
			consumptionHorizon = active.ConsumptionHorizon
		}
		name := pw.WindowNameOrDefault(*active, 0)
		return true, maxCharge, forecastHorizon, consumptionHorizon, name
	}

	// Legacy path: use single StartHR/EndHR window.
	inWindow, err := checkTimeRangeAt(now, r.cfg.StartHR, r.cfg.EndHR)
	if err != nil {
		u.HandleError(err, "checkTimeRangeAt failed in resolveActiveWindow")
		return false, maxCharge, forecastHorizon, consumptionHorizon, ""
	}
	if inWindow {
		return true, maxCharge, forecastHorizon, consumptionHorizon, "legacy"
	}
	return false, maxCharge, forecastHorizon, consumptionHorizon, ""
}

// pauseStateAt reports whether the runner is currently paused at the
// provided time and returns the pause-until timestamp when paused.
func (r *Runner) pauseStateAt(now time.Time) (bool, *time.Time) {
	deadline := r.paused.Load()
	if deadline == nil {
		return false, nil
	}

	if deadline.IsZero() {
		return true, nil
	}

	until := deadline.UTC()
	if !until.After(now) {
		localUntil := until.In(r.now().Location())
		u.Log.Infof("pause expired at %s; resuming", localUntil.Format(time.RFC3339))
		r.clearPause()
		return false, nil
	}

	copyUntil := until
	return true, &copyUntil
}

// isInCooldown returns true when the provided `now` falls within the
// cooldown period — the last cooldownMinutes before endHR in a same-day
// or cross-midnight window. The cooldown duration is a proximity check
// (distance to end_hr), structurally distinct from checkTimeRangeAt's
// containment check, but uses the same clock parsing, timezone handling,
// and cross-midnight day adjustment.
func isInCooldown(now time.Time, startHR, endHR string, cooldownMinutes int) (bool, error) {
	startTime, endTime, err := validateScheduleWindow("start_hr", startHR, "end_hr", endHR)
	if err != nil {
		return false, err
	}

	endAt := time.Date(now.Year(), now.Month(), now.Day(), endTime.Hour(), endTime.Minute(), 0, 0, now.Location())

	if isCrossMidnightWindow(startTime, endTime) {
		startAt := time.Date(now.Year(), now.Month(), now.Day(), startTime.Hour(), startTime.Minute(), 0, 0, now.Location())
		if now.After(startAt) || now.Equal(startAt) {
			endAt = endAt.Add(24 * time.Hour)
		}
	}

	cooldownStart := endAt.Add(-time.Duration(cooldownMinutes) * time.Minute)
	return !now.Before(cooldownStart) && now.Before(endAt), nil
}

// isCrossMidnightWindow returns true when the start clock time is strictly
// after the end clock time indicating a window that spans midnight.
func isCrossMidnightWindow(startTime, endTime time.Time) bool {
	return startTime.After(endTime)
}

// checkTimeRangeAt determines whether the provided `now` falls inside the
// inclusive time range defined by startHR and endHR. It supports windows
// that cross midnight.
func checkTimeRangeAt(now time.Time, startHR, endHR string) (bool, error) {
	startTime, endTime, err := validateScheduleWindow("start time", startHR, "end time", endHR)
	if err != nil {
		return false, err
	}

	startAt := time.Date(now.Year(), now.Month(), now.Day(), startTime.Hour(), startTime.Minute(), 0, 0, now.Location())
	endAt := time.Date(now.Year(), now.Month(), now.Day(), endTime.Hour(), endTime.Minute(), 0, 0, now.Location())

	if isCrossMidnightWindow(startTime, endTime) {
		inRange := (now.After(startAt) || now.Equal(startAt)) || (now.Before(endAt) || now.Equal(endAt))
		return inRange, nil
	}

	inRange := (now.After(startAt) || now.Equal(startAt)) && (now.Before(endAt) || now.Equal(endAt))
	return inRange, nil
}

// nextMatchingWeekday advances t by 24h increments until its weekday is in
// the provided set. The set must be non-empty; the function loops at most 7
// times before returning (since a non-empty set guarantees a match within 7
// days).
func nextMatchingWeekday(t time.Time, weekdays map[time.Weekday]bool) time.Time {
	for i := 0; i < 7; i++ {
		if weekdays[t.Weekday()] {
			return t
		}
		t = t.Add(24 * time.Hour)
	}
	return t
}

// ---------------------------------------------------------------------------
// scheduler_mode: optimize (SPEC-0001 AC-13 … AC-18, ADR-0004, ADR-0005)
// ---------------------------------------------------------------------------

const (
	schedulerModeOptimize = "optimize"

	// optTickTimeout bounds the input fetches of one run (design §Tick budget).
	optTickTimeout = 50 * time.Second
	// optEarlyFireSnap: a run at most this long before an hour boundary is
	// the run for that boundary (timer jitter).
	optEarlyFireSnap = 2 * time.Second
	// optWatchdogInterval is the discharge SoC watchdog period (S-006c).
	optWatchdogInterval = 15 * time.Minute
	// optWatchdogSocMarginPct: the watchdog restores defaults when
	// SoC ≤ opt_min_soc_pct + this margin.
	optWatchdogSocMarginPct = 1.0
	// optWatchdogReadTimeout bounds the watchdog's SoC read.
	optWatchdogReadTimeout = 10 * time.Second
	// optTriggerMinInterval rate-limits trigger_now (S-011b, ADR-0005).
	optTriggerMinInterval = 60 * time.Second
	// optDiscoveryTimeout bounds a discovery republish (~60 messages).
	optDiscoveryTimeout = 30 * time.Second
	// optDecisionFallback is last_decision for a run that restores defaults.
	optDecisionFallback = "fallback_defaults"
	// optInputInverter names the inverter as the failed input of a write fallback.
	optInputInverter = "inverter"
)

// optShutdownTimeout is the deadline of the shutdown defaults write
// (ADR-0004 §2); a variable so tests can shorten it.
var optShutdownTimeout = 5 * time.Second

var (
	errTriggerRateLimited    = errors.New("rate limited")
	errSetReserveOptimize    = errors.New("not supported in scheduler_mode=optimize")
	errOptimizeBatteryReader = errors.New("battery reader not configured")
)

// dispatchWriter is the optimize-mode Modbus write path (ADR-0003/0004/0005).
// The runner creates it lazily, so dry-run optimizer runs never construct it.
type dispatchWriter interface {
	Apply(froniusIP string, cmd fronius.DispatchCommand) (fronius.Applied, error)
	SetDefaults(froniusIP string) error
	ForceChargeWithRevert(froniusIP string, targetPct int16, revertS int) error
}

type froniusDispatchWriter struct{}

func (froniusDispatchWriter) Apply(froniusIP string, cmd fronius.DispatchCommand) (fronius.Applied, error) {
	return fronius.ApplyDispatch(froniusIP, cmd)
}

func (froniusDispatchWriter) SetDefaults(froniusIP string) error {
	return fronius.SetDefaultsOrdered(froniusIP)
}

func (froniusDispatchWriter) ForceChargeWithRevert(froniusIP string, targetPct int16, revertS int) error {
	return fronius.ForceChargeWithRevert(froniusIP, targetPct, revertS)
}

// newDispatchWriter is a package-level factory, mirroring newBatteryWriter,
// so tests can inject a recording writer.
var newDispatchWriter = func() dispatchWriter {
	return froniusDispatchWriter{}
}

// optRun is the optimizer entry point used by the runner; a package variable
// so runner tests can inject results without the full input pipeline.
var optRun = runOptimization

// optLastState is the optimizer part of the last published state. It is
// merged into every state publish in optimize mode (design §MQTT).
type optLastState struct {
	action   optimize.Action
	rateW    float64
	cost     *float64
	horizonH int
	dryRun   bool
	reasons  []optimize.Reason
}

// optRunnerState is the optimize-mode state. It is owned by the runner
// goroutine; timer callbacks only Submit intents.
type optRunnerState struct {
	cfg    OptimizeConfig
	deps   optimizeDeps
	cache  profileCache
	writer dispatchWriter // created on first write

	hourTimer     *time.Timer
	pauseTimer    *time.Timer
	watchdogTimer *time.Timer

	last          *optLastState
	lastApplied   optimize.Action // "" = unknown (nothing written yet, or a write failed)
	overrideUntil time.Time       // manual force_charge override (ADR-0005)
	lastTriggerAt time.Time       // last accepted trigger_now
	currency      string          // last known price currency
	planCheck     *optPlanCheck   // slot to compare with the measured SoC at its end (AC-18b)
	ev            optEVGuard      // EV guard state (SPEC-0002)
}

// optPlanCheck remembers the slot a run applied (or, in dry run, would have
// applied) so the next run at the slot end can log planned vs. actual SoC.
type optPlanCheck struct {
	start, end         time.Time
	action             optimize.Action
	rateW              float64
	startSocPct        float64 // measured at the run that applied the slot
	plannedStartSocPct float64 // the plan's own start (grid step, floored at opt_min_soc_pct)
	plannedEndSocPct   float64
	plannedPVWh        float64 // the slot's PV forecast, scaled by its fraction
	plannedConsWh      float64 // the slot's consumption profile, scaled by its fraction
	capacityWh         float64
	dryRun             bool
	evGuard            bool              // the EV guard held the battery during the slot (SPEC-0002)
	changes            []optActionChange // later writes in the slot
}

// optActionChange is one write that changed the inverter's action within a
// slot; n counts identical repeats (same action and cause) in a row.
type optActionChange struct {
	at     string // local "15:04" of the first write
	action optimize.Action
	cause  string
	n      int
}

// String renders "15:04 action (cause)", with " x6" for repeats.
func (c optActionChange) String() string {
	s := fmt.Sprintf("%s %s (%s)", c.at, c.action, c.cause)
	if c.n > 1 {
		s += fmt.Sprintf(" x%d", c.n)
	}
	return s
}

// optNoteActionChange records a write that changed the inverter's action
// after the run that applied the current slot (EV guard, pause, set_defaults,
// force_charge, watchdog). Without it the plan check at the slot end named
// only the action the hour started with: in the live trial a 17:00 slot was
// logged as "hold" with a -1.3 kWh deviation, while self_consume had run from
// 17:01 after the EV guard released. Writes outside the remembered slot are
// ignored. A repeat of the previous entry (force_charge pressed six times)
// is counted instead of listed again.
func (r *Runner) optNoteActionChange(action optimize.Action, cause string) {
	pc := r.opt.planCheck
	now := r.now()
	if pc == nil || now.Before(pc.start) || !now.Before(pc.end) {
		return
	}
	if n := len(pc.changes); n > 0 && pc.changes[n-1].action == action && pc.changes[n-1].cause == cause {
		pc.changes[n-1].n++
		return
	}
	at := now.In(optLoc(r.opt.cfg)).Format("15:04")
	pc.changes = append(pc.changes, optActionChange{at: at, action: action, cause: cause, n: 1})
}

// optPlanCheckTolerance is how close a run must start to the remembered
// slot end for the plan check to be logged (normal hourly tick).
const optPlanCheckTolerance = 2 * time.Minute

func newOptRunnerState(cfg RunnerConfig) *optRunnerState {
	oc := cfg.Optimize
	if oc.FroniusIP == "" {
		oc.FroniusIP = cfg.FroniusIP
	}
	return &optRunnerState{
		cfg:      oc,
		deps:     cfg.OptimizeDeps,
		currency: strings.TrimSpace(cfg.MQTT.Currency),
	}
}

// mergeLast copies the last optimizer fields into a state payload that does
// not carry its own, so HA templates never lose them.
func (o *optRunnerState) mergeLast(p *mqtt.StatePayload) {
	if o.last == nil || p.OptAction != nil {
		return
	}
	action := string(o.last.action)
	rate := o.last.rateW
	horizon := o.last.horizonH
	dry := o.last.dryRun
	p.OptAction = &action
	p.OptRateW = &rate
	if o.last.cost != nil {
		c := *o.last.cost
		p.OptExpectedCost = &c
	}
	p.OptHorizonH = &horizon
	p.OptDryRun = &dry
	p.OptReasons = optReasonStrings(o.last.reasons)
}

// StartOptimizeTicker submits the startup run (AC-13). Each run re-arms the
// hourly wall-clock timer itself, so exactly one timer is pending.
func (r *Runner) StartOptimizeTicker() {
	if !r.Submit(mqtt.Intent{Kind: mqtt.IntentTick}) {
		u.Log.Warn("optimizer startup run dropped because runner queue is full")
	}
	r.optArmEVPoll()
}

// handleOptimizeIntent handles the intents whose semantics differ in
// optimize mode (ADR-0005). It returns false for intents the generic handler
// processes (shutdown, unknown kinds).
func (r *Runner) handleOptimizeIntent(ctx context.Context, intent mqtt.Intent) bool {
	switch intent.Kind {
	case mqtt.IntentTick:
		r.optimizeTick(ctx)
	case mqtt.IntentTriggerNow:
		r.optTriggerNow(ctx, intent)
	case mqtt.IntentPause:
		r.optPause(ctx, intent)
	case mqtt.IntentResume:
		r.optResume(ctx, intent)
	case mqtt.IntentSetDefaults:
		r.publishIntentAck(ctx, intent, r.optSetDefaults(ctx))
	case mqtt.IntentForceCharge:
		r.publishIntentAck(ctx, intent, r.optForceCharge(ctx, intent))
	case mqtt.IntentSetReserve:
		r.publishError(ctx, "runner", errSetReserveOptimize)
		r.publishIntentAck(ctx, intent, errSetReserveOptimize)
	case mqtt.IntentWatchdog:
		r.optWatchdog(ctx)
	case mqtt.IntentEVPoll:
		r.optEVPoll(ctx)
	default:
		return false
	}
	return true
}

// optimizeTick runs the optimizer once and applies, gates and publishes the
// result (design §Interactions). It always re-arms the hourly timer first.
func (r *Runner) optimizeTick(ctx context.Context) {
	o := r.opt
	loc := optLoc(o.cfg)
	clock := r.now()
	now := optSnapToBoundary(clock, loc)
	r.optScheduleNextHour(now, clock)

	started := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, optTickTimeout)
	res := optRun(runCtx, o.deps, o.cfg, now, &o.cache)
	cancel()

	r.optUpdateCurrency(ctx, res.Currency)
	r.optApplyResult(ctx, res, now, started)

	// The daily history rebuild runs after the write and publish, so it never
	// delays the hourly re-assertion (design §Concurrency, T-0155).
	if _, err := o.cache.rebuildIfStale(ctx, o.deps, o.cfg, now); err != nil {
		u.Log.Debugw("consumption profile rebuild failed; retrying later", "error", err)
	}
}

// optSnapToBoundary returns the next hour boundary when clock is at most
// optEarlyFireSnap before it, else clock.
func optSnapToBoundary(clock time.Time, loc *time.Location) time.Time {
	next := optNextBoundary(clock, loc)
	if next.Sub(clock) <= optEarlyFireSnap {
		return next.In(clock.Location())
	}
	return clock
}

// optNextBoundary is the start of the hour after the one containing now.
func optNextBoundary(now time.Time, loc *time.Location) time.Time {
	return optSlot0(now, loc).Add(time.Hour)
}

// optScheduleNextHour replaces the pending hourly timer with one that fires
// at the next wall-clock boundary after now; the delay is measured from the
// real clock.
func (r *Runner) optScheduleNextHour(now, clock time.Time) {
	o := r.opt
	optStopTimer(&o.hourTimer)
	next := optNextBoundary(now, optLoc(o.cfg))
	delay := next.Sub(clock)
	if delay < 0 {
		delay = 0
	}
	o.hourTimer = newTimerFunc(delay, func() {
		if !r.Submit(mqtt.Intent{Kind: mqtt.IntentTick}) {
			u.Log.Warn("optimizer hourly run dropped because runner queue is full")
		}
	})
	u.Log.Debugw("optimizer next run scheduled", "at", next.Format(time.RFC3339), "delay", delay.Round(time.Second))
}

func optStopTimer(t **time.Timer) {
	if *t != nil {
		(*t).Stop()
		*t = nil
	}
}

// optSelectSlot returns the index of the plan slot containing at, or -1
// (write-time slot selection, S-008).
func optSelectSlot(plan optimize.Plan, at time.Time) int {
	for i, h := range plan.Hours {
		if !at.Before(h.Start) && at.Before(h.End) {
			return i
		}
	}
	return -1
}

// optFallbackAfterPlan turns a successful result into a fallback, dropping
// the plan's action code from the reasons.
func optFallbackAfterPlan(res optimizeResult, reason optimize.Reason, input string, err error) optimizeResult {
	kept := make([]optimize.Reason, 0, len(res.Reasons))
	for _, r := range res.Reasons {
		if r != res.Plan.Reason {
			kept = append(kept, r)
		}
	}
	res.Reasons = kept
	return optFail(res, reason, input, err)
}

// optDecision is the action the runner applies (or would apply) this run.
type optDecision struct {
	action    optimize.Action
	rateW     float64
	chargePct *int16
}

// optApplyResult selects the slot, applies the gates and writes, then
// publishes state and plan (design §Interactions).
func (r *Runner) optApplyResult(ctx context.Context, res optimizeResult, now, started time.Time) {
	o := r.opt
	loc := optLoc(o.cfg)
	clock := r.now()
	paused, pauseUntil := r.pauseStateAt(clock)
	if !o.overrideUntil.IsZero() && !clock.Before(o.overrideUntil) {
		u.Log.Infow("manual override ended", "until", o.overrideUntil.In(loc).Format(time.RFC3339))
		o.overrideUntil = time.Time{}
	}
	override := !o.overrideUntil.IsZero()
	r.optLogPlanCheck(res, now)

	// Write-time slot selection (S-008): normally slot 0; a run that overran
	// into the next hour applies the slot containing the write time.
	k := -1
	if res.Fallback == nil {
		writeAt := clock
		if writeAt.Before(now) {
			writeAt = now // early timer fire snapped to the boundary
		}
		k = optSelectSlot(res.Plan, writeAt)
		switch {
		case k < 0:
			res = optFallbackAfterPlan(res, optimize.ReasonFallbackInvalidPlan, optInputPlan,
				fmt.Errorf("no plan slot contains the write time %s", writeAt.In(loc).Format(time.RFC3339)))
		case k > 0:
			u.Log.Warnw("optimizer run overran its hour; applying a later plan slot",
				"slot", k, "start", res.Plan.Hours[k].Start.In(loc).Format(time.RFC3339))
		}
	}

	d := optDecision{action: optimize.ActionSelfConsume}
	if res.Fallback == nil {
		ph := res.Plan.Hours[k]
		bat := res.Battery
		bat.E0Wh = ph.StartWh
		d.action, d.rateW = optimize.ApplyDischargeMargin(ph.Action, ph.RateW, ph.Fraction, bat)
		if d.action != ph.Action || d.rateW != ph.RateW {
			u.Log.Debugw("rate clamped", "requested_rate_w", ph.RateW, "rate_w", d.rateW, "limit", "discharge_soc_margin", "action", d.action)
		}
		if d.action != ph.Action {
			// The reason follows the applied action, not the plan's discharge.
			res.Reasons = optReplaceReason(res.Reasons, res.Plan.Reason, optimize.ReasonDischargeClampedMinSoc)
			res.Plan.Reason = optimize.ReasonDischargeClampedMinSoc
		}
	}

	slotEnd := time.Time{}
	if k >= 0 {
		slotEnd = res.Plan.Hours[k].End
	}
	d = r.optEVGuardOnRun(d, res.Fallback == nil && !paused && !override, slotEnd)

	var overrideReason optimize.Reason
	switch {
	case paused:
		overrideReason = optimize.ReasonPaused
	case override:
		overrideReason = optimize.ReasonManualOverride
	}

	// Gates: dry-run, paused and manual override publish only.
	switch {
	case o.cfg.DryRun:
		if res.Fallback != nil {
			u.Log.Warnw("optimizer fallback (dry run): inverter defaults would be restored",
				"reason", res.Fallback.Reason, "input", res.Fallback.Input, "error", optErrString(res.Fallback.Err),
				"next_run", optNextBoundary(now, loc).Format(time.RFC3339))
		}
		r.optLogWouldWrite(d)
	case paused:
		u.Log.Infow("optimizer run paused: plan published, no write", "action", d.action, "rate_w", d.rateW, "reason", optimize.ReasonPaused)
	case override:
		u.Log.Infow("manual override active: plan published, no write", "action", d.action, "rate_w", d.rateW,
			"reason", optimize.ReasonManualOverride, "until", o.overrideUntil.In(loc).Format(time.RFC3339))
	case res.Fallback != nil:
		r.optRestoreForFallback(ctx, res, now)
	default:
		applied, failReason, err := r.optDispatch(ctx, d)
		if err != nil {
			res = optFallbackAfterPlan(res, failReason, optInputInverter, err)
			d = optDecision{action: optimize.ActionSelfConsume}
		} else {
			d = applied
		}
	}
	if res.Fallback != nil {
		o.optEVGuardIneligible("fallback")
	}
	if o.ev.active {
		res.Reasons = optSortReasons(append(res.Reasons, optimize.ReasonEVGuardHold))
	}

	o.planCheck = nil
	if res.Fallback == nil && k >= 0 && res.HasSoc && !paused && !override {
		ph := res.Plan.Hours[k]
		o.planCheck = &optPlanCheck{
			start: ph.Start, end: ph.End, action: d.action, rateW: d.rateW,
			startSocPct: res.StartSocPct, plannedEndSocPct: ph.SocEndPct,
			plannedStartSocPct: res.Battery.MinSocPct + ph.StartWh/res.Battery.CapacityWh*100,
			plannedPVWh:        ph.PVWh, plannedConsWh: ph.ConsumptionWh,
			capacityWh: res.Battery.CapacityWh, dryRun: o.cfg.DryRun, evGuard: o.ev.active,
		}
	}

	r.optArmWatchdog()
	r.optPublishRun(ctx, res, k, d, overrideReason, paused, pauseUntil, now, started)
}

// optLogPlanCheck logs planned vs. actual SoC for the slot the previous run
// applied, when this run starts at that slot's end (AC-18b). Runs in the
// middle of a slot (trigger_now, resume) do not log; they replace the
// remembered slot with their own decision.
func (r *Runner) optLogPlanCheck(res optimizeResult, now time.Time) {
	pc := r.opt.planCheck
	if pc == nil || !res.HasSoc {
		return
	}
	if d := now.Sub(pc.end); d < -optPlanCheckTolerance || d > optPlanCheckTolerance {
		return
	}
	// The planned change is measured from the plan's own start SoC: below
	// opt_min_soc_pct (inverter floor 7 %, soft minimum 10 %) the plan starts
	// at the minimum, and comparing with the measured start would report a
	// deviation that never happened.
	plannedWh := (pc.plannedEndSocPct - pc.plannedStartSocPct) / 100 * pc.capacityWh
	actualWh := (res.StartSocPct - pc.startSocPct) / 100 * pc.capacityWh
	loc := optLoc(r.opt.cfg)
	fields := []any{
		"slot_start", pc.start.In(loc).Format(time.RFC3339),
		"action", pc.action, // the action applied at the start of the slot
		"rate_w", pc.rateW,
		"dry_run", pc.dryRun,
		"planned_soc_start_pct", math.Round(pc.plannedStartSocPct*10) / 10,
		"actual_soc_start_pct", math.Round(pc.startSocPct*10) / 10,
		"planned_soc_end_pct", math.Round(pc.plannedEndSocPct*10) / 10,
		"actual_soc_pct", math.Round(res.StartSocPct*10) / 10,
		"planned_delta_wh", math.Round(plannedWh),
		"actual_delta_wh", math.Round(actualWh),
		"deviation_wh", math.Round(actualWh - plannedWh),
		// The slot's inputs, so a deviation can be traced to the PV forecast
		// or the consumption profile against the HA sensors.
		"planned_pv_wh", math.Round(pc.plannedPVWh),
		"planned_consumption_wh", math.Round(pc.plannedConsWh),
		"ev_guard", pc.evGuard,
	}
	if len(pc.changes) > 0 {
		// The deviation then covers several actions, not only "action".
		changes := make([]string, len(pc.changes))
		for i, c := range pc.changes {
			changes[i] = c.String()
		}
		fields = append(fields, "action_changes", changes)
	}
	u.Log.Infow("plan check", fields...)
}

// optRestoreForFallback writes defaults for a fallback result (AC-16).
func (r *Runner) optRestoreForFallback(ctx context.Context, res optimizeResult, now time.Time) {
	o := r.opt
	err := r.optWriteDefaults()
	u.Log.Warnw("optimizer fallback: inverter defaults restored",
		"reason", res.Fallback.Reason, "input", res.Fallback.Input, "error", optErrString(res.Fallback.Err),
		"next_run", optNextBoundary(now, optLoc(o.cfg)).Format(time.RFC3339))
	if err != nil {
		u.Log.Errorw("defaults restore failed; inverter state unknown", "reason", res.Fallback.Reason, "error", err)
		r.publishError(ctx, "optimizer", err)
	}
}

// optDispatch writes the decision through ApplyDispatch (rates clamped,
// revert timeout 3900 s, read-back). On failure it ensures one defaults
// restore was attempted and returns the fallback reason with the error.
func (r *Runner) optDispatch(ctx context.Context, d optDecision) (optDecision, optimize.Reason, error) {
	o := r.opt
	cmd := r.optDispatchCommand(d)
	applied, err := r.optWriter().Apply(o.cfg.FroniusIP, cmd)
	if err != nil {
		reason := optimize.ReasonFallbackModbusWriteFailed
		if errors.Is(err, fronius.ErrInverterLimits) {
			reason = optimize.ReasonFallbackInverterLimits
		}
		restored := applied.DefaultsRestored
		if errors.Is(err, fronius.ErrModbusWrite) {
			// ApplyDispatch already attempted the defaults restore once.
			o.lastApplied = ""
			if restored {
				o.lastApplied = optimize.ActionSelfConsume
			}
		} else {
			// Nothing was written (limits, invalid command or connect error).
			restored = r.optWriteDefaults() == nil
		}
		u.Log.Errorw("dispatch failed", "reason", reason, "action", d.action, "error", err)
		if !restored {
			u.Log.Errorw("defaults restore failed; inverter state unknown", "reason", reason)
		}
		r.publishError(ctx, "optimizer", err)
		return d, reason, err
	}

	o.lastApplied = optimize.Action(applied.Mode)
	d.action = optimize.Action(applied.Mode)
	if d.action != optimize.ActionCharge && d.action != optimize.ActionDischarge {
		d.rateW = 0
	}
	if d.action == optimize.ActionCharge && applied.Limits.WChaMaxW > 0 {
		pct := int16(math.Round(math.Min(d.rateW, applied.Limits.WChaMaxW) / applied.Limits.WChaMaxW * 100))
		d.chargePct = &pct
	}
	// rate_w is the planned battery-side rate; setpoint_w is what the inverter
	// was told (AC-side for discharge, see optDispatchCommand).
	u.Log.Infow("dispatch applied", "action", applied.Mode, "rate_w", d.rateW, "setpoint_w", math.Round(cmd.RateW),
		"registers", optRegisterList(applied.Writes), "readback_ok", true)
	return d, "", nil
}

// optDispatchCommand builds the dispatch command for a decision.
//
// The plan's rates are battery-side: the energy that goes into or comes out
// of the battery (SPEC-0001 AC-11c). The Gen24 does not treat its two rate
// registers the same way, so the setpoint differs per direction:
//
//   - Charge: the Gen24 charge setpoint is battery-side. In the live trial the
//     stored energy matched the setpoint within 1-2 % (ratio 0.98-1.01), so
//     the planned rate is written as is.
//   - Discharge: the Gen24 discharge setpoint is AC-side, the power the
//     inverter delivers to the house and grid. The battery supplies that plus
//     the conversion loss: in the live trial the SoC fell by 1.10 x the
//     setpoint at 2.4-3.0 kW, which is exactly 1/sqrt(0.83). Writing the
//     planned battery-side rate therefore drained ~10 % more than planned and
//     left the battery short later in the night. The setpoint is the planned
//     battery-side rate times the discharge efficiency sqrt(eta), so the
//     battery delivers what the plan expects. The rate limit is converted the
//     same way, because opt_max_discharge_rate_w is battery-side too.
func (r *Runner) optDispatchCommand(d optDecision) fronius.DispatchCommand {
	cmd := fronius.DispatchCommand{
		Mode:    fronius.DispatchMode(d.action),
		RevertS: fronius.RevertSeconds,
	}
	switch d.action {
	case optimize.ActionCharge:
		cmd.RateW = d.rateW
		cmd.MaxRateW = r.opt.cfg.MaxChargeW
	case optimize.ActionDischarge:
		etaD := optimize.DischargeEfficiency(r.opt.cfg.Efficiency)
		cmd.RateW = d.rateW * etaD
		cmd.MaxRateW = r.opt.cfg.MaxDischargeW * etaD
	}
	return cmd
}

// optLogWouldWrite logs the registers a live run would write (AC-15). No
// Modbus connection is opened, so WChaMax is assumed from the configured rate
// limits; the live path reads it from the inverter.
func (r *Runner) optLogWouldWrite(d optDecision) {
	lim := fronius.InverterLimits{WChaMaxW: optAssumedWChaMax(r.opt.cfg), RateSF: fronius.ExpectedRateSF}
	cmd := r.optDispatchCommand(d)
	regs, err := fronius.BuildDispatchRegisters(cmd, lim)
	fields := []any{"action", d.action, "rate_w", d.rateW, "setpoint_w", math.Round(cmd.RateW), "registers", optRegisterList(regs), "assumed_wchamax_w", lim.WChaMaxW}
	if err != nil {
		fields = append(fields, "error", err)
	}
	u.Log.Infow("dry run: no Modbus registers written", fields...)
}

// optAssumedWChaMax is the WChaMax used for the dry-run register preview.
func optAssumedWChaMax(cfg OptimizeConfig) float64 {
	w := math.Max(cfg.MaxChargeW, cfg.MaxDischargeW)
	return math.Min(math.Max(w, fronius.MinWChaMaxW), fronius.MaxWChaMaxW)
}

// optRegisterList renders register writes as "addr=value" in write order.
func optRegisterList(writes []fronius.RegisterWrite) []string {
	out := make([]string, 0, len(writes))
	for _, w := range writes {
		out = append(out, fmt.Sprintf("%d=%d", w.Addr, w.Value))
	}
	return out
}

func optErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// optWriter returns the dispatch writer, creating it on first use.
func (r *Runner) optWriter() dispatchWriter {
	if r.opt.writer == nil {
		r.opt.writer = newDispatchWriter()
	}
	return r.opt.writer
}

// optWriteDefaults writes ordered defaults and tracks lastApplied. Callers
// decide about dry-run; explicit user commands write even in dry-run.
// optUserForcedInDryRun reports whether, in dry-run, a user force_charge
// command left the inverter in a forced state. In dry-run the optimizer never
// writes, so only that explicit command can (review I-01).
func (o *optRunnerState) optUserForcedInDryRun() bool {
	return o.cfg.DryRun && o.lastApplied == optimize.ActionCharge
}

func (r *Runner) optWriteDefaults() error {
	o := r.opt
	if err := r.optWriter().SetDefaults(o.cfg.FroniusIP); err != nil {
		o.lastApplied = ""
		return err
	}
	o.lastApplied = optimize.ActionSelfConsume
	return nil
}

// optPublishRun publishes the state and plan of one run and logs the Info
// "optimizer run" line (UX §7.1, §7.2).
func (r *Runner) optPublishRun(ctx context.Context, res optimizeResult, k int, d optDecision, overrideReason optimize.Reason,
	paused bool, pauseUntil *time.Time, now, started time.Time) {
	o := r.opt
	loc := optLoc(o.cfg)
	reasons := res.reasonsWithOverride(overrideReason)
	reason := ""
	if len(reasons) > 0 {
		reason = string(reasons[0])
	}

	var hours []optimize.PlanHour
	var cost, terminal *float64
	if res.Fallback == nil && k >= 0 {
		hours = res.Plan.Hours[k:]
		c, tv := res.Plan.ExpectedCost, res.Plan.TerminalValue
		cost, terminal = &c, &tv
	}

	o.last = &optLastState{
		action:   d.action,
		rateW:    d.rateW,
		cost:     cost,
		horizonH: len(hours),
		dryRun:   o.cfg.DryRun,
		reasons:  reasons,
	}

	lastDecision := string(d.action)
	switch {
	case paused:
		lastDecision = "paused"
	case res.Fallback != nil:
		lastDecision = optDecisionFallback
	}
	payload := r.newCommandPayload(lastDecision, reason, now)
	payload.Paused = paused
	payload.NextRun = pauseUntil
	if res.HasSoc {
		soc := res.StartSocPct
		payload.BatterySOCPct = &soc
	}
	if res.Battery.CapacityWh > 0 {
		capWh := res.Battery.CapacityWh
		payload.BatteryCapacityWh = &capWh
	}
	payload.ChargePct = d.chargePct
	r.publishState(payload)

	plan := mqtt.PlanPayload{
		Timestamp:     r.now().UTC(),
		DryRun:        o.cfg.DryRun,
		Currency:      o.currency,
		HorizonH:      len(hours),
		ExpectedCost:  cost,
		TerminalValue: terminal,
		Reason:        reason,
		Reasons:       optReasonStrings(reasons),
		Hours:         make([]mqtt.PlanEntry, 0, len(hours)),
	}
	if res.HasSoc {
		soc := res.StartSocPct
		plan.StartSocPct = &soc
	}
	if len(hours) > 0 {
		end := hours[len(hours)-1].End.In(loc)
		plan.HorizonEnd = &end
	}
	for _, h := range hours {
		plan.Hours = append(plan.Hours, mqtt.PlanEntry{
			Start:         h.Start.In(loc),
			Action:        string(h.Action),
			RateW:         h.RateW,
			SocEndPct:     h.SocEndPct,
			Buy:           h.Buy,
			Sell:          h.Sell,
			ConsumptionWh: h.ConsumptionWh,
			PvWh:          h.PVWh,
		})
	}
	r.optPublishPlan(ctx, plan)

	u.Log.Infow("optimizer run",
		"dry_run", o.cfg.DryRun,
		"action", d.action,
		"rate_w", d.rateW,
		"reason", reason,
		"reasons", optReasonStrings(reasons),
		"expected_cost", cost,
		"terminal_value", terminal,
		"horizon_h", len(hours),
		"soc_pct", res.StartSocPct,
		"duration_ms", time.Since(started).Milliseconds(),
		"price_min", res.PriceMin,
		"price_max", res.PriceMax,
		"price_mean", res.PriceMean,
		"profile_sum_wh", res.ProfileSumWh,
	)
}

// optPublishPlan publishes the retained <prefix>/plan topic.
func (r *Runner) optPublishPlan(ctx context.Context, plan mqtt.PlanPayload) {
	if !r.cfg.MQTT.Enabled {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, const_mqtt_op_timeout)
	defer cancel()
	mqtt.PublishPlan(pctx, r.client, r.cfg.MQTT.TopicPrefix, plan)
}

// optPublishEmptyPlan publishes a plan without hours (every plan sensor goes
// unavailable), used when a safety path replaces the applied plan.
func (r *Runner) optPublishEmptyPlan(ctx context.Context, reasons []optimize.Reason) {
	reason := ""
	if len(reasons) > 0 {
		reason = string(reasons[0])
	}
	r.optPublishPlan(ctx, mqtt.PlanPayload{
		Timestamp: r.now().UTC(),
		DryRun:    r.opt.cfg.DryRun,
		Currency:  r.opt.currency,
		Reason:    reason,
		Reasons:   optReasonStrings(reasons),
	})
}

func optReasonStrings(rs []optimize.Reason) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, string(r))
	}
	return out
}

// optUpdateCurrency republishes discovery once when the price currency first
// becomes known or changes, so the Expected Cost unit follows it (UX O-3,
// T-0115). The MQTT client keeps the currency for reconnect republishes.
func (r *Runner) optUpdateCurrency(ctx context.Context, currency string) {
	o := r.opt
	currency = strings.TrimSpace(currency)
	if currency == "" || currency == o.currency {
		return
	}
	o.currency = currency
	r.cfg.MQTT.Currency = currency
	if setter, ok := r.client.(interface{ SetCurrency(string) }); ok {
		setter.SetCurrency(currency)
	}
	if !r.cfg.MQTT.Enabled || !r.cfg.MQTT.HADiscovery {
		return
	}
	u.Log.Infow("price currency changed; republishing discovery", "currency", currency)
	dctx, cancel := context.WithTimeout(ctx, optDiscoveryTimeout)
	defer cancel()
	mqtt.PublishDiscovery(dctx, r.client, r.cfg.MQTT, appVersion)
}

// optArmWatchdog (re)arms the 15-minute SoC watchdog while a live discharge
// is applied and cancels it otherwise (ADR-0004 §6, S-006c).
func (r *Runner) optArmWatchdog() {
	o := r.opt
	optStopTimer(&o.watchdogTimer)
	if o.cfg.DryRun || o.lastApplied != optimize.ActionDischarge {
		return
	}
	o.watchdogTimer = newTimerFunc(optWatchdogInterval, func() {
		if !r.Submit(mqtt.Intent{Kind: mqtt.IntentWatchdog}) {
			u.Log.Warn("optimizer watchdog check dropped because runner queue is full")
		}
	})
}

// optWatchdog checks SoC during a forced discharge. At SoC ≤ min + 1 % or on
// a read failure it restores defaults and stops checking until the next
// hourly run. It never starts a forced action.
func (r *Runner) optWatchdog(ctx context.Context) {
	o := r.opt
	if o.cfg.DryRun || o.lastApplied != optimize.ActionDischarge {
		optStopTimer(&o.watchdogTimer)
		return
	}

	soc, readErr := r.optReadSoc(ctx)
	if readErr == nil && soc > o.cfg.MinSocPct+optWatchdogSocMarginPct {
		u.Log.Debugw("watchdog: SoC above minimum", "soc_pct", soc, "min_soc_pct", o.cfg.MinSocPct)
		r.optArmWatchdog()
		return
	}

	writeErr := r.optWriteDefaults()
	optStopTimer(&o.watchdogTimer)
	if writeErr == nil {
		r.optNoteActionChange(optimize.ActionSelfConsume, "watchdog")
	}
	if readErr != nil {
		u.Log.Warnw("watchdog: SoC read failed, defaults restored", "reason", optimize.ReasonWatchdogMinSoc,
			"min_soc_pct", o.cfg.MinSocPct, "error", readErr)
	} else {
		u.Log.Infow("watchdog: SoC near minimum, defaults restored", "soc_pct", soc, "min_soc_pct", o.cfg.MinSocPct)
	}
	if writeErr != nil {
		u.Log.Errorw("defaults restore failed; inverter state unknown", "reason", optimize.ReasonWatchdogMinSoc, "error", writeErr)
		r.publishError(ctx, "optimizer", writeErr)
	}

	reasons := []optimize.Reason{optimize.ReasonWatchdogMinSoc}
	o.last = &optLastState{action: optimize.ActionSelfConsume, dryRun: o.cfg.DryRun, reasons: reasons}
	payload := r.newCommandPayload(optDecisionFallback, string(optimize.ReasonWatchdogMinSoc), r.now())
	if readErr == nil {
		payload.BatterySOCPct = &soc
	}
	r.publishState(payload)
	r.optPublishEmptyPlan(ctx, reasons)
}

// optReadSoc reads the SoC for the watchdog through pkg/storage.
func (r *Runner) optReadSoc(ctx context.Context) (float64, error) {
	o := r.opt
	if o.deps.Battery == nil {
		return 0, errOptimizeBatteryReader
	}
	rctx, cancel := context.WithTimeout(ctx, optWatchdogReadTimeout)
	defer cancel()
	soc, _, err := o.deps.Battery.Read(rctx, o.cfg.FroniusIP)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(soc) || math.IsInf(soc, 0) || soc < 0 || soc > 100 {
		return 0, fmt.Errorf("implausible SoC %v", soc)
	}
	return soc, nil
}

// optTriggerNow runs the optimizer immediately, at most once per 60 s.
func (r *Runner) optTriggerNow(ctx context.Context, intent mqtt.Intent) {
	o := r.opt
	now := r.now()
	if !o.lastTriggerAt.IsZero() && now.Sub(o.lastTriggerAt) < optTriggerMinInterval {
		u.Log.Infow("trigger_now rate limited", "min_interval_s", int(optTriggerMinInterval.Seconds()))
		r.publishIntentAck(ctx, intent, errTriggerRateLimited)
		return
	}
	o.lastTriggerAt = now
	r.optimizeTick(ctx)
	r.publishIntentAck(ctx, intent, nil)
}

// optPause writes defaults first when live, then pauses (ADR-0005). If the
// defaults write fails the pause is still set and the ack carries the error.
// The pause timer submits a run when the pause expires.
func (r *Runner) optPause(ctx context.Context, intent mqtt.Intent) {
	o := r.opt
	o.optEVGuardIneligible("paused")
	var err error
	if !o.cfg.DryRun || o.optUserForcedInDryRun() {
		o.overrideUntil = time.Time{}
		if err = r.optWriteDefaults(); err != nil {
			u.Log.Errorw("defaults restore failed; inverter state unknown", "reason", optimize.ReasonPaused, "error", err)
			r.publishError(ctx, "pause", err)
		} else {
			u.Log.Infow("pause: inverter defaults restored", "reason", optimize.ReasonPaused)
			r.optNoteActionChange(optimize.ActionSelfConsume, "pause")
		}
	}
	optStopTimer(&o.watchdogTimer)
	r.setPause(intent.PauseUntil)

	optStopTimer(&o.pauseTimer)
	if intent.PauseUntil != nil {
		delay := intent.PauseUntil.Sub(r.now())
		if delay < 0 {
			delay = 0
		}
		o.pauseTimer = newTimerFunc(delay, func() {
			if !r.Submit(mqtt.Intent{Kind: mqtt.IntentTick}) {
				u.Log.Warn("optimizer pause-expiry run dropped because runner queue is full")
			}
		})
	}

	if o.last != nil {
		last := *o.last
		if err == nil && !o.cfg.DryRun {
			last.action, last.rateW = optimize.ActionSelfConsume, 0
		}
		last.reasons = optSortReasons(append(append([]optimize.Reason(nil), last.reasons...), optimize.ReasonPaused))
		o.last = &last
	}
	payload := r.newCommandPayload("paused", string(optimize.ReasonPaused), r.now())
	payload.Paused = true
	if intent.PauseUntil != nil {
		until := intent.PauseUntil.UTC()
		payload.NextRun = &until
	}
	r.publishState(payload)
	r.publishIntentAck(ctx, intent, err)
}

// optResume clears the pause and runs the optimizer immediately (ADR-0005).
func (r *Runner) optResume(ctx context.Context, intent mqtt.Intent) {
	r.clearPause()
	optStopTimer(&r.opt.pauseTimer)
	payload := r.newCommandPayload("resume", "schedule resumed by command", r.now())
	r.publishState(payload)
	r.publishIntentAck(ctx, intent, nil)
	r.optimizeTick(ctx)
}

// optSetDefaults is always accepted, paused or not, and writes even in
// dry-run because it is explicit user intent. It ends a manual override.
func (r *Runner) optSetDefaults(ctx context.Context) error {
	o := r.opt
	o.overrideUntil = time.Time{}
	o.optEVGuardIneligible("set_defaults")
	optStopTimer(&o.watchdogTimer)
	if err := r.optWriteDefaults(); err != nil {
		r.publishError(ctx, "set_defaults", err)
		return err
	}
	r.optNoteActionChange(optimize.ActionSelfConsume, "set_defaults")
	if o.last != nil {
		last := *o.last
		last.action, last.rateW = optimize.ActionSelfConsume, 0
		o.last = &last
	}
	now := r.now()
	paused, until := r.pauseStateAt(now)
	payload := r.newCommandPayload(string(mqtt.IntentSetDefaults), "set defaults command executed", now)
	payload.Paused = paused
	payload.NextRun = until
	r.publishState(payload)
	return nil
}

// optForceCharge writes the force charge with the revert timeout and starts
// a manual override until duration_s or the end of the current hour
// (ADR-0005). It is rejected while paused, as in the other modes.
func (r *Runner) optForceCharge(ctx context.Context, intent mqtt.Intent) error {
	o := r.opt
	now := r.now()
	if paused, _ := r.pauseStateAt(now); paused {
		err := fmt.Errorf("%w: %s", errRunnerPaused, intent.Kind)
		r.publishError(ctx, "force_charge", err)
		return err
	}
	targetPct, err := r.forceChargeTargetPct(ctx, intent)
	if err != nil {
		return err
	}
	loc := optLoc(o.cfg)
	revertS := fronius.ForceChargeRevertSeconds(intent.DurationS, now.In(loc))
	if err := r.optWriter().ForceChargeWithRevert(o.cfg.FroniusIP, targetPct, revertS); err != nil {
		o.lastApplied = ""
		r.publishError(ctx, "force_charge", err)
		return err
	}
	optStopTimer(&o.watchdogTimer)
	o.optEVGuardIneligible("force_charge")

	reason := "force charge command executed"
	if targetPct == 0 {
		// A 0 % force charge writes defaults (ForceChargeWithRevert).
		o.lastApplied = optimize.ActionSelfConsume
		o.overrideUntil = time.Time{}
	} else {
		o.lastApplied = optimize.ActionCharge
		r.optNoteActionChange(optimize.ActionCharge, "force_charge")
		until := optNextBoundary(now, loc)
		if intent.DurationS > 0 {
			until = now.Add(time.Duration(intent.DurationS) * time.Second)
		}
		o.overrideUntil = until
		reason = string(optimize.ReasonManualOverride)
		u.Log.Infow("manual override active", "until", until.In(loc).Format(time.RFC3339), "charge_pct", targetPct, "revert_s", revertS)
		if o.last != nil {
			last := *o.last
			last.action, last.rateW = optimize.ActionCharge, 0
			last.reasons = optSortReasons(append(append([]optimize.Reason(nil), last.reasons...), optimize.ReasonManualOverride))
			o.last = &last
		}
	}

	payload := r.newCommandPayload(string(mqtt.IntentForceCharge), reason, now)
	payload.ChargePct = &targetPct
	r.publishState(payload)
	return nil
}

// optimizeShutdown stops the optimizer timers and, when live and the last
// applied action is not self_consume, writes defaults within
// optShutdownTimeout before Run returns (ADR-0004 §2, S-001).
func (r *Runner) optimizeShutdown() {
	o := r.opt
	if o == nil {
		return
	}
	optStopTimer(&o.hourTimer)
	optStopTimer(&o.pauseTimer)
	optStopTimer(&o.watchdogTimer)
	optStopTimer(&o.ev.timer)
	if o.cfg.DryRun && !o.optUserForcedInDryRun() {
		return
	}
	if o.lastApplied == optimize.ActionSelfConsume {
		return
	}

	if err := optSetDefaultsWithin(r.optWriter(), o.cfg.FroniusIP, optShutdownTimeout); err != nil {
		u.Log.Errorw("defaults restore failed; inverter state unknown", "reason", "shutdown", "error", err)
		return
	}
	o.lastApplied = optimize.ActionSelfConsume
	u.Log.Info("shutdown: inverter defaults restored")
}
