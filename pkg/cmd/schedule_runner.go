package cmd

import (
	"context"
	"errors"
	"fmt"
	"sbam/pkg/fronius"
	"sbam/pkg/mqtt"
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
	BattMinSoC         float64 // % of capacity the inverter never discharges below
	GridImportLimit    float64 // W; 0 = disabled
	GridImportMargin   float64 // W kept free under GridImportLimit
	GridChargePriority string  // "battery" (default) or "loads"
	PWBaseLoad         float64 // W; 0 = PWConsumption/24
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
	Hold(froniusIP string) error
	ControlMode(froniusIP string) (int16, error)
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

func (w froniusBatteryWriter) Hold(froniusIP string) error {
	return fronius.ApplyHold(w.sunspecType, froniusIP, w.ports()...)
}

func (w froniusBatteryWriter) ControlMode(froniusIP string) (int16, error) {
	return fronius.ReadStorageControlMode(w.sunspecType, froniusIP, w.ports()...)
}

var newBatteryWriter = func(sunspecType fronius.SunSpecType) batteryWriter {
	return froniusBatteryWriter{sunspecType: sunspecType}
}

// newTimerFunc is a package-level factory for one-shot timers. Tests may
// override it to inject deterministic timer behavior.
var newTimerFunc = time.AfterFunc

// windowResetRetryDelay is how long after a failed window-end reset, or a
// failed read of the inverter mode outside every window, the next tick runs.
// Tests replace it.
var windowResetRetryDelay = 5 * time.Minute

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
	// activeWindowResets is true while the active window owes the inverter a
	// return to normal when it ends (set_defaults or hold_battery).
	activeWindowResets bool
	// manualOutsideWindow is true after a force_charge or hold command given
	// outside every window: the periodic guard leaves it in place. It lives in
	// memory only, so a restart hands that state back to the guard.
	manualOutsideWindow bool
	resetRetryTimer     *time.Timer
	limit               *limitSession
	limitStop           chan struct{}
	reserve             *reserveTelemetry // last reserve evaluation in the reserve window
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
		r.activeWindowResets = windowResets(active)
	} else {
		r.activeWindow = ""
		r.activeWindowResets = false
	}

	interval := 60 * time.Minute
	if active != nil && active.TickMinutes != nil {
		interval = time.Duration(*active.TickMinutes) * time.Minute
	}

	t := time.NewTicker(interval)
	r.tickerCh = t.C

	// Schedule the window-end reset: set_defaults when enabled, otherwise
	// at the window end for a hold_battery window, so the hold never
	// outlives the window.
	if active != nil {
		switch {
		case active.Defaults != nil && *active.Defaults:
			beforeEnd := 5 // default minutes
			if active.BeforeEndDefaults != nil {
				beforeEnd = *active.BeforeEndDefaults
			}
			r.scheduleDefaults(active, now, beforeEnd)
		case active.HoldBattery != nil && *active.HoldBattery:
			r.scheduleDefaults(active, now, 0)
		}
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
	r.activeWindowResets = false
}

// windowResets reports whether a window must leave the inverter in normal
// mode when it ends.
func windowResets(w *pw.Window) bool {
	return (w.Defaults != nil && *w.Defaults) || (w.HoldBattery != nil && *w.HoldBattery)
}

// anyWindowResets reports whether at least one configured window returns the
// inverter to normal, which is what entitles sbam to enforce it between windows.
func (r *Runner) anyWindowResets() bool {
	for i := range r.cfg.Windows {
		if windowResets(&r.cfg.Windows[i]) {
			return true
		}
	}
	return false
}

// resetOnWindowExit returns the inverter to normal as part of the tick that
// leaves a window. The window-end timer usually got there first; this tick
// cancels the timer when both land together (before_end_defaults_minutes: 0),
// and its intent can be dropped on a full queue. Writing defaults twice is
// harmless, so it runs regardless of what the timer did.
func (r *Runner) resetOnWindowExit(ctx context.Context) {
	r.stopLimitLoop()
	if err := r.handleSetDefaults(ctx, mqtt.Intent{Kind: mqtt.IntentSetDefaults, System: true}); err != nil {
		u.HandleError(err, "window-end reset failed")
		r.scheduleResetRetry()
	}
}

// enforceNormalOutsideWindow is the periodic safety net: outside every window
// the inverter must not be held or force-charged. It reads the real inverter
// mode, so it also covers a reset lost to a restart or a failed write. Commands
// given outside a window are left alone.
func (r *Runner) enforceNormalOutsideWindow(ctx context.Context) {
	if r.manualOutsideWindow || !r.anyWindowResets() {
		return
	}
	mode, err := r.writer.ControlMode(r.cfg.FroniusIP)
	if err != nil {
		u.Log.Warnw("cannot read inverter mode outside the charge windows", "error", err)
		r.scheduleResetRetry()
		return
	}
	if mode == 0 {
		return
	}
	u.Log.Warnw("inverter not in normal mode outside the charge windows, restoring it", "storctl_mod", mode)
	r.resetOnWindowExit(ctx)
}

// scheduleResetRetry asks for another tick soon, without waiting for the
// hourly tick between windows. Only the latest retry is kept.
func (r *Runner) scheduleResetRetry() {
	if r.resetRetryTimer != nil {
		r.resetRetryTimer.Stop()
	}
	r.resetRetryTimer = newTimerFunc(windowResetRetryDelay, func() {
		if !r.Submit(mqtt.Intent{Kind: mqtt.IntentTick}) {
			u.Log.Warn("window reset retry dropped because runner queue is full")
		}
	})
}

// scheduleDefaults parses the active window's end time and schedules a
// one-shot time.AfterFunc that fires set_defaults at
// (window.end - beforeEnd minutes). The timer is tracked in r.defaultsTimer
// so it can be cancelled on window change.
func (r *Runner) scheduleDefaults(active *pw.Window, now time.Time, beforeEnd int) {
	endTime, err := parseScheduleClock(active.End, "end")
	if err != nil {
		u.Log.Warnw("cannot parse window end for set_defaults timer", "window", active.Name, "end", active.End, "error", err)
		return
	}

	endAt := time.Date(now.Year(), now.Month(), now.Day(),
		endTime.Hour(), endTime.Minute(), 0, 0, now.Location())

	// If endAt is before or equal to now, advance by 24h (cross-midnight window).
	if !endAt.After(now) {
		endAt = endAt.AddDate(0, 0, 1)
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
		r.submitSystemDefaults()
	})
}

// submitSystemDefaults queues the window-end reset. It is a System intent, so
// it runs even while paused.
func (r *Runner) submitSystemDefaults() {
	if !r.Submit(mqtt.Intent{Kind: mqtt.IntentSetDefaults, System: true}) {
		u.Log.Warn("set_defaults dropped because runner queue is full")
	}
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
			startAt = startAt.AddDate(0, 0, 1)
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
	inChargeWindow, effectiveMaxCharge, effectiveForecastHorizon, effectiveConsumptionHorizon, activeWindowName, activeWindow := r.resolveActiveWindow(now)

	// In windows mode, restart the ticker when the active window changes.
	if r.cfg.SchedulerMode == "windows" {
		prevWindow := r.activeWindow
		if activeWindowName != prevWindow {
			u.Log.Infow("active window changed", "from", prevWindow, "to", activeWindowName)
			leftResettingWindow := r.activeWindowResets
			r.startWindowsTicker(now)
			if leftResettingWindow {
				r.resetOnWindowExit(ctx)
			}
		} else if !inChargeWindow {
			r.enforceNormalOutsideWindow(ctx)
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
		r.stopLimitLoop()
		payload := makeBasePayload("paused", "Forecast Charging execution skipped because sbam is paused", inChargeWindow, reserveWindowActive, r.cfg.SchedulerMode)
		payload.BatterySOCPct = &socPct
		payload.BatteryCapacityWh = &capacityMax
		payload.Paused = true
		payload.NextRun = pauseUntil
		r.publishState(payload)
		return nil
	}

	if !inChargeWindow {
		r.stopLimitLoop()
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
		if active := activeWindow; active != nil {
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
	var siteForecasts []pw.Forecasts

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
		if forecastRetrieved {
			siteForecasts = powerHandler.LastForecasts()
		}
	}

	holdBattery, effectiveReserve := r.windowReserve(now, activeWindow, capacityMax, siteForecasts)

	// Grid charge power: the active window's max_charge, limited by what
	// grid_import_limit leaves free right now.
	chargeCapW, allowanceW, limitErr := r.gridChargeAllowance(ctx, effectiveMaxCharge)
	if limitErr != nil {
		u.Log.Warnw("grid_import_limit", "error", limitErr)
		r.publishError(ctx, "grid_import_limit", limitErr)
	}

	u.Log.Infof("your Daily consumption is:%d Wh", int(effectiveConsumption))

	froniusHandler := newFronius(r.cfg.SunSpecType)
	chargePct, decision, reason, powerState, froniusErr := froniusHandler.Handler(
		solarPowerProduction,
		capacityToCharge,
		capacityMax,
		effectiveConsumption,
		chargeCapW,
		effectiveReserve,
		r.cfg.StartHR,
		r.cfg.EndHR,
		r.cfg.FroniusIP,
		reserveWindowActive,
		r.cfg.PWLWT,
		r.cfg.PWUPT,
		forecastRetrieved,
		holdBattery,
	)
	if froniusErr != nil {
		r.stopLimitLoop()
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

	payload.GridChargeAllowanceW = allowanceW
	r.followUpCharge(now, tickCharge{
		decision:      decision,
		powerState:    powerState,
		chargePct:     chargePct,
		ceilingW:      effectiveMaxCharge,
		capW:          chargeCapW,
		allowanceW:    allowanceW,
		capacityWh:    capacityMax,
		storedWh:      capacityMax - capacityToCharge,
		hold:          holdBattery,
		reserveWh:     effectiveReserve,
		reserveWindow: reserveWindowActive,
		window:        activeWindow,
	})

	r.publishState(payload)
	return nil
}

// handleIntent processes a single Intent from the queue and dispatches to
// the appropriate handler. It also acknowledges MQTT commands where
// required.
func (r *Runner) handleIntent(ctx context.Context, intent mqtt.Intent) {
	// Commands that write the battery or stop the schedule end the
	// grid_import_limit loop; ticks start or stop it themselves.
	switch intent.Kind {
	case mqtt.IntentShutdown, mqtt.IntentPause, mqtt.IntentForceCharge, mqtt.IntentSetDefaults, mqtt.IntentHold:
		r.stopLimitLoop()
	}

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
	case mqtt.IntentHold:
		err := r.handleHold(ctx, intent)
		r.publishIntentAck(ctx, intent, err)
		return
	case mqtt.IntentLimitCheck:
		r.handleLimitCheck(ctx)
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
	r.noteManualCommand()

	payload := r.newCommandPayload(string(mqtt.IntentForceCharge), "force charge command executed", r.now())
	payload.Paused = false
	payload.ChargePct = &targetPct
	r.publishState(payload)
	return nil
}

// noteManualCommand records a command given outside every window, so the
// periodic guard does not undo it. Inside a window the window end still wins.
func (r *Runner) noteManualCommand() {
	if inWindow, _, _, _, _, _ := r.resolveActiveWindow(r.now()); !inWindow {
		r.manualOutsideWindow = true
	}
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
		targetPct, err = r.resolveForceChargeTargetPct(ctx, intent.TargetPct)
		if err != nil {
			r.publishError(ctx, "force_charge", err)
			return 0, err
		}
	}

	return targetPct, nil
}

// resolveForceChargeTargetPct caps any requested force charge percentage
// by configured max_charge and current battery max capacity, and by the
// power free under grid_import_limit when it is set.
//
// Effective target is:
// min(requestedPct, min(max_charge, free)*100/pw_batt_max)
//
// When the cap is 0 the effective target becomes 0 (no charge). When the
// meter cannot be read under grid_import_limit the command is rejected.
func (r *Runner) resolveForceChargeTargetPct(ctx context.Context, requestedPct int16) (int16, error) {
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

	capW := r.cfg.MaxCharge
	if r.cfg.GridImportLimit > 0 {
		pf, err := readPowerFlow(ctx, r.cfg.FroniusIP)
		if err != nil {
			return 0, fmt.Errorf("grid_import_limit: cannot read the power flow for force_charge: %w", err)
		}
		capW = min(capW, max(0, r.cfg.GridImportLimit-r.cfg.GridImportMargin-pf.BaselineImportW()))
	}

	requestedLoad := capacityMax * float64(requestedPct) / 100.0
	targetPct := fronius.SetChargePower(capacityMax, requestedLoad, capW)

	return targetPct, nil
}

// handleSetDefaults triggers the battery writer to restore inverter
// defaults. It respects pause state and publishes the resulting state.
func (r *Runner) handleSetDefaults(ctx context.Context, intent mqtt.Intent) error {
	paused, pauseUntil := r.pauseStateAt(r.now())
	// The window-end reset (System) always runs: a pause must never leave a
	// force charge or a hold in place outside the window.
	if paused && !intent.System {
		err := fmt.Errorf("%w: %s", errRunnerPaused, intent.Kind)
		r.publishError(ctx, "set_defaults", err)
		return err
	}

	if err := r.writer.SetDefaults(r.cfg.FroniusIP); err != nil {
		r.publishError(ctx, "set_defaults", err)
		return err
	}
	r.manualOutsideWindow = false

	payload := r.newCommandPayload(string(mqtt.IntentSetDefaults), "set defaults command executed", r.now())
	payload.Paused = paused // the window-end reset leaves a pause in place
	payload.NextRun = pauseUntil
	r.publishState(payload)
	return nil
}

// handleHold blocks battery discharge (no force charge) until the next
// tick or command writes something else. It respects pause state, so send
// it before pausing.
func (r *Runner) handleHold(ctx context.Context, intent mqtt.Intent) error {
	paused, _ := r.pauseStateAt(r.now())
	if paused {
		err := fmt.Errorf("%w: %s", errRunnerPaused, intent.Kind)
		r.publishError(ctx, "hold", err)
		return err
	}

	if err := r.writer.Hold(r.cfg.FroniusIP); err != nil {
		r.publishError(ctx, "hold", err)
		return err
	}
	r.noteManualCommand()

	payload := r.newCommandPayload(string(mqtt.IntentHold), "hold command executed: battery discharge blocked", r.now())
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
	inChargeWindow, _, _, _, _, _ := r.resolveActiveWindow(now)

	reserveWindowActive, reserveErr := checkTimeRangeAt(now, r.cfg.BattReserveStartHR, r.cfg.BattReserveEndHR)
	if reserveErr != nil {
		u.HandleError(reserveErr, "unable to compute reserve window status")
		reserveWindowActive = false
	}

	return makeBasePayload(lastDecision, reason, inChargeWindow, reserveWindowActive, r.cfg.SchedulerMode)
}

// publishState publishes the provided state payload via MQTT using the
// configured MQTT client and topic prefix.
// While the reserve window is active it carries the last reserve evaluation,
// so paths that do not evaluate it (pause, cooldown, errors, commands) do not
// clear the reserve sensors.
func (r *Runner) publishState(payload mqtt.StatePayload) {
	if r.opt != nil {
		r.opt.mergeLast(&payload)
	}
	if payload.ReserveWindowActive == nil || !*payload.ReserveWindowActive {
		r.reserve = nil
	} else if rt := r.reserve; rt != nil && payload.ReserveAtRisk == nil {
		target, deficit, atRisk := rt.targetWh, rt.deficitWh, rt.atRisk
		payload.ReserveTargetWh = &target
		payload.ReserveDeficitWh = &deficit
		payload.ReserveAtRisk = &atRisk
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
// configured charge window and returns the effective charge parameters and
// the active window (nil on the legacy path or outside every window).
// When windows are configured it delegates to power.ResolveActiveWindow;
// otherwise it falls back to the legacy checkTimeRangeAt call using
// RunnerConfig.StartHR / EndHR.
func (r *Runner) resolveActiveWindow(now time.Time) (inWindow bool, maxCharge float64, forecastHorizon, consumptionHorizon, windowName string, active *pw.Window) {
	maxCharge = r.cfg.MaxCharge
	forecastHorizon = r.cfg.ForecastHorizon
	consumptionHorizon = r.cfg.ConsumptionHorizon

	if len(r.cfg.Windows) > 0 {
		active = pw.ResolveActiveWindow(r.cfg.Windows, now, r.cfg.WeekdayFeature)
		if active == nil {
			return false, maxCharge, forecastHorizon, consumptionHorizon, "", nil
		}
		maxCharge = active.MaxCharge
		if active.ForecastHorizon != "" {
			forecastHorizon = active.ForecastHorizon
		}
		if active.ConsumptionHorizon != "" {
			consumptionHorizon = active.ConsumptionHorizon
		}
		name := pw.WindowNameOrDefault(*active, 0)
		return true, maxCharge, forecastHorizon, consumptionHorizon, name, active
	}

	// Legacy path: use single StartHR/EndHR window.
	inWindow, err := checkTimeRangeAt(now, r.cfg.StartHR, r.cfg.EndHR)
	if err != nil {
		u.HandleError(err, "checkTimeRangeAt failed in resolveActiveWindow")
		return false, maxCharge, forecastHorizon, consumptionHorizon, "", nil
	}
	if inWindow {
		return true, maxCharge, forecastHorizon, consumptionHorizon, "legacy", nil
	}
	return false, maxCharge, forecastHorizon, consumptionHorizon, "", nil
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
			endAt = endAt.AddDate(0, 0, 1)
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

// nextMatchingWeekday advances t one calendar day at a time (same wall-clock
// time, so DST changes do not shift it) until its weekday is in
// the provided set. The set must be non-empty; the function loops at most 7
// times before returning (since a non-empty set guarantees a match within 7
// days).
func nextMatchingWeekday(t time.Time, weekdays map[time.Weekday]bool) time.Time {
	for i := 0; i < 7; i++ {
		if weekdays[t.Weekday()] {
			return t
		}
		t = t.AddDate(0, 0, 1)
	}
	return t
}
