package cmd

import (
	"context"
	"errors"
	"fmt"
	"sbam/pkg/fronius"
	"sbam/pkg/mqtt"
	u "sbam/src/utils"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const runnerIntentQueueSize = 16

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
	LatestState        *latestStateCache
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

type latestStateCache struct {
	mu      sync.RWMutex
	payload *mqtt.StatePayload
}

func newLatestStateCache() *latestStateCache {
	return &latestStateCache{}
}

func cloneFloat64Ptr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	copyV := *v
	return &copyV
}

func cloneInt16Ptr(v *int16) *int16 {
	if v == nil {
		return nil
	}
	copyV := *v
	return &copyV
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	copyV := *v
	return &copyV
}

func cloneTimePtr(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	copyV := v.UTC()
	return &copyV
}

func cloneStatePayload(payload mqtt.StatePayload) mqtt.StatePayload {
	cloned := payload
	cloned.BatterySOCPct = cloneFloat64Ptr(payload.BatterySOCPct)
	cloned.BatteryCapacityWh = cloneFloat64Ptr(payload.BatteryCapacityWh)
	cloned.ForecastTodayWh = cloneFloat64Ptr(payload.ForecastTodayWh)
	cloned.PwNetWh = cloneFloat64Ptr(payload.PwNetWh)
	cloned.ChargePct = cloneInt16Ptr(payload.ChargePct)
	cloned.ChargeWindowActive = cloneBoolPtr(payload.ChargeWindowActive)
	cloned.ReserveWindowActive = cloneBoolPtr(payload.ReserveWindowActive)
	cloned.NextRun = cloneTimePtr(payload.NextRun)
	return cloned
}

func (c *latestStateCache) Store(payload mqtt.StatePayload) {
	if c == nil {
		return
	}
	cloned := cloneStatePayload(payload)

	c.mu.Lock()
	c.payload = &cloned
	c.mu.Unlock()
}

func (c *latestStateCache) Load() (mqtt.StatePayload, bool) {
	if c == nil {
		return mqtt.StatePayload{}, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.payload == nil {
		return mqtt.StatePayload{}, false
	}

	return cloneStatePayload(*c.payload), true
}

// Runner owns serialized schedule and command execution.
// Single-writer invariant: all Fronius Modbus write operations must be executed by this runner.
type Runner struct {
	cfg     RunnerConfig
	client  mqtt.Client
	writer  batteryWriter
	intents chan mqtt.Intent
	paused  atomic.Pointer[time.Time]
}

func NewRunner(cfg RunnerConfig, client mqtt.Client) *Runner {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.LatestState == nil {
		cfg.LatestState = newLatestStateCache()
	}

	return &Runner{
		cfg:     cfg,
		client:  client,
		writer:  newBatteryWriter(),
		intents: make(chan mqtt.Intent, runnerIntentQueueSize),
	}
}

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

	paused, pauseUntil := r.pauseStateAt(now)
	if !inChargeWindow {
		u.Log.Info("The current time is outside the range defined by start_hr and end_hr.: " + r.cfg.StartHR + " <= t <= " + r.cfg.EndHR)
		capMax := capacityMax

		payload := makeBasePayload(fronius.DecisionIdle.String(), "current time outside configured charging window", inChargeWindow, reserveWindowActive)
		payload.BatterySOCPct = &socPct
		payload.BatteryCapacityWh = &capMax
		payload.Paused = paused
		payload.NextRun = pauseUntil
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

	if paused {
		payload := makeBasePayload("paused", "schedule execution skipped because runner is paused", inChargeWindow, reserveWindowActive)
		payload.BatterySOCPct = &socPct
		payload.BatteryCapacityWh = &capacityMax
		payload.ForecastTodayWh = &solarPowerProduction
		payload.Paused = true
		payload.NextRun = pauseUntil
		r.publishState(payload)
		return nil
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

func (r *Runner) handleForceCharge(ctx context.Context, intent mqtt.Intent) error {
	paused, _ := r.pauseStateAt(r.now())
	if paused {
		err := fmt.Errorf("%w: %s", errRunnerPaused, intent.Kind)
		r.publishError(ctx, "force_charge", err)
		return err
	}
	if intent.TargetPct < 1 || intent.TargetPct > 100 {
		err := fmt.Errorf("%w: target_pct must be between 1 and 100", mqtt.ErrInvalidPayload)
		r.publishError(ctx, "force_charge", err)
		return err
	}

	if err := r.writer.ForceCharge(r.cfg.FroniusIP, intent.TargetPct); err != nil {
		r.publishError(ctx, "force_charge", err)
		return err
	}

	payload := r.newCommandPayload(string(mqtt.IntentForceCharge), "force charge command executed", r.now())
	payload.Paused = false
	payload.ChargePct = &intent.TargetPct
	r.publishState(payload)
	return nil
}

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

func (r *Runner) publishState(payload mqtt.StatePayload) {
	if r.cfg.LatestState != nil {
		r.cfg.LatestState.Store(payload)
	}
	publishStateSnapshot(r.client, r.cfg.MQTT, payload)
}

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
		Timestamp: r.now(),
	})
}

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

func (r *Runner) now() time.Time {
	return r.cfg.Now().UTC()
}

func (r *Runner) setPause(pauseUntil *time.Time) {
	if pauseUntil == nil {
		indefinite := time.Time{}
		r.paused.Store(&indefinite)
		return
	}

	until := pauseUntil.UTC()
	r.paused.Store(&until)
}

func (r *Runner) clearPause() {
	r.paused.Store(nil)
}

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
		r.clearPause()
		return false, nil
	}

	copyUntil := until
	return true, &copyUntil
}

func checkTimeRangeAt(now time.Time, startHR, endHR string) (bool, error) {
	layout := "15:04"
	startTime, err := time.Parse(layout, startHR)
	if err != nil {
		return false, fmt.Errorf("invalid start time %q: %w", startHR, err)
	}

	endTime, err := time.Parse(layout, endHR)
	if err != nil {
		return false, fmt.Errorf("invalid end time %q: %w", endHR, err)
	}

	startAt := time.Date(now.Year(), now.Month(), now.Day(), startTime.Hour(), startTime.Minute(), 0, 0, now.Location())
	endAt := time.Date(now.Year(), now.Month(), now.Day(), endTime.Hour(), endTime.Minute(), 0, 0, now.Location())
	inRange := (now.After(startAt) || now.Equal(startAt)) && (now.Before(endAt) || now.Equal(endAt))
	return inRange, nil
}
