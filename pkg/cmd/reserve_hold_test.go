package cmd

import (
	"testing"
	"time"

	"sbam/pkg/fronius"
	pw "sbam/pkg/power"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var cest = time.FixedZone("CEST", 2*3600)

func localAt(clock string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04", "2026-10-20 "+clock, cest)
	return t
}

// morningForecast: PV reaches 500 W in the 09:00-09:30 period; the
// 08:00-09:00 gap at 500 W is (500-100)*0.5 + (500-300)*0.5 = 300 Wh.
func morningForecast() []pw.Forecasts {
	var f pw.Forecasts
	for clock, kw := range map[string]float64{"08:30": 0.1, "09:00": 0.3, "09:30": 0.6, "10:00": 1.2} {
		f.Forecasts = append(f.Forecasts, pw.Forecast{PVEstimate: kw, PeriodEnd: localAt(clock).UTC().Format(time.RFC3339)})
	}
	return []pw.Forecasts{f}
}

func boolPtr(b bool) *bool { return &b }

func reserveRunner(window pw.Window, static float64) *Runner {
	return NewRunner(RunnerConfig{
		FroniusIP:          "127.0.0.1",
		PWConsumption:      12000, // base load 500 W
		PWBattReserve:      static,
		BattMinSoC:         25,
		StartHR:            "00:00",
		EndHR:              "08:00",
		BattReserveStartHR: "00:00",
		BattReserveEndHR:   "08:00",
		Windows:            []pw.Window{window},
		WeekdayFeature:     true,
		SchedulerMode:      "crontab",
	}, newFakeClient())
}

var nightWindow = pw.Window{Name: "night", Start: "00:00", End: "08:00", MaxCharge: 2500, ReserveHorizon: pw.ReserveHorizonUntilSolar}

func TestUntilSolarReserve(t *testing.T) {
	now := localAt("03:00")

	r := reserveRunner(nightWindow, 1000)
	assert.InDelta(t, 1280+300, r.untilSolarReserve(now, nightWindow, 5120, morningForecast()), 0.01,
		"min SoC 25 %% of 5120 Wh plus the 300 Wh morning gap")

	r = reserveRunner(nightWindow, 2700)
	assert.InDelta(t, 2700, r.untilSolarReserve(now, nightWindow, 5120, morningForecast()), 0.01,
		"never below pw_batt_reserve")

	r = reserveRunner(nightWindow, 1000)
	assert.InDelta(t, 1000, r.untilSolarReserve(now, nightWindow, 5120, nil), 0.01,
		"no forecast falls back to pw_batt_reserve")

	r = reserveRunner(nightWindow, 1000)
	r.cfg.BattMinSoC = 100
	assert.InDelta(t, 5120, r.untilSolarReserve(now, nightWindow, 5120, morningForecast()), 0.01,
		"capped at the battery capacity")

	r = reserveRunner(nightWindow, 1000)
	r.cfg.PWBaseLoad = 100 // already covered by the 08:00-08:30 period
	assert.InDelta(t, 1280, r.untilSolarReserve(now, nightWindow, 5120, morningForecast()), 0.01,
		"explicit base load overrides pw_consumption/24")

	bad := nightWindow
	bad.End = "8h"
	r = reserveRunner(bad, 1000)
	assert.InDelta(t, 1000, r.untilSolarReserve(now, bad, 5120, morningForecast()), 0.01,
		"unparseable window end falls back to pw_batt_reserve")
}

func TestNextClockAt(t *testing.T) {
	got, err := nextClockAt(localAt("03:00"), "08:00")
	require.NoError(t, err)
	assert.Equal(t, localAt("08:00"), got)

	got, err = nextClockAt(localAt("23:00"), "08:00")
	require.NoError(t, err)
	assert.Equal(t, localAt("08:00").Add(24*time.Hour), got, "cross-midnight window ends tomorrow")

	_, err = nextClockAt(localAt("03:00"), "bogus")
	assert.Error(t, err)
}

func TestCheckReserveOptions(t *testing.T) {
	assert.NoError(t, checkReserveOptions(5, 0))
	assert.NoError(t, checkReserveOptions(0, 450))
	assert.ErrorContains(t, checkReserveOptions(101, 0), "batt_min_soc")
	assert.ErrorContains(t, checkReserveOptions(-1, 0), "batt_min_soc")
	assert.ErrorContains(t, checkReserveOptions(5, -1), "pw_base_load")
}

// tickWith runs one tick at now with fake storage, power and fronius clients
// and returns the fronius fake to inspect what the runner passed.
func tickWith(t *testing.T, r *Runner, now time.Time, sites []pw.Forecasts) *trackingFroniusClient {
	t.Helper()
	oldStorage, oldPower, oldFronius := newStorage, newPower, newFronius
	t.Cleanup(func() { newStorage, newPower, newFronius = oldStorage, oldPower, oldFronius })

	newStorage = func() storageClient {
		return &fakeStorageClient{capacityToCharge: 3000, capacityMax: 5120, socPct: 41}
	}
	newPower = func() powerClient {
		return &fakePowerClient{forecastWh: 12000, retrieved: true, sites: sites}
	}
	tracker := &trackingFroniusClient{decision: fronius.DecisionIdle, reason: "test"}
	newFronius = func(fronius.SunSpecType) froniusClient { return tracker }

	require.NoError(t, r.Tick(t.Context(), now))
	require.Equal(t, 1, tracker.calls)
	return tracker
}

func TestTickPassesUntilSolarReserveAndHold(t *testing.T) {
	w := nightWindow
	w.HoldBattery = boolPtr(true)
	r := reserveRunner(w, 1000)

	tracker := tickWith(t, r, localAt("03:00"), morningForecast())

	assert.InDelta(t, 1580, tracker.lastReserveWh, 0.01)
	assert.True(t, tracker.lastHold)
}

func TestTickWithoutNewOptionsKeepsStaticReserve(t *testing.T) {
	w := pw.Window{Name: "night", Start: "00:00", End: "08:00", MaxCharge: 2500}
	r := reserveRunner(w, 1000)

	tracker := tickWith(t, r, localAt("03:00"), morningForecast())

	assert.InDelta(t, 1000, tracker.lastReserveWh, 0.01)
	assert.False(t, tracker.lastHold)
}

func TestTickHoldFalseWhenDisabled(t *testing.T) {
	w := nightWindow
	w.HoldBattery = boolPtr(false)
	r := reserveRunner(w, 1000)

	tracker := tickWith(t, r, localAt("03:00"), nil)

	assert.False(t, tracker.lastHold)
	assert.InDelta(t, 1000, tracker.lastReserveWh, 0.01, "no forecast periods: static reserve")
}

func TestScheduleReserveFlags(t *testing.T) {
	for name, def := range map[string]string{"batt_min_soc": "5", "pw_base_load": "0"} {
		flag := scdCmd.Flags().Lookup(name)
		if assert.NotNil(t, flag, name) {
			assert.Equal(t, def, flag.DefValue, name)
		}
	}
}
