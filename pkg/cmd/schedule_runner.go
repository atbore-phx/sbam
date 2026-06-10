package cmd

import (
	"context"
	"errors"
	"fmt"
	"sbam/pkg/fronius"
	"sbam/pkg/mqtt"
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
	MQTT               mqtt.Config
	Now                func() time.Time
}

type batteryWriter interface {
	ForceCharge(froniusIP string, targetPct int16) error
	SetDefaults(froniusIP string) error
}

type froniusBatteryWriter struct{}

func (froniusBatteryWriter) ForceCharge(froniusIP string, targetPct int16) error {
	return fronius.ForceCharge(froniusIP, targetPct)
}

func (froniusBatteryWriter) SetDefaults(froniusIP string) error {
	return fronius.Setdefaults(froniusIP)
}

var newBatteryWriter = func() batteryWriter {
	return froniusBatteryWriter{}
}

// latest state caching was removed; the runner publishes state directly.

// Runner owns serialized schedule and command execution.
// Single-writer invariant: all Fronius Modbus write operations must be executed by this runner.
type Runner struct {
	cfg     RunnerConfig
	client  mqtt.Client
	writer  batteryWriter
	intents chan mqtt.Intent
	paused  atomic.Pointer[time.Time]
}

// NewRunner constructs a Runner with the provided configuration and MQTT
// client. If `cfg.Now` is nil it defaults to `time.Now`. The runner is
// initialized with a buffered intent queue and a battery writer.
func NewRunner(cfg RunnerConfig, client mqtt.Client) *Runner {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &Runner{
		cfg:     cfg,
		client:  client,
		writer:  newBatteryWriter(),
		intents: make(chan mqtt.Intent, runnerIntentQueueSize),
	}
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
			return ctx.Err()
		case intent := <-r.intents:
			r.handleIntent(ctx, intent)
			if intent.Kind == mqtt.IntentShutdown {
				return nil
			}
		}
	}
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

	inChargeWindow, err := checkTimeRangeAt(now, r.cfg.StartHR, r.cfg.EndHR)
	if err != nil {
		r.publishError(ctx, "tick", err)
		return err
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

		payload := makeBasePayload(fronius.DecisionSkip.String(), fmt.Sprintf("storage read failed: %v", storageErr), inChargeWindow, reserveWindowActive)
		paused, pauseUntil := r.pauseStateAt(now)
		payload.Paused = paused
		payload.NextRun = pauseUntil
		r.publishState(payload)
		r.publishError(ctx, "storage", storageErr)
		return storageErr
	}

	if paused, pauseUntil := r.pauseStateAt(now); paused {
		payload := makeBasePayload("paused", "Forecast Charging execution skipped because sbam is paused", inChargeWindow, reserveWindowActive)
		payload.BatterySOCPct = &socPct
		payload.BatteryCapacityWh = &capacityMax
		payload.Paused = true
		payload.NextRun = pauseUntil
		r.publishState(payload)
		return nil
	}

	if !inChargeWindow {
		u.Log.Info("The current time is outside the range defined by start_hr and end_hr.: " + r.cfg.StartHR + " <= t <= " + r.cfg.EndHR)
		capMax := capacityMax

		payload := makeBasePayload(fronius.DecisionIdle.String(), "current time outside configured charging window", inChargeWindow, reserveWindowActive)
		payload.BatterySOCPct = &socPct
		payload.BatteryCapacityWh = &capMax
		r.publishState(payload)
		return nil
	}

	inCooldown, cooldownErr := isInCooldown(now, r.cfg.StartHR, r.cfg.EndHR, chargeCooldownMinutes)
	if cooldownErr != nil {
		r.publishError(ctx, "tick", cooldownErr)
		return cooldownErr
	}
	if inCooldown {
		u.Log.Info("cooldown active — charge decisions suppressed near window end")
		capMax := capacityMax
		payload := makeBasePayload("cooldown", "charge decisions suppressed near window end", inChargeWindow, reserveWindowActive)
		payload.BatterySOCPct = &socPct
		payload.BatteryCapacityWh = &capMax
		r.publishState(payload)
		return nil
	}

	powerHandler := newPower()
	solarPowerProduction, forecastRetrieved, forecastErr := powerHandler.Handler(r.cfg.APIKey, r.cfg.URL, r.cfg.CacheForecast, r.cfg.CacheFilePrefix, r.cfg.CacheTime)
	if forecastErr != nil {
		u.HandleError(forecastErr, "power forecast retrieval failed; disabling forecast for this run")
		r.publishError(ctx, "power", forecastErr)
		solarPowerProduction = 0.0
		forecastRetrieved = false
	}

	u.Log.Infof("your Daily consumption is:%d Wh", int(r.cfg.PWConsumption))

	froniusHandler := newFronius()
	chargePct, decision, reason, powerState, froniusErr := froniusHandler.Handler(
		solarPowerProduction,
		capacityToCharge,
		capacityMax,
		r.cfg.PWConsumption,
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

		payload := makeBasePayload(fronius.DecisionSkip.String(), fmt.Sprintf("fronius handler failed: %v", froniusErr), inChargeWindow, reserveWindowActive)
		payload.BatterySOCPct = &socPct
		payload.BatteryCapacityWh = &capacityMax
		payload.Paused = false
		r.publishState(payload)
		r.publishError(ctx, "fronius", froniusErr)
		return froniusErr
	}

	payload := makeBasePayload(decision.String(), reason, inChargeWindow, reserveWindowActive)
	payload.BatterySOCPct = &socPct
	payload.BatteryCapacityWh = &capacityMax
	payload.ForecastTodayWh = &solarPowerProduction
	payload.PwNetWh = &powerState.Net
	payload.ChargePct = &chargePct
	payload.Paused = false
	r.publishState(payload)
	return nil
}

// handleIntent processes a single Intent from the queue and dispatches to
// the appropriate handler. It also acknowledges MQTT commands where
// required.
func (r *Runner) handleIntent(ctx context.Context, intent mqtt.Intent) {
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
	if intent.TargetPct < 0 || intent.TargetPct > 100 {
		err := fmt.Errorf("%w: target_pct must be between 0 and 100", mqtt.ErrInvalidPayload)
		r.publishError(ctx, "force_charge", err)
		return err
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
			return err
		}
		targetPct = 100
	case intent.TargetPct == 0:
		targetPct = 0
	default:
		targetPct, err = r.resolveForceChargeTargetPct(intent.TargetPct)
		if err != nil {
			r.publishError(ctx, "force_charge", err)
			return err
		}
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
	inChargeWindow, inChargeErr := checkTimeRangeAt(now, r.cfg.StartHR, r.cfg.EndHR)
	if inChargeErr != nil {
		u.HandleError(inChargeErr, "unable to compute charge window status")
		inChargeWindow = false
	}

	reserveWindowActive, reserveErr := checkTimeRangeAt(now, r.cfg.BattReserveStartHR, r.cfg.BattReserveEndHR)
	if reserveErr != nil {
		u.HandleError(reserveErr, "unable to compute reserve window status")
		reserveWindowActive = false
	}

	return makeBasePayload(lastDecision, reason, inChargeWindow, reserveWindowActive)
}

// publishState publishes the provided state payload via MQTT using the
// configured MQTT client and topic prefix.
func (r *Runner) publishState(payload mqtt.StatePayload) {
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
