package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sbam/pkg/fronius"
	"sbam/pkg/homeassistant"
	"sbam/pkg/mqtt"
	"sbam/pkg/optimize"
	u "sbam/src/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// Runner tests for scheduler_mode: optimize (test plan T-0140 … T-0155,
// security TC-001b, TC-002a–c, TC-006b, TC-008c, TC-011a/b, UX T-0115).

const schOptIP = "192.0.2.10"

// --- fakes -----------------------------------------------------------------

// schOptEvents is an ordered log shared by the fakes, used to assert the
// order of writes, publishes and history requests.
type schOptEvents struct {
	mu   sync.Mutex
	list []string
}

func (e *schOptEvents) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, s)
}

func (e *schOptEvents) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.list...)
}

func (e *schOptEvents) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = nil
}

func schOptIndex(list []string, prefix string) int {
	for i, s := range list {
		if strings.HasPrefix(s, prefix) {
			return i
		}
	}
	return -1
}

type schOptForce struct {
	pct     int16
	revertS int
}

// schOptWriter is a recording dispatchWriter.
type schOptWriter struct {
	ev *schOptEvents

	mu            sync.Mutex
	applies       []fronius.DispatchCommand
	defaults      int
	forces        []schOptForce
	applyErr      error
	applyRestored bool
	defaultsErr   error
	defaultsDelay time.Duration
}

func (w *schOptWriter) Apply(ip string, cmd fronius.DispatchCommand) (fronius.Applied, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ev.add("apply:" + string(cmd.Mode))
	w.applies = append(w.applies, cmd)
	if w.applyErr != nil {
		return fronius.Applied{DefaultsRestored: w.applyRestored}, w.applyErr
	}
	return fronius.Applied{Mode: cmd.Mode, Limits: fronius.InverterLimits{WChaMaxW: 5000, RateSF: -2}}, nil
}

func (w *schOptWriter) SetDefaults(ip string) error {
	w.mu.Lock()
	delay := w.defaultsDelay
	w.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ev.add("defaults")
	w.defaults++
	return w.defaultsErr
}

func (w *schOptWriter) ForceChargeWithRevert(ip string, pct int16, revertS int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ev.add("force")
	w.forces = append(w.forces, schOptForce{pct: pct, revertS: revertS})
	return nil
}

func (w *schOptWriter) counts() (applies, defaults, forces int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.applies), w.defaults, len(w.forces)
}

func (w *schOptWriter) lastApply() fronius.DispatchCommand {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.applies[len(w.applies)-1]
}

// schOptClient is a recording mqtt.Client that also accepts SetCurrency.
type schOptClient struct {
	ev *schOptEvents

	mu         sync.Mutex
	msgs       []publishedMessage
	currencies []string
}

func (c *schOptClient) Connect(context.Context) error    { return nil }
func (c *schOptClient) Disconnect(context.Context) error { return nil }
func (c *schOptClient) Publish(_ context.Context, topic string, _ byte, _ bool, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ev.add("publish:" + topic)
	c.msgs = append(c.msgs, publishedMessage{topic: topic, payload: append([]byte(nil), payload...)})
	return nil
}
func (c *schOptClient) Subscribe(context.Context, string, byte, mqtt.MessageHandler) error {
	return nil
}
func (c *schOptClient) IsConnected() bool { return true }
func (c *schOptClient) OnConnect(func())  {}
func (c *schOptClient) SetCurrency(currency string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.currencies = append(c.currencies, currency)
}

func (c *schOptClient) bySuffix(suffix string) []publishedMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []publishedMessage
	for _, m := range c.msgs {
		if strings.HasSuffix(m.topic, suffix) {
			out = append(out, m)
		}
	}
	return out
}

func (c *schOptClient) lastState(t *testing.T) mqtt.StatePayload {
	t.Helper()
	msgs := c.bySuffix("/state")
	require.NotEmpty(t, msgs, "expected a state publish")
	return decodeStatePayload(t, msgs[len(msgs)-1].payload)
}

func (c *schOptClient) lastPlan(t *testing.T) mqtt.PlanPayload {
	t.Helper()
	msgs := c.bySuffix("/plan")
	require.NotEmpty(t, msgs, "expected a plan publish")
	var p mqtt.PlanPayload
	require.NoError(t, json.Unmarshal(msgs[len(msgs)-1].payload, &p))
	return p
}

func (c *schOptClient) acks(t *testing.T) []mqtt.AckPayload {
	t.Helper()
	var out []mqtt.AckPayload
	for _, m := range c.bySuffix("/ack") {
		out = append(out, decodeAckPayload(t, m.payload))
	}
	return out
}

type schOptBattery struct {
	mu  sync.Mutex
	soc float64
	err error
}

func (b *schOptBattery) Read(context.Context, string) (float64, float64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.soc, 10000, b.err
}

func (b *schOptBattery) set(soc float64, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.soc, b.err = soc, err
}

type schOptClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *schOptClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *schOptClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

type schOptTimer struct {
	delay time.Duration
	fn    func()
	timer *time.Timer
}

// stopped reports whether the runner stopped this timer (a real far-future
// timer: Stop returns false only when it was already stopped).
func (t *schOptTimer) stopped() bool { return !t.timer.Stop() }

type schOptTimers struct {
	mu   sync.Mutex
	list []*schOptTimer
}

func (ts *schOptTimers) withDelay(d time.Duration) []*schOptTimer {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	var out []*schOptTimer
	for _, t := range ts.list {
		if t.delay == d {
			out = append(out, t)
		}
	}
	return out
}

func (ts *schOptTimers) all() []*schOptTimer {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]*schOptTimer(nil), ts.list...)
}

// --- harness -----------------------------------------------------------------

type schOptSlot struct {
	action  optimize.Action
	rateW   float64
	startWh float64
}

type schOptHarness struct {
	t        *testing.T
	loc      *time.Location
	clock    *schOptClock
	timers   *schOptTimers
	events   *schOptEvents
	client   *schOptClient
	writer   *schOptWriter
	battery  *schOptBattery
	runner   *Runner
	logs     *observer.ObservedLogs
	factory  atomic.Int32
	runNows  []time.Time
	result   func(now time.Time) optimizeResult
	duringFn func() // runs inside optRun (simulates a slow run)
}

func newSchOptHarness(t *testing.T, dryRun bool) *schOptHarness {
	t.Helper()
	loc := optRunLoc(t)
	h := &schOptHarness{
		t:       t,
		loc:     loc,
		clock:   &schOptClock{t: time.Date(2026, 10, 5, 10, 20, 0, 0, loc)},
		timers:  &schOptTimers{},
		events:  &schOptEvents{},
		battery: &schOptBattery{soc: 60},
	}
	h.client = &schOptClient{ev: h.events}
	h.writer = &schOptWriter{ev: h.events}
	h.result = func(now time.Time) optimizeResult {
		return schOptResult(optSlot0(now, loc), schOptSlot{action: optimize.ActionSelfConsume})
	}

	oldTimer, oldWriter, oldRun, oldLog := newTimerFunc, newDispatchWriter, optRun, u.Log
	newTimerFunc = func(d time.Duration, f func()) *time.Timer {
		tm := &schOptTimer{delay: d, fn: f, timer: time.NewTimer(1000 * time.Hour)}
		h.timers.mu.Lock()
		h.timers.list = append(h.timers.list, tm)
		h.timers.mu.Unlock()
		return tm.timer
	}
	newDispatchWriter = func() dispatchWriter {
		h.factory.Add(1)
		return h.writer
	}
	optRun = func(_ context.Context, _ optimizeDeps, _ OptimizeConfig, now time.Time, _ *profileCache) optimizeResult {
		h.runNows = append(h.runNows, now)
		h.events.add("run")
		if h.duringFn != nil {
			h.duringFn()
		}
		return h.result(now)
	}
	core, logs := observer.New(zap.DebugLevel)
	u.Log = zap.New(core).Sugar()
	h.logs = logs
	t.Cleanup(func() {
		newTimerFunc, newDispatchWriter, optRun, u.Log = oldTimer, oldWriter, oldRun, oldLog
		for _, tm := range h.timers.all() {
			tm.timer.Stop()
		}
	})

	h.runner = NewRunner(RunnerConfig{
		FroniusIP:     schOptIP,
		MaxCharge:     10000,
		SchedulerMode: schedulerModeOptimize,
		MQTT:          mqtt.Config{Enabled: true, TopicPrefix: "sbam", Optimizer: true},
		Now:           h.clock.now,
		Optimize: OptimizeConfig{
			DryRun:        dryRun,
			FroniusIP:     schOptIP,
			MaxChargeW:    4500,
			MaxDischargeW: 4500,
			MinSocPct:     10,
			Efficiency:    0.83,
			Location:      loc,
		},
		OptimizeDeps: optimizeDeps{Battery: h.battery},
	}, h.client)
	return h
}

// schOptResult builds a validated-looking result with one slot per entry,
// starting at start.
func schOptResult(start time.Time, slots ...schOptSlot) optimizeResult {
	plan := optimize.Plan{ExpectedCost: 12.5, TerminalValue: 1.25, StartSocPct: 60, Reason: optimize.ReasonSelfConsumeOptimal}
	for i, s := range slots {
		startWh := s.startWh
		if startWh == 0 {
			startWh = 5000
		}
		plan.Hours = append(plan.Hours, optimize.PlanHour{
			Start:     start.Add(time.Duration(i) * time.Hour),
			End:       start.Add(time.Duration(i+1) * time.Hour),
			Fraction:  1,
			Action:    s.action,
			RateW:     s.rateW,
			StartWh:   startWh,
			EndWh:     startWh,
			SocEndPct: 60,
			Buy:       1.5,
			Sell:      0.9,
		})
	}
	if len(slots) > 0 {
		switch slots[0].action {
		case optimize.ActionDischarge:
			plan.Reason = optimize.ReasonExportHighPrice
		case optimize.ActionCharge:
			plan.Reason = optimize.ReasonChargeAheadOfPeak
		}
	}
	return optimizeResult{
		Plan:          plan,
		Battery:       optimize.Battery{CapacityWh: 10000, SocPct: 60, MinSocPct: 10, E0Wh: 5000, EmaxWh: 9000},
		Reasons:       []optimize.Reason{plan.Reason},
		Currency:      "DKK",
		StartSocPct:   60,
		HasSoc:        true,
		TerminalValue: plan.TerminalValue,
	}
}

func schOptFallback(reason optimize.Reason) optimizeResult {
	return optimizeResult{
		Fallback:    &optimizeFallback{Reason: reason, Input: optInputPrice, Err: errors.New("no price for the current hour")},
		Reasons:     []optimize.Reason{reason},
		Currency:    "DKK",
		StartSocPct: 60,
		HasSoc:      true,
	}
}

func (h *schOptHarness) setPlan(slots ...schOptSlot) {
	h.result = func(now time.Time) optimizeResult {
		return schOptResult(optSlot0(now, h.loc), slots...)
	}
}

func (h *schOptHarness) at(hour, minute, second int) time.Time {
	return time.Date(2026, 10, 5, hour, minute, second, 0, h.loc)
}

func (h *schOptHarness) handle(intent mqtt.Intent) {
	h.runner.handleIntent(context.Background(), intent)
}

func (h *schOptHarness) tick() { h.handle(mqtt.Intent{Kind: mqtt.IntentTick}) }

func (h *schOptHarness) command(kind mqtt.IntentKind, mod func(*mqtt.Intent)) {
	intent := mqtt.Intent{Kind: kind, CommandTopic: "sbam/cmd/" + string(kind)}
	if mod != nil {
		mod(&intent)
	}
	h.handle(intent)
}

// process handles every queued intent (from timer callbacks or Submit).
func (h *schOptHarness) process() {
	for {
		select {
		case intent := <-h.runner.intents:
			h.handle(intent)
		default:
			return
		}
	}
}

func (h *schOptHarness) hourTimers() []*schOptTimer {
	var out []*schOptTimer
	for _, tm := range h.timers.all() {
		if tm.delay != optWatchdogInterval {
			out = append(out, tm)
		}
	}
	return out
}

func (h *schOptHarness) logged(msg string) bool {
	return h.logs.FilterMessage(msg).Len() > 0
}

func (h *schOptHarness) shutdown() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(h.t, h.runner.Run(ctx), context.Canceled)
}

// --- T-0140: startup tick, hourly wall-clock timer, early-fire snap (AC-13) --

func TestOptimizeRunner_T0140_StartupTickAndHourlyTimer(t *testing.T) {
	h := newSchOptHarness(t, true)

	h.runner.StartOptimizeTicker()
	require.Len(t, h.runner.intents, 1, "startup submits exactly one immediate tick")
	h.process()

	require.Len(t, h.runNows, 1)
	assert.True(t, h.runNows[0].Equal(h.at(10, 20, 0)))
	timers := h.timers.all()
	require.Len(t, timers, 1, "one hourly timer armed")
	assert.Equal(t, 40*time.Minute, timers[0].delay, "next run at exactly 11:00:00")

	// Fire 1 s early: the run snaps to 11:00 and the next timer is for 12:00.
	h.clock.set(h.at(10, 59, 59))
	timers[0].fn()
	h.process()
	require.Len(t, h.runNows, 2)
	assert.True(t, h.runNows[1].Equal(h.at(11, 0, 0)), "early fire snaps to the boundary")
	timers = h.timers.all()
	require.Len(t, timers, 2)
	assert.Equal(t, time.Hour+time.Second, timers[1].delay)
	assert.True(t, timers[0].stopped(), "each tick replaces the pending timer")

	// 3 s early is not snapped (edge of the 2 s window).
	h.clock.set(h.at(11, 59, 57))
	h.tick()
	require.Len(t, h.runNows, 3)
	assert.True(t, h.runNows[2].Equal(h.at(11, 59, 57)))
	timers = h.timers.all()
	require.Len(t, timers, 3)
	assert.Equal(t, 3*time.Second, timers[2].delay)
	assert.True(t, timers[1].stopped())
	assert.False(t, timers[2].stopped(), "exactly one timer stays pending")
}

func TestOptimizeRunner_T0140_TickOnHourBoundaryArmsFullHour(t *testing.T) {
	h := newSchOptHarness(t, true)
	h.clock.set(h.at(13, 0, 0))
	h.tick()
	require.Len(t, h.timers.all(), 1)
	assert.Equal(t, time.Hour, h.timers.all()[0].delay)
}

// --- T-0141: live dispatch with the clamped rate (AC-14, S-006a) -----------

func TestOptimizeRunner_T0141_LiveDischargeDispatch(t *testing.T) {
	h := newSchOptHarness(t, false)
	// SoC margin: 0.95·(1100 − 1 %·10000) = 950 W for a full slot.
	h.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 3000, startWh: 1100})

	h.tick()

	applies, defaults, _ := h.writer.counts()
	require.Equal(t, 1, applies)
	assert.Equal(t, 0, defaults)
	cmd := h.writer.lastApply()
	assert.Equal(t, fronius.DispatchDischarge, cmd.Mode)
	// The plan's 950 W is battery-side; the Gen24 discharge setpoint is
	// AC-side, so the inverter is told 950 x sqrt(0.83) (SPEC-0001 AC-11e).
	etaD := math.Sqrt(0.83)
	assert.InDelta(t, 950*etaD, cmd.RateW, 1e-9, "battery-side rate clamped by the SoC margin, written AC-side")
	assert.Equal(t, fronius.RevertSeconds, cmd.RevertS)
	assert.InDelta(t, 4500*etaD, cmd.MaxRateW, 1e-9, "the battery-side limit is converted the same way")

	state := h.client.lastState(t)
	require.NotNil(t, state.OptAction)
	assert.Equal(t, "discharge", *state.OptAction)
	assert.Equal(t, "discharge", state.LastDecision)
	assert.Equal(t, string(optimize.ReasonExportHighPrice), state.LastDecisionReason)
	assert.InDelta(t, 950, *state.OptRateW, 1e-9, "the published rate stays battery-side, like the plan")
	assert.False(t, *state.OptDryRun)
	require.NotNil(t, state.OptExpectedCost)
	assert.Equal(t, []string{string(optimize.ReasonExportHighPrice)}, state.OptReasons)
	require.NotNil(t, state.ChargeWindowActive)
	assert.False(t, *state.ChargeWindowActive)

	plan := h.client.lastPlan(t)
	assert.Len(t, plan.Hours, 1)
	assert.Equal(t, "DKK", plan.Currency)
	assert.True(t, h.logged("dispatch applied"))
	assert.Equal(t, 1, h.logs.FilterMessage("optimizer run").Len(), "exactly one optimizer run line")
}

func TestOptimizeRunner_T0141_ChargeDispatchPublishesChargePct(t *testing.T) {
	h := newSchOptHarness(t, false)
	h.setPlan(schOptSlot{action: optimize.ActionCharge, rateW: 2500})

	h.tick()

	cmd := h.writer.lastApply()
	assert.Equal(t, fronius.DispatchCharge, cmd.Mode)
	assert.Equal(t, 2500.0, cmd.RateW)
	state := h.client.lastState(t)
	require.NotNil(t, state.ChargePct)
	assert.Equal(t, int16(50), *state.ChargePct, "2500 W of WChaMax 5000 W")
}

// --- T-0142: dry-run never constructs the writer (AC-15) -------------------

func TestOptimizeRunner_T0142_DryRunNeverWrites(t *testing.T) {
	h := newSchOptHarness(t, true)

	h.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 3000})
	h.tick()
	state := h.client.lastState(t)
	assert.True(t, *state.OptDryRun)
	assert.Equal(t, "discharge", *state.OptAction)
	assert.True(t, h.logged("dry run: no Modbus registers written"))
	entry := h.logs.FilterMessage("dry run: no Modbus registers written").All()[0]
	regs, ok := entry.ContextMap()["registers"]
	require.True(t, ok)
	assert.Contains(t, fmt.Sprint(regs), fmt.Sprintf("%d=1", fronius.StorCtl_Mod), "would-write list ends with StorCtl_Mod=1")
	assert.Empty(t, h.timers.withDelay(optWatchdogInterval), "no watchdog in dry-run")

	// Fallback path.
	h.result = func(time.Time) optimizeResult { return schOptFallback(optimize.ReasonFallbackMissingPrices) }
	h.tick()
	fb := h.logs.FilterMessage("optimizer fallback (dry run): inverter defaults would be restored").All()
	require.Len(t, fb, 1, "a dry-run fallback is logged at Warn with its detail")
	assert.Equal(t, string(optimize.ReasonFallbackMissingPrices), fmt.Sprint(fb[0].ContextMap()["reason"]))
	// Pause, watchdog and shutdown paths.
	h.command(mqtt.IntentPause, nil)
	h.handle(mqtt.Intent{Kind: mqtt.IntentWatchdog})
	h.tick()
	h.shutdown()

	assert.Equal(t, int32(0), h.factory.Load(), "writer factory must never be called in dry-run")
	assert.True(t, h.logs.FilterMessageSnippet("dry run").Len() >= 2)
}

// --- T-0143: fallback restores defaults and keeps the hourly timer (AC-16) -

func TestOptimizeRunner_T0143_FallbackRestoresDefaults(t *testing.T) {
	h := newSchOptHarness(t, false)
	h.result = func(time.Time) optimizeResult { return schOptFallback(optimize.ReasonFallbackMissingPrices) }

	h.tick()

	applies, defaults, _ := h.writer.counts()
	assert.Equal(t, 0, applies)
	assert.Equal(t, 1, defaults)
	state := h.client.lastState(t)
	assert.Equal(t, optDecisionFallback, state.LastDecision)
	assert.Equal(t, string(optimize.ReasonFallbackMissingPrices), state.LastDecisionReason)
	assert.Equal(t, "self_consume", *state.OptAction)
	assert.Nil(t, state.OptExpectedCost)
	assert.Equal(t, 0, *state.OptHorizonH)

	stateMsgs := h.client.bySuffix("/state")
	assert.Contains(t, string(stateMsgs[len(stateMsgs)-1].payload), `"opt_expected_cost":null`)
	plan := h.client.lastPlan(t)
	assert.Empty(t, plan.Hours)
	assert.Equal(t, string(optimize.ReasonFallbackMissingPrices), plan.Reason)

	require.Len(t, h.timers.all(), 1, "next run still armed")
	assert.Equal(t, 40*time.Minute, h.timers.all()[0].delay)
	assert.True(t, h.logged("optimizer fallback: inverter defaults restored"))
}

func TestOptimizeRunner_T0143_WriteFailures(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		restored     bool
		wantReason   optimize.Reason
		wantDefaults int
	}{
		{
			name:         "write failure, ApplyDispatch restored defaults",
			err:          fmt.Errorf("%w: register 40357", fronius.ErrModbusWrite),
			restored:     true,
			wantReason:   optimize.ReasonFallbackModbusWriteFailed,
			wantDefaults: 0,
		},
		{
			name:         "read-back mismatch",
			err:          fmt.Errorf("%w: %w", fronius.ErrModbusWrite, fronius.ErrReadBackMismatch),
			wantReason:   optimize.ReasonFallbackModbusWriteFailed,
			wantDefaults: 0,
		},
		{
			name:         "implausible inverter limits: runner restores defaults",
			err:          fmt.Errorf("%w: WChaMax 0 W", fronius.ErrInverterLimits),
			wantReason:   optimize.ReasonFallbackInverterLimits,
			wantDefaults: 1,
		},
		{
			name:         "connect error: runner restores defaults",
			err:          errors.New("dial tcp: connection refused"),
			wantReason:   optimize.ReasonFallbackModbusWriteFailed,
			wantDefaults: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newSchOptHarness(t, false)
			h.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 2000})
			h.writer.applyErr = tc.err
			h.writer.applyRestored = tc.restored

			h.tick()

			_, defaults, _ := h.writer.counts()
			assert.Equal(t, tc.wantDefaults, defaults)
			state := h.client.lastState(t)
			assert.Equal(t, optDecisionFallback, state.LastDecision)
			assert.Equal(t, string(tc.wantReason), state.LastDecisionReason)
			assert.NotContains(t, state.OptReasons, string(optimize.ReasonExportHighPrice), "action code dropped on fallback")
			assert.Equal(t, "self_consume", *state.OptAction)
			assert.NotEmpty(t, h.client.bySuffix("/error"), "write failures go to <prefix>/error")
			assert.Empty(t, h.timers.withDelay(optWatchdogInterval), "no watchdog after a failed discharge")
		})
	}
}

// --- T-0144 / TC-002a: pause writes defaults before publishing ------------

func TestOptimizeRunner_T0144_PauseWritesDefaultsFirst(t *testing.T) {
	h := newSchOptHarness(t, false)
	h.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 2000})
	h.tick()
	require.Len(t, h.timers.withDelay(optWatchdogInterval), 1)
	h.events.reset()

	h.command(mqtt.IntentPause, nil)

	ev := h.events.snapshot()
	iDefaults, iState := schOptIndex(ev, "defaults"), schOptIndex(ev, "publish:sbam/state")
	require.GreaterOrEqual(t, iDefaults, 0, "defaults written")
	require.GreaterOrEqual(t, iState, 0, "paused state published")
	assert.Less(t, iDefaults, iState, "defaults before the paused state")
	state := h.client.lastState(t)
	assert.True(t, state.Paused)
	assert.Equal(t, "paused", state.LastDecision)
	assert.Equal(t, string(optimize.ReasonPaused), state.LastDecisionReason)
	assert.Equal(t, "self_consume", *state.OptAction)
	acks := h.client.acks(t)
	require.NotEmpty(t, acks)
	assert.True(t, acks[len(acks)-1].Accepted)
	assert.True(t, h.timers.withDelay(optWatchdogInterval)[0].stopped(), "watchdog cancelled by pause")

	// Hourly runs while paused publish but do not write.
	applies, defaults, _ := h.writer.counts()
	h.clock.set(h.at(11, 0, 0))
	h.tick()
	a2, d2, _ := h.writer.counts()
	assert.Equal(t, applies, a2)
	assert.Equal(t, defaults, d2)
	state = h.client.lastState(t)
	assert.Equal(t, "paused", state.LastDecision)
	assert.Equal(t, string(optimize.ReasonPaused), state.LastDecisionReason)
	assert.Contains(t, state.OptReasons, string(optimize.ReasonExportHighPrice))
	assert.NotEmpty(t, h.client.lastPlan(t).Hours, "plan still published while paused")
}

func TestOptimizeRunner_T0144_PauseDefaultsFailureStillPauses(t *testing.T) {
	h := newSchOptHarness(t, false)
	h.writer.defaultsErr = errors.New("modbus down")

	h.command(mqtt.IntentPause, nil)

	paused, _ := h.runner.pauseStateAt(h.clock.now())
	assert.True(t, paused)
	acks := h.client.acks(t)
	require.Len(t, acks, 1)
	assert.False(t, acks[0].Accepted)
	assert.Contains(t, acks[0].Error, "modbus down")
}

// --- T-0145 / TC-002b: set_defaults is accepted while paused ---------------

func TestOptimizeRunner_T0145_SetDefaultsWhilePaused(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry_run=%v", dryRun), func(t *testing.T) {
			h := newSchOptHarness(t, dryRun)
			h.command(mqtt.IntentPause, nil)
			_, before, _ := h.writer.counts()

			h.command(mqtt.IntentSetDefaults, nil)

			_, after, _ := h.writer.counts()
			assert.Equal(t, before+1, after, "explicit set_defaults writes even while paused and in dry-run")
			acks := h.client.acks(t)
			require.Len(t, acks, 2)
			assert.Equal(t, "set_defaults", acks[1].Command)
			assert.True(t, acks[1].Accepted)
			assert.True(t, h.client.lastState(t).Paused, "set_defaults does not resume")
		})
	}
}

// --- T-0146 / TC-002c: pause expiry and resume run immediately -------------

func TestOptimizeRunner_T0146_PauseExpiryAndResumeTick(t *testing.T) {
	h := newSchOptHarness(t, false)
	until := h.at(10, 30, 0)
	h.command(mqtt.IntentPause, func(i *mqtt.Intent) { i.PauseUntil = &until })

	pauseTimers := h.timers.withDelay(10 * time.Minute)
	require.Len(t, pauseTimers, 1, "pause timer armed at until")
	require.Empty(t, h.runNows)

	h.clock.set(until)
	pauseTimers[0].fn()
	h.process()
	require.Len(t, h.runNows, 1, "pause expiry runs the optimizer without waiting for xx:00")
	state := h.client.lastState(t)
	assert.False(t, state.Paused)
	applies, _, _ := h.writer.counts()
	assert.Equal(t, 1, applies, "the expiry run writes again")

	// resume after an indefinite pause also ticks immediately.
	h.command(mqtt.IntentPause, nil)
	h.command(mqtt.IntentResume, nil)
	require.Len(t, h.runNows, 2)
	assert.False(t, h.client.lastState(t).Paused)
}

// --- T-0147 / TC-011a: force_charge override (ADR-0005) --------------------

func schOptForceChargeStorage(t *testing.T) {
	t.Helper()
	old := newStorage
	newStorage = func() storageClient { return &fakeStorageClient{capacityMax: 10000, socPct: 40} }
	t.Cleanup(func() { newStorage = old })
}

func TestOptimizeRunner_T0147_ForceChargeOverride(t *testing.T) {
	schOptForceChargeStorage(t)
	h := newSchOptHarness(t, false)
	h.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 2000})
	h.clock.set(h.at(10, 10, 0))

	h.command(mqtt.IntentForceCharge, func(i *mqtt.Intent) { i.TargetPct = 80; i.DurationS = 1800 })

	require.Len(t, h.writer.forces, 1)
	assert.Equal(t, int16(80), h.writer.forces[0].pct)
	assert.Equal(t, 2100, h.writer.forces[0].revertS, "40359 = duration_s + 300")
	acks := h.client.acks(t)
	require.Len(t, acks, 1)
	assert.True(t, acks[0].Accepted)
	assert.Equal(t, string(optimize.ReasonManualOverride), h.client.lastState(t).LastDecisionReason)

	// 10:20 run during the override: publish only.
	h.clock.set(h.at(10, 20, 0))
	h.tick()
	applies, _, _ := h.writer.counts()
	assert.Equal(t, 0, applies)
	state := h.client.lastState(t)
	assert.Equal(t, string(optimize.ReasonManualOverride), state.LastDecisionReason)
	assert.Equal(t, "discharge", state.LastDecision)
	assert.True(t, h.logged("manual override active: plan published, no write"))

	// 10:40 the override has expired: the run writes.
	h.clock.set(h.at(10, 40, 0))
	h.tick()
	applies, _, _ = h.writer.counts()
	assert.Equal(t, 1, applies)
	assert.True(t, h.logged("manual override ended"))
}

func TestOptimizeRunner_T0147_ForceChargeOverrideAcrossHours(t *testing.T) {
	schOptForceChargeStorage(t)
	h := newSchOptHarness(t, false)
	h.setPlan(schOptSlot{action: optimize.ActionHold})
	h.clock.set(h.at(10, 10, 0))
	h.command(mqtt.IntentForceCharge, func(i *mqtt.Intent) { i.TargetPct = 80; i.DurationS = 7200 })
	assert.Equal(t, 7500, h.writer.forces[0].revertS)

	for _, hour := range []int{11, 12} {
		h.clock.set(h.at(hour, 0, 0))
		h.tick()
		applies, _, _ := h.writer.counts()
		assert.Equal(t, 0, applies, "%02d:00 run must not write", hour)
		assert.Equal(t, string(optimize.ReasonManualOverride), h.client.lastState(t).LastDecisionReason)
	}
	h.clock.set(h.at(13, 0, 0))
	h.tick()
	applies, _, _ := h.writer.counts()
	assert.Equal(t, 1, applies, "13:00 run writes normally")
}

func TestOptimizeRunner_T0147_ForceChargeWithoutDurationEndsAtHour(t *testing.T) {
	schOptForceChargeStorage(t)
	h := newSchOptHarness(t, false)
	h.clock.set(h.at(10, 10, 0))
	h.command(mqtt.IntentForceCharge, func(i *mqtt.Intent) { i.TargetPct = 100; i.IgnoreMaxCharge = true })
	assert.Equal(t, 50*60+300, h.writer.forces[0].revertS, "seconds to end of hour + 300")
	assert.True(t, h.runner.opt.overrideUntil.Equal(h.at(11, 0, 0)))

	// set_defaults ends the override.
	h.command(mqtt.IntentSetDefaults, nil)
	assert.True(t, h.runner.opt.overrideUntil.IsZero())
}

func TestOptimizeRunner_T0147_ForceChargeRejectedWhilePaused(t *testing.T) {
	h := newSchOptHarness(t, false)
	h.command(mqtt.IntentPause, nil)
	h.command(mqtt.IntentForceCharge, func(i *mqtt.Intent) { i.TargetPct = 100; i.IgnoreMaxCharge = true })
	_, _, forces := h.writer.counts()
	assert.Equal(t, 0, forces)
	acks := h.client.acks(t)
	require.Len(t, acks, 2)
	assert.False(t, acks[1].Accepted)
}

// --- T-0148 / TC-011b: trigger_now rate limit ------------------------------

func TestOptimizeRunner_T0148_TriggerNowRateLimited(t *testing.T) {
	h := newSchOptHarness(t, true)
	for i := 0; i < 5; i++ {
		h.clock.set(h.at(10, 20, 2*i))
		h.command(mqtt.IntentTriggerNow, nil)
	}
	assert.Len(t, h.runNows, 1, "only one run within 60 s")
	acks := h.client.acks(t)
	require.Len(t, acks, 5)
	assert.True(t, acks[0].Accepted)
	for _, a := range acks[1:] {
		assert.False(t, a.Accepted)
		assert.Equal(t, "rate limited", a.Error)
	}

	h.clock.set(h.at(10, 21, 0))
	h.command(mqtt.IntentTriggerNow, nil)
	assert.Len(t, h.runNows, 2, "accepted again after 60 s")
}

// --- T-0149 / TC-001b: shutdown restores defaults (S-001) ------------------

// Review I-01: in dry-run the optimizer never writes, but an explicit user
// force_charge does. Pause and shutdown must then restore defaults; without
// such a command, dry-run stays write-free (T-0142).
func TestOptimizeRunner_I01_DryRunUserForceChargeRestoredOnStop(t *testing.T) {
	t.Run("shutdown", func(t *testing.T) {
		schOptForceChargeStorage(t)
		h := newSchOptHarness(t, true)
		h.tick()
		h.command(mqtt.IntentForceCharge, func(i *mqtt.Intent) { i.TargetPct = 80; i.DurationS = 1800 })
		h.shutdown()
		_, defaults, _ := h.writer.counts()
		require.Len(t, h.writer.forces, 1, "user force_charge writes even in dry-run")
		assert.Equal(t, 1, defaults, "defaults restored after a user force_charge in dry-run")
	})
	t.Run("pause", func(t *testing.T) {
		schOptForceChargeStorage(t)
		h := newSchOptHarness(t, true)
		h.tick()
		h.command(mqtt.IntentForceCharge, func(i *mqtt.Intent) { i.TargetPct = 80; i.DurationS = 1800 })
		h.command(mqtt.IntentPause, nil)
		_, defaults, _ := h.writer.counts()
		assert.Equal(t, 1, defaults, "pause restores defaults after a user force_charge in dry-run")
		h.shutdown()
		_, defaults, _ = h.writer.counts()
		assert.Equal(t, 1, defaults, "no second write once defaults are restored")
	})
}

// T-0156 (AC-18b): the run at a slot's end logs planned vs. actual SoC for
// the slot the previous run applied; a run in the middle of a slot does not.
// The planned change starts at the plan's own start SoC (harness: min SoC
// 10 %, 10 kWh), which equals the measured SoC above the minimum.
func TestOptimizeRunner_T0156_PlanCheck(t *testing.T) {
	h := newSchOptHarness(t, false)
	withSoc := func(startSoc, plannedEnd float64) func(time.Time) optimizeResult {
		return func(now time.Time) optimizeResult {
			res := schOptResult(optSlot0(now, h.loc), schOptSlot{action: optimize.ActionCharge, rateW: 1000,
				startWh: (startSoc - 10) / 100 * 10000})
			res.StartSocPct = startSoc
			res.Plan.Hours[0].SocEndPct = plannedEnd
			res.Plan.Hours[0].PVWh = 1234.4
			res.Plan.Hours[0].ConsumptionWh = 567.6
			return res
		}
	}

	h.clock.set(h.at(10, 0, 0))
	h.result = withSoc(40, 50)
	h.tick()
	assert.Equal(t, 0, h.logs.FilterMessage("plan check").Len(), "nothing to compare on the first run")

	h.clock.set(h.at(11, 0, 0))
	h.result = withSoc(48, 55)
	h.tick()
	checks := h.logs.FilterMessage("plan check").All()
	require.Len(t, checks, 1)
	f := checks[0].ContextMap()
	assert.Equal(t, "charge", fmt.Sprint(f["action"]))
	assert.Equal(t, 40.0, f["planned_soc_start_pct"])
	assert.Equal(t, 40.0, f["actual_soc_start_pct"])
	assert.Equal(t, 50.0, f["planned_soc_end_pct"])
	assert.Equal(t, 48.0, f["actual_soc_pct"])
	assert.Equal(t, 1000.0, f["planned_delta_wh"], "10 % of 10 kWh")
	assert.Equal(t, 800.0, f["actual_delta_wh"])
	assert.Equal(t, -200.0, f["deviation_wh"])
	assert.Equal(t, 1234.0, f["planned_pv_wh"], "the slot's PV forecast")
	assert.Equal(t, 568.0, f["planned_consumption_wh"], "the slot's consumption profile")
	assert.Equal(t, false, f["dry_run"])

	// A trigger_now run at 11:30 is mid-slot: no plan check.
	h.clock.set(h.at(11, 30, 0))
	h.result = withSoc(52, 55)
	h.command(mqtt.IntentTriggerNow, nil)
	assert.Equal(t, 1, h.logs.FilterMessage("plan check").Len())
}

// T-0156b (AC-18b amendment): below opt_min_soc_pct the plan starts at the
// minimum, so the planned change is measured from there and a battery that
// simply stays at the inverter floor shows no phantom deviation (live trial
// 2026-10-07 02:00-05:00).
func TestOptimizeRunner_T0156b_PlanCheckBelowMinSoc(t *testing.T) {
	h := newSchOptHarness(t, false)
	floor := func(measured float64) func(time.Time) optimizeResult {
		return func(now time.Time) optimizeResult {
			// Plan starts at the 10 % minimum (StartWh 0) and stays there.
			res := schOptResult(optSlot0(now, h.loc), schOptSlot{action: optimize.ActionSelfConsume})
			res.Plan.Hours[0].StartWh = 0
			res.Plan.Hours[0].SocEndPct = 10
			res.StartSocPct = measured
			return res
		}
	}
	h.clock.set(h.at(2, 0, 0))
	h.result = floor(7)
	h.tick()
	h.clock.set(h.at(3, 0, 0))
	h.result = floor(6.9)
	h.tick()

	checks := h.logs.FilterMessage("plan check").All()
	require.Len(t, checks, 1)
	f := checks[0].ContextMap()
	assert.Equal(t, 10.0, f["planned_soc_start_pct"])
	assert.Equal(t, 7.0, f["actual_soc_start_pct"])
	assert.Equal(t, 0.0, f["planned_delta_wh"])
	assert.Equal(t, -10.0, f["actual_delta_wh"])
	assert.Equal(t, -10.0, f["deviation_wh"], "only the real 0.1 % drop, not the 3 % gap to the soft minimum")
}

func TestOptimizeRunner_T0149_ShutdownDefaults(t *testing.T) {
	t.Run("last action discharge", func(t *testing.T) {
		h := newSchOptHarness(t, false)
		h.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 2000})
		h.tick()
		start := time.Now()
		h.shutdown()
		_, defaults, _ := h.writer.counts()
		assert.Equal(t, 1, defaults)
		assert.Less(t, time.Since(start), 5*time.Second)
		for _, tm := range h.timers.all() {
			assert.True(t, tm.stopped(), "timers stopped on shutdown")
		}
	})
	t.Run("last action self_consume", func(t *testing.T) {
		h := newSchOptHarness(t, false)
		h.tick()
		h.shutdown()
		_, defaults, _ := h.writer.counts()
		assert.Equal(t, 0, defaults)
	})
	t.Run("shutdown intent", func(t *testing.T) {
		h := newSchOptHarness(t, false)
		h.setPlan(schOptSlot{action: optimize.ActionStandby})
		h.tick()
		require.True(t, h.runner.Submit(mqtt.Intent{Kind: mqtt.IntentShutdown}))
		require.NoError(t, h.runner.Run(context.Background()))
		_, defaults, _ := h.writer.counts()
		assert.Equal(t, 1, defaults)
	})
	t.Run("write exceeds the deadline", func(t *testing.T) {
		old := optShutdownTimeout
		optShutdownTimeout = 20 * time.Millisecond
		t.Cleanup(func() { optShutdownTimeout = old })
		h := newSchOptHarness(t, false)
		h.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 2000})
		h.tick()
		h.writer.mu.Lock()
		h.writer.defaultsDelay = 200 * time.Millisecond
		h.writer.mu.Unlock()
		start := time.Now()
		h.shutdown()
		assert.Less(t, time.Since(start), 150*time.Millisecond, "Run returns at the deadline")
		assert.True(t, h.logs.FilterMessage("defaults restore failed; inverter state unknown").Len() > 0)
		time.Sleep(250 * time.Millisecond) // let the background write finish before cleanup
	})
}

// --- T-0150 / TC-006b: discharge SoC watchdog (S-006c) ---------------------

func TestOptimizeRunner_T0150_Watchdog(t *testing.T) {
	cases := []struct {
		name string
		soc  float64
		err  error
	}{
		{name: "SoC at min + 0.5", soc: 10.5},
		{name: "SoC read fails", err: errors.New("solar api timeout")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newSchOptHarness(t, false)
			h.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 2000})
			h.tick()
			wd := h.timers.withDelay(optWatchdogInterval)
			require.Len(t, wd, 1, "watchdog armed during discharge")

			h.battery.set(tc.soc, tc.err)
			wd[0].fn()
			h.process()

			_, defaults, _ := h.writer.counts()
			assert.Equal(t, 1, defaults)
			state := h.client.lastState(t)
			assert.Equal(t, string(optimize.ReasonWatchdogMinSoc), state.LastDecisionReason)
			assert.Equal(t, optDecisionFallback, state.LastDecision)
			assert.Equal(t, "self_consume", *state.OptAction)
			assert.Empty(t, h.client.lastPlan(t).Hours)
			assert.Len(t, h.timers.withDelay(optWatchdogInterval), 1, "no further watchdog until the next hourly run")
		})
	}

	t.Run("SoC healthy re-arms", func(t *testing.T) {
		h := newSchOptHarness(t, false)
		h.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 2000})
		h.tick()
		h.battery.set(50, nil)
		h.timers.withDelay(optWatchdogInterval)[0].fn()
		h.process()
		_, defaults, _ := h.writer.counts()
		assert.Equal(t, 0, defaults)
		assert.Len(t, h.timers.withDelay(optWatchdogInterval), 2, "re-armed")
	})

	t.Run("no discharge, no watchdog", func(t *testing.T) {
		h := newSchOptHarness(t, false)
		h.setPlan(schOptSlot{action: optimize.ActionCharge, rateW: 2000})
		h.tick()
		assert.Empty(t, h.timers.withDelay(optWatchdogInterval))
		// A stray watchdog intent never writes.
		h.handle(mqtt.Intent{Kind: mqtt.IntentWatchdog})
		_, defaults, _ := h.writer.counts()
		assert.Equal(t, 0, defaults)
	})
}

// --- T-0151 / TC-008c: write-time slot selection (S-008) -------------------

func TestOptimizeRunner_T0151_WriteTimeSlotSelection(t *testing.T) {
	t.Run("run overran into the next hour", func(t *testing.T) {
		h := newSchOptHarness(t, false)
		h.clock.set(h.at(10, 59, 50))
		h.setPlan(schOptSlot{action: optimize.ActionHold}, schOptSlot{action: optimize.ActionDischarge, rateW: 2000})
		h.duringFn = func() { h.clock.set(h.at(11, 0, 5)) }

		h.tick()

		require.Len(t, h.writer.applies, 1)
		assert.Equal(t, fronius.DispatchDischarge, h.writer.lastApply().Mode, "the 11:00 slot is applied")
		assert.True(t, h.logged("optimizer run overran its hour; applying a later plan slot"))
		assert.Len(t, h.client.lastPlan(t).Hours, 1, "published plan starts at the applied slot")
	})
	t.Run("no slot contains the write time", func(t *testing.T) {
		h := newSchOptHarness(t, false)
		h.clock.set(h.at(10, 59, 50))
		h.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 2000})
		h.duringFn = func() { h.clock.set(h.at(11, 0, 5)) }

		h.tick()

		applies, defaults, _ := h.writer.counts()
		assert.Equal(t, 0, applies)
		assert.Equal(t, 1, defaults)
		state := h.client.lastState(t)
		assert.Equal(t, string(optimize.ReasonFallbackInvalidPlan), state.LastDecisionReason)
	})
	t.Run("early fire at 10:59:59 applies the 11:00 slot", func(t *testing.T) {
		h := newSchOptHarness(t, false)
		h.clock.set(h.at(10, 59, 59))
		h.setPlan(schOptSlot{action: optimize.ActionStandby})
		h.tick()
		require.Len(t, h.runNows, 1)
		assert.True(t, h.runNows[0].Equal(h.at(11, 0, 0)))
		assert.Equal(t, fronius.DispatchStandby, h.writer.lastApply().Mode)
	})
}

// --- T-0152: set_reserve is rejected (ADR-0005) ----------------------------

func TestOptimizeRunner_T0152_SetReserveRejected(t *testing.T) {
	h := newSchOptHarness(t, false)
	h.command(mqtt.IntentSetReserve, func(i *mqtt.Intent) { i.PwBattReserve = 500 })
	acks := h.client.acks(t)
	require.Len(t, acks, 1)
	assert.False(t, acks[0].Accepted)
	assert.Equal(t, "not supported in scheduler_mode=optimize", acks[0].Error)
	assert.Equal(t, int32(0), h.factory.Load())
}

// --- T-0155: the daily profile rebuild runs after the write ----------------

type schOptEventHA struct {
	inner haClient
	ev    *schOptEvents
}

func (f schOptEventHA) State(ctx context.Context, entity string) (homeassistant.State, error) {
	return f.inner.State(ctx, entity)
}

func (f schOptEventHA) History(ctx context.Context, entity string, start, end time.Time) ([]homeassistant.HistoryPoint, error) {
	f.ev.add("history:" + entity)
	return f.inner.History(ctx, entity, start, end)
}

func TestOptimizeRunner_T0155_RebuildAfterWrite(t *testing.T) {
	env := newOptRunEnv(t)
	h := newSchOptHarness(t, false)
	optRun = runOptimization // the real pipeline with fake inputs
	h.clock.set(env.now)
	o := h.runner.opt
	o.cfg = env.cfg
	o.cfg.DryRun = false
	o.deps = optimizeDeps{HA: schOptEventHA{inner: env.ha, ev: h.events}, Forecasts: env.fc, Battery: env.bat}
	// A profile built yesterday: due for its daily rebuild.
	yesterday := env.now.Add(-20 * time.Hour)
	o.cache = profileCache{date: optLocalDate(yesterday, env.loc), profile: optStaticProfile(env.cfg), builtAt: yesterday, lastAttempt: yesterday}

	h.tick()

	ev := h.events.snapshot()
	iApply, iHistory := schOptIndex(ev, "apply:"), schOptIndex(ev, "history:")
	require.GreaterOrEqual(t, iApply, 0, "dispatch written: %v", ev)
	require.GreaterOrEqual(t, iHistory, 0, "profile rebuilt: %v", ev)
	assert.Less(t, iApply, iHistory, "write before any history request")
	assert.Less(t, schOptIndex(ev, "publish:sbam/plan"), iHistory, "publish before any history request")
	assert.Equal(t, optLocalDate(env.now, env.loc), o.cache.date)
}

// --- T-0115: discovery republished once when the currency becomes known ----

func TestOptimizeRunner_T0115_CurrencyRepublishesDiscovery(t *testing.T) {
	t.Chdir(t.TempDir()) // discovery persists its device identity file in cwd
	h := newSchOptHarness(t, true)
	h.runner.cfg.MQTT.HADiscovery = true
	currency := "DKK"
	h.result = func(now time.Time) optimizeResult {
		res := schOptResult(optSlot0(now, h.loc), schOptSlot{action: optimize.ActionHold})
		res.Currency = currency
		return res
	}
	costConfig := "/opt_expected_cost/config"

	h.tick()
	msgs := h.client.bySuffix(costConfig)
	require.Len(t, msgs, 1, "republished once when the currency became known")
	assert.Contains(t, string(msgs[0].payload), `"unit_of_measurement":"DKK"`)
	assert.Equal(t, []string{"DKK"}, h.client.currencies, "client keeps the currency for reconnects")
	assert.Equal(t, "DKK", h.client.lastPlan(t).Currency)

	h.tick()
	assert.Len(t, h.client.bySuffix(costConfig), 1, "unchanged currency: no republish")

	currency = "EUR"
	h.tick()
	msgs = h.client.bySuffix(costConfig)
	require.Len(t, msgs, 2)
	assert.Contains(t, string(msgs[1].payload), `"unit_of_measurement":"EUR"`)

	currency = "" // a fallback without a price read keeps the known currency
	h.tick()
	assert.Len(t, h.client.bySuffix(costConfig), 2)
}

// --- state merge (design §MQTT) --------------------------------------------

func TestOptimizeRunner_CommandStateKeepsOptimizerFields(t *testing.T) {
	h := newSchOptHarness(t, false)
	h.setPlan(schOptSlot{action: optimize.ActionHold})
	h.tick()

	h.command(mqtt.IntentSetDefaults, nil)

	state := h.client.lastState(t)
	assert.Equal(t, "set_defaults", state.LastDecision)
	require.NotNil(t, state.OptAction, "opt_* fields merged into command state")
	assert.Equal(t, "self_consume", *state.OptAction)
	require.NotNil(t, state.OptExpectedCost)
	assert.Equal(t, 12.5, *state.OptExpectedCost)
	assert.NotEmpty(t, state.OptReasons)
}

func TestOptimizeRunner_OtherModesUnaffected(t *testing.T) {
	runner := NewRunner(RunnerConfig{SchedulerMode: "windows"}, newFakeClient())
	assert.Nil(t, runner.opt)
	runner.optimizeShutdown() // no-op outside optimize mode
}

// T-0157 (AC-14 amendment): when the dispatch SoC margin leaves no discharge,
// standby is written (not hold, which would let PV charge the battery) and the
// reason follows the applied action (live trial 2026-10-06 10:00).
func TestOptimizeRunner_T0157_ClampedDischargeIsStandby(t *testing.T) {
	h := newSchOptHarness(t, false)
	// startWh 100 Wh above the 10 % minimum is below the 1 % margin.
	h.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 3000, startWh: 100})
	h.tick()

	require.Equal(t, fronius.DispatchMode(optimize.ActionStandby), h.writer.lastApply().Mode)
	st := h.client.lastState(t)
	assert.Equal(t, string(optimize.ActionStandby), *st.OptAction)
	assert.Equal(t, string(optimize.ReasonDischargeClampedMinSoc), st.LastDecisionReason)
	assert.NotContains(t, st.OptReasons, string(optimize.ReasonExportHighPrice))
	plan := h.client.lastPlan(t)
	assert.Equal(t, string(optimize.ActionDischarge), plan.Hours[0].Action, "the published plan is unchanged")
	assert.Equal(t, string(optimize.ReasonDischargeClampedMinSoc), plan.Reason)

	// Edge: a partial clamp keeps discharge and the plan's reason.
	h2 := newSchOptHarness(t, false)
	h2.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 3000, startWh: 1100})
	h2.tick()
	assert.Equal(t, fronius.DispatchMode(optimize.ActionDischarge), h2.writer.lastApply().Mode)
	assert.Equal(t, string(optimize.ReasonExportHighPrice), h2.client.lastState(t).LastDecisionReason)
}

// T-0222 (AC-11e): the Gen24 discharge setpoint is AC-side, so a planned
// battery-side discharge is written as rate x sqrt(eta). The live trial
// showed SoC falling by 1.10 x the setpoint (= 1/sqrt(0.83)). Charge
// setpoints stay battery-side (ratio 0.98-1.01 in the same trial).
func TestOptimizeRunner_T0222_DischargeSetpointIsACSide(t *testing.T) {
	etaD := math.Sqrt(0.83)

	h := newSchOptHarness(t, false)
	h.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 3000, startWh: 8000})
	h.tick()
	cmd := h.writer.lastApply()
	assert.InDelta(t, 3000*etaD, cmd.RateW, 1e-9, "setpoint = planned battery drain x sqrt(eta)")
	applied := h.logs.FilterMessage("dispatch applied").All()
	require.Len(t, applied, 1)
	assert.Equal(t, 3000.0, applied[0].ContextMap()["rate_w"], "the log keeps the battery-side rate")
	assert.Equal(t, math.Round(3000*etaD), applied[0].ContextMap()["setpoint_w"])
	assert.InDelta(t, 3000, *h.client.lastState(t).OptRateW, 1e-9, "MQTT keeps the battery-side rate")

	// Edge: at the configured limit the setpoint is the limit x sqrt(eta),
	// so the battery delivers opt_max_discharge_rate_w and not ~10 % more.
	h2 := newSchOptHarness(t, false)
	h2.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 4500, startWh: 8000})
	h2.tick()
	assert.InDelta(t, 4500*etaD, h2.writer.lastApply().RateW, 1e-9)
	assert.InDelta(t, 4500*etaD, h2.writer.lastApply().MaxRateW, 1e-9)

	// Charge is unchanged: battery-side setpoint.
	h3 := newSchOptHarness(t, false)
	h3.setPlan(schOptSlot{action: optimize.ActionCharge, rateW: 2000})
	h3.tick()
	assert.Equal(t, 2000.0, h3.writer.lastApply().RateW)
	assert.Equal(t, 4500.0, h3.writer.lastApply().MaxRateW)

	// Dry run previews the same setpoint.
	h4 := newSchOptHarness(t, true)
	h4.setPlan(schOptSlot{action: optimize.ActionDischarge, rateW: 3000, startWh: 8000})
	h4.tick()
	preview := h4.logs.FilterMessage("dry run: no Modbus registers written").All()
	require.NotEmpty(t, preview)
	assert.Equal(t, math.Round(3000*etaD), preview[len(preview)-1].ContextMap()["setpoint_w"])
}
