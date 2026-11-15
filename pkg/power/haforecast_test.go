package power

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	_ "time/tzdata" // Europe/Copenhagen without system tzdata

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const (
	entToday    = "sensor.solcast_pv_forecast_forecast_today"
	entTomorrow = "sensor.solcast_pv_forecast_forecast_tomorrow"
	entPolled   = "sensor.solcast_pv_forecast_api_last_polled"
)

var testEntities = HAForecastEntities{Today: entToday, Tomorrow: entTomorrow, LastPolled: entPolled}

// fakeReader is a fake EntityReader: entity → state or error, with a call
// counter. Unknown entities fail like an HA 404.
type fakeReader struct {
	mu       sync.Mutex
	states   map[string]EntityState
	errs     map[string]error
	calls    map[string]int
	onBefore func(id string)
}

func newFakeReader() *fakeReader {
	return &fakeReader{states: map[string]EntityState{}, errs: map[string]error{}, calls: map[string]int{}}
}

func (f *fakeReader) set(id string, st EntityState) *fakeReader {
	f.states[id] = st
	return f
}

func (f *fakeReader) fail(id string, err error) *fakeReader {
	f.errs[id] = err
	return f
}

func (f *fakeReader) count(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[id]
}

func (f *fakeReader) ReadEntity(ctx context.Context, id string) (EntityState, error) {
	f.mu.Lock()
	f.calls[id]++
	before := f.onBefore
	f.mu.Unlock()
	if before != nil {
		before(id)
	}
	if err := ctx.Err(); err != nil {
		return EntityState{}, err
	}
	if err, ok := f.errs[id]; ok {
		return EntityState{}, err
	}
	st, ok := f.states[id]
	if !ok {
		return EntityState{}, fmt.Errorf("homeassistant: GET /api/states/%s: status 404: %w", id, ErrEntityNotFound)
	}
	return st, nil
}

func copenhagen(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Copenhagen")
	require.NoError(t, err)
	return loc
}

// loadStateFixture decodes a testdata HA state with encoding/json into
// map[string]any, as production does.
func loadStateFixture(t *testing.T, name string) EntityState {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	st := EntityState{State: m["state"].(string)}
	if attrs, ok := m["attributes"].(map[string]any); ok {
		st.Attributes = attrs
	}
	return st
}

// synthPV is the synthetic PV profile used by generated days.
func synthPV(t time.Time) float64 {
	h := t.Hour()
	if h < 8 || h > 16 {
		return 0
	}
	return 1.0 + 0.1*float64(h-8)
}

// dayElements builds detailedForecast elements for a local day, starting at
// fromHour (wall clock).
func dayElements(loc *time.Location, y int, m time.Month, d int, fromHour int) []any {
	start := time.Date(y, m, d, 0, 0, 0, 0, loc)
	next := time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	var out []any
	for s := start; s.Before(next); s = s.Add(30 * time.Minute) {
		if s.Hour() < fromHour {
			continue
		}
		out = append(out, map[string]any{"period_start": s.Format(time.RFC3339), "pv_estimate": synthPV(s), "pv_estimate10": 0.0})
	}
	return out
}

func element(start string, kw any) map[string]any {
	return map[string]any{"period_start": start, "pv_estimate": kw}
}

func stateOf(list any) EntityState {
	return EntityState{State: "1.0", Attributes: map[string]any{HAForecastAttribute: list, "estimate": 1.0}}
}

func detailed(st EntityState) []any {
	return st.Attributes[HAForecastAttribute].([]any)
}

// solcastShape expresses the same elements as a Solcast API response
// (period_end in UTC, PT30M), the reference for AC-3/AC-9 equality.
func solcastShape(t *testing.T, states ...EntityState) Forecasts {
	t.Helper()
	var f Forecasts
	for _, st := range states {
		for _, el := range detailed(st) {
			e := el.(map[string]any)
			start, err := time.Parse(time.RFC3339, e["period_start"].(string))
			require.NoError(t, err)
			f.Forecasts = append(f.Forecasts, Forecast{
				PVEstimate: e["pv_estimate"].(float64),
				PeriodEnd:  start.Add(30 * time.Minute).UTC().Format(time.RFC3339),
				Period:     "PT30M",
			})
		}
	}
	return f
}

func warnLogs(logs *observer.ObservedLogs) []observer.LoggedEntry {
	return logs.FilterLevelExact(zapcore.WarnLevel).All()
}

func fieldsOf(e observer.LoggedEntry) map[string]any {
	return e.ContextMap()
}

// T-0301: the today fixture converts to Solcast-shaped periods.
func TestParseDetailedForecast_Valid(t *testing.T) {
	loc := copenhagen(t)
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, loc)
	st := loadStateFixture(t, "ha_solcast_today.json")
	elements := detailed(st)
	require.Len(t, elements, 48)

	got, dropped, err := ParseDetailedForecast(st.Attributes, now)
	require.NoError(t, err)
	assert.Equal(t, 0, dropped)
	require.Len(t, got, 48)
	assert.Equal(t, "2026-10-25T23:30:00Z", got[0].PeriodEnd)
	for i, el := range elements {
		e := el.(map[string]any)
		start, err := time.Parse(time.RFC3339, e["period_start"].(string))
		require.NoError(t, err)
		assert.Equal(t, start.Add(30*time.Minute).UTC().Format(time.RFC3339), got[i].PeriodEnd)
		assert.Equal(t, "PT30M", got[i].Period)
		assert.Equal(t, e["pv_estimate"].(float64), got[i].PVEstimate)
		assert.Zero(t, got[i].PVEstimate10, "pv_estimate10 is ignored")
	}
}

// T-0302: invalid elements are dropped, valid ones kept, 50 kW inclusive.
func TestParseDetailedForecast_DropsInvalid(t *testing.T) {
	loc := copenhagen(t)
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, loc)
	at := func(h int) string { return time.Date(2026, 10, 26, h, 0, 0, 0, loc).Format(time.RFC3339) }

	valid := []any{
		element(at(8), 0.5),
		element(at(9), 1.5),
		element(at(10), 0.0),
		element(at(11), 50.0),
	}
	invalid := []any{
		element(at(12), "1.2"),
		element(at(12), "NaN"),
		element(at(12), nil),
		element(at(12), true),
		element(at(12), -0.1),
		element(at(12), 50.0001),
		element(at(12), 1e6),
		element("2026-10-26T10:00:00", 1.0),
		map[string]any{"period_start": 12345.0, "pv_estimate": 1.0},
		element("garbage", 1.0),
		"not an object",
		element(now.Add(-49*time.Hour).Format(time.RFC3339), 1.0),
		element(now.Add(73*time.Hour).Format(time.RFC3339), 1.0),
	}
	list := append(append([]any{}, valid[:2]...), invalid...)
	list = append(list, valid[2:]...)

	got, dropped, err := ParseDetailedForecast(map[string]any{HAForecastAttribute: list}, now)
	require.NoError(t, err)
	assert.Equal(t, len(invalid), dropped)
	require.Len(t, got, 4)
	total := 0.0
	for _, f := range got {
		total += f.PVEstimate
	}
	assert.InDelta(t, 52.0, total, 1e-9)
	assert.Equal(t, 50.0, got[3].PVEstimate, "50 kW is inside the bound")
}

// T-0303: attribute shapes and size.
func TestParseDetailedForecast_Shapes(t *testing.T) {
	loc := copenhagen(t)
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, loc)
	big := make([]any, HAForecastMaxElements+1)
	for i := range big {
		big[i] = element(now.Format(time.RFC3339), 1.0)
	}
	maxOK := make([]any, HAForecastMaxElements)
	for i := range maxOK {
		maxOK[i] = element(now.Format(time.RFC3339), 1.0)
	}

	tests := []struct {
		name    string
		attrs   map[string]any
		wantErr error
		wantLen int
	}{
		{name: "attribute missing", attrs: map[string]any{"estimate": 1.0}, wantErr: ErrHAForecastAttributeMissing},
		{name: "nil attributes", attrs: nil, wantErr: ErrHAForecastAttributeMissing},
		{name: "object", attrs: map[string]any{HAForecastAttribute: map[string]any{"a": 1.0}}, wantErr: ErrHAForecastFormat},
		{name: "string", attrs: map[string]any{HAForecastAttribute: "[]"}, wantErr: ErrHAForecastFormat},
		{name: "null", attrs: map[string]any{HAForecastAttribute: nil}, wantErr: ErrHAForecastFormat},
		{name: "1001 elements", attrs: map[string]any{HAForecastAttribute: big}, wantErr: ErrHAForecastFormat},
		{name: "1000 elements", attrs: map[string]any{HAForecastAttribute: maxOK}, wantLen: HAForecastMaxElements},
		{name: "empty list", attrs: map[string]any{HAForecastAttribute: []any{}}, wantErr: ErrHAForecastNoValidPeriods},
		{name: "only invalid", attrs: map[string]any{HAForecastAttribute: []any{element("x", 1.0), "y"}}, wantErr: ErrHAForecastNoValidPeriods},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := ParseDetailedForecast(tc.attrs, now)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				for _, other := range []error{ErrHAForecastAttributeMissing, ErrHAForecastFormat, ErrHAForecastNoValidPeriods} {
					if other != tc.wantErr {
						assert.NotErrorIs(t, err, other)
					}
				}
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Len(t, got, tc.wantLen)
		})
	}
}

// T-0304: merge de-duplicates by instant (first wins), drops overlaps and
// keeps both repeated hours on the autumn DST day.
func TestMergeForecastPeriods(t *testing.T) {
	loc := copenhagen(t)
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, loc)
	parse := func(t *testing.T, list ...any) []Forecast {
		t.Helper()
		f, _, err := ParseDetailedForecast(map[string]any{HAForecastAttribute: list}, now)
		require.NoError(t, err)
		return f
	}

	t.Run("same instant with different offsets, first wins", func(t *testing.T) {
		today := parse(t, element("2026-10-26T10:00:00+01:00", 1.0))
		tomorrow := parse(t, element("2026-10-26T09:00:00Z", 2.0))
		for i := 0; i < 100; i++ {
			merged, dups, overlaps := MergeForecastPeriods(today, tomorrow)
			require.Len(t, merged, 1)
			assert.Equal(t, 1.0, merged[0].PVEstimate)
			assert.Equal(t, 1, dups)
			assert.Equal(t, 0, overlaps)
		}
	})

	t.Run("overlapping period dropped", func(t *testing.T) {
		list := parse(t, element("2026-10-26T10:00:00+01:00", 1.0), element("2026-10-26T10:15:00+01:00", 3.0))
		merged, dups, overlaps := MergeForecastPeriods(list)
		require.Len(t, merged, 1)
		assert.Equal(t, 1.0, merged[0].PVEstimate)
		assert.Equal(t, 0, dups)
		assert.Equal(t, 1, overlaps)
	})

	t.Run("tomorrow 00:00 in both entities counts once", func(t *testing.T) {
		shared := element("2026-10-27T00:00:00+01:00", 4.0)
		today := parse(t, element("2026-10-26T23:30:00+01:00", 0.0), shared)
		tomorrow := parse(t, shared, element("2026-10-27T00:30:00+01:00", 2.0))
		merged, dups, _ := MergeForecastPeriods(today, tomorrow)
		assert.Equal(t, 1, dups)
		require.Len(t, merged, 3)
		day := time.Date(2026, 10, 27, 0, 0, 0, 0, loc)
		gotWh, err := GetDayPowerEstimate(Forecasts{Forecasts: merged}, day, nil)
		require.NoError(t, err)
		singleWh, err := GetDayPowerEstimate(Forecasts{Forecasts: parse(t, shared, element("2026-10-27T00:30:00+01:00", 2.0))}, day, nil)
		require.NoError(t, err)
		assert.InDelta(t, singleWh, gotWh, 1e-9)
	})

	t.Run("autumn DST repeated hour kept, result sorted", func(t *testing.T) {
		dstNow := time.Date(2026, 10, 25, 10, 0, 0, 0, loc)
		f, _, err := ParseDetailedForecast(map[string]any{HAForecastAttribute: []any{
			element("2026-10-25T03:00:00+01:00", 3.0),
			element("2026-10-25T02:00:00+01:00", 2.0),
			element("2026-10-25T02:00:00+02:00", 1.0),
		}}, dstNow)
		require.NoError(t, err)
		merged, dups, overlaps := MergeForecastPeriods(f)
		require.Len(t, merged, 3)
		assert.Equal(t, 0, dups)
		assert.Equal(t, 0, overlaps)
		assert.Equal(t, []float64{1.0, 2.0, 3.0}, []float64{merged[0].PVEstimate, merged[1].PVEstimate, merged[2].PVEstimate})
	})

	t.Run("unparseable period_end skipped", func(t *testing.T) {
		merged, dups, overlaps := MergeForecastPeriods([]Forecast{{PeriodEnd: "bad"}})
		assert.Empty(t, merged)
		assert.Zero(t, dups+overlaps)
	})
}

// T-0305: one entity failing still yields the other's periods; both
// failing or a cancelled context yields Missing.
func TestLoadHAForecast_Failures(t *testing.T) {
	loc := copenhagen(t)
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, loc)
	today := loadStateFixture(t, "ha_solcast_today.json")
	noAttr := EntityState{State: "18.4", Attributes: map[string]any{"estimate": 18.4}}
	entities := HAForecastEntities{Today: entToday, Tomorrow: entTomorrow}

	t.Run("tomorrow not found", func(t *testing.T) {
		logs := observeLogs(t)
		r := NewHAForecastReader(newFakeReader().set(entToday, today), entities)
		f, meta, err := r.Load(context.Background(), now)
		require.NoError(t, err)
		assert.False(t, meta.Missing)
		assert.Len(t, f.Forecasts, 48)
		warns := warnLogs(logs)
		require.Len(t, warns, 1)
		assert.Contains(t, warns[0].Message, "not found")
		assert.Equal(t, "pv_entity_tomorrow", fieldsOf(warns[0])["key"])
		assert.Equal(t, entTomorrow, fieldsOf(warns[0])["entity"])
	})

	t.Run("tomorrow attribute missing", func(t *testing.T) {
		logs := observeLogs(t)
		r := NewHAForecastReader(newFakeReader().set(entToday, today).set(entTomorrow, noAttr), entities)
		f, meta, err := r.Load(context.Background(), now)
		require.NoError(t, err)
		assert.False(t, meta.Missing)
		assert.Len(t, f.Forecasts, 48)
		warns := warnLogs(logs)
		require.Len(t, warns, 1)
		assert.Contains(t, warns[0].Message, "attr_brk_halfhourly")
		assert.Equal(t, "pv_entity_tomorrow", fieldsOf(warns[0])["key"])
	})

	t.Run("tomorrow wrong format has its own warn", func(t *testing.T) {
		logs := observeLogs(t)
		r := NewHAForecastReader(newFakeReader().set(entToday, today).set(entTomorrow, stateOf("oops")), entities)
		_, _, err := r.Load(context.Background(), now)
		require.NoError(t, err)
		warns := warnLogs(logs)
		require.Len(t, warns, 1)
		assert.Contains(t, warns[0].Message, "unexpected detailedForecast format")
		assert.NotContains(t, warns[0].Message, "attr_brk_halfhourly")
	})

	t.Run("today transport error", func(t *testing.T) {
		logs := observeLogs(t)
		tomorrow := loadStateFixture(t, "ha_solcast_tomorrow.json")
		r := NewHAForecastReader(newFakeReader().fail(entToday, errors.New("homeassistant: GET /api/states/x: status 502")).set(entTomorrow, tomorrow), entities)
		f, _, err := r.Load(context.Background(), now)
		require.NoError(t, err)
		assert.Len(t, f.Forecasts, 48)
		warns := warnLogs(logs)
		require.Len(t, warns, 1)
		assert.Equal(t, "PV forecast entity could not be read from Home Assistant", warns[0].Message)
		assert.Equal(t, "pv_entity_today", fieldsOf(warns[0])["key"])
	})

	t.Run("both fail", func(t *testing.T) {
		observeLogs(t)
		r := NewHAForecastReader(newFakeReader().set(entTomorrow, noAttr), entities)
		f, meta, err := r.Load(context.Background(), now)
		require.Error(t, err)
		assert.True(t, meta.Missing)
		assert.Empty(t, f.Forecasts)
		assert.ErrorIs(t, err, ErrEntityNotFound)
		assert.ErrorIs(t, err, ErrHAForecastAttributeMissing)
		assert.Contains(t, err.Error(), "pv forecast: "+entTomorrow+": detailedForecast attribute missing; enable attr_brk_halfhourly")
	})

	t.Run("context cancelled", func(t *testing.T) {
		observeLogs(t)
		ctx, cancel := context.WithCancel(context.Background())
		fr := newFakeReader().set(entToday, today)
		fr.onBefore = func(string) { cancel() }
		r := NewHAForecastReader(fr, entities)
		_, meta, err := r.Load(ctx, now)
		require.Error(t, err)
		assert.True(t, meta.Missing)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 0, fr.count(entTomorrow), "no read after cancellation")
	})

	t.Run("no reader", func(t *testing.T) {
		_, meta, err := NewHAForecastReader(nil, entities).Load(context.Background(), now)
		require.Error(t, err)
		assert.True(t, meta.Missing)
	})
}

// T-0306: staleness from the last-polled entity (AC-8, S-024).
func TestLoadHAForecast_Staleness(t *testing.T) {
	loc := copenhagen(t)
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, loc)
	today := loadStateFixture(t, "ha_solcast_today.json")
	tomorrow := loadStateFixture(t, "ha_solcast_tomorrow.json")
	polled := func(ts string) EntityState { return EntityState{State: ts} }
	ago := func(d time.Duration) EntityState { return polled(now.Add(-d).UTC().Format(time.RFC3339)) }
	baseReader := func() *fakeReader { return newFakeReader().set(entToday, today).set(entTomorrow, tomorrow) }

	t.Run("recorded fixture is fresh", func(t *testing.T) {
		observeLogs(t)
		r := NewHAForecastReader(baseReader().set(entPolled, loadStateFixture(t, "ha_solcast_last_polled.json")), testEntities)
		_, meta, err := r.Load(context.Background(), now)
		require.NoError(t, err)
		assert.Equal(t, 3*time.Hour+47*time.Minute+57*time.Second, meta.Age)
	})

	t.Run("23h59m old is used", func(t *testing.T) {
		logs := observeLogs(t)
		r := NewHAForecastReader(baseReader().set(entPolled, ago(23*time.Hour+59*time.Minute)), testEntities)
		f, meta, err := r.Load(context.Background(), now)
		require.NoError(t, err)
		assert.False(t, meta.Stale)
		assert.Equal(t, 23*time.Hour+59*time.Minute, meta.Age)
		assert.Len(t, f.Forecasts, 96)
		assert.Empty(t, warnLogs(logs))
	})

	t.Run("24h1m old is stale and entities are not read", func(t *testing.T) {
		logs := observeLogs(t)
		fr := baseReader().set(entPolled, ago(24*time.Hour+time.Minute))
		r := NewHAForecastReader(fr, testEntities)
		f, meta, err := r.Load(context.Background(), now)
		require.ErrorIs(t, err, ErrHAForecastStale)
		assert.True(t, meta.Stale)
		assert.False(t, meta.Missing)
		assert.Equal(t, 24*time.Hour+time.Minute, meta.Age)
		assert.Empty(t, f.Forecasts)
		assert.Equal(t, 0, fr.count(entToday))
		assert.Equal(t, 0, fr.count(entTomorrow))
		assert.Contains(t, err.Error(), "pv forecast: stale: last polled 2026-10-25T09:59:00+01:00 (24h ago)")
		warns := warnLogs(logs)
		require.Len(t, warns, 1)
		assert.Contains(t, warns[0].Message, "last polled more than 24 h ago")
		assert.Equal(t, 24*time.Hour+time.Minute, fieldsOf(warns[0])["age"])
	})

	t.Run("2h in the future warns and skips the check", func(t *testing.T) {
		logs := observeLogs(t)
		r := NewHAForecastReader(baseReader().set(entPolled, ago(-2*time.Hour)), testEntities)
		f, meta, err := r.Load(context.Background(), now)
		require.NoError(t, err)
		assert.Len(t, f.Forecasts, 96)
		assert.Zero(t, meta.Age)
		warns := warnLogs(logs)
		require.Len(t, warns, 1)
		assert.Contains(t, warns[0].Message, "in the future")
	})

	t.Run("5m in the future is fresh without warn", func(t *testing.T) {
		logs := observeLogs(t)
		r := NewHAForecastReader(baseReader().set(entPolled, ago(-5*time.Minute)), testEntities)
		_, meta, err := r.Load(context.Background(), now)
		require.NoError(t, err)
		assert.Zero(t, meta.Age)
		assert.Empty(t, warnLogs(logs))
	})

	onceCases := []struct {
		name  string
		setup func(*fakeReader)
		msg   string
	}{
		{name: "unavailable", setup: func(f *fakeReader) { f.set(entPolled, polled("unavailable")) }, msg: "not a timestamp"},
		{name: "garbage", setup: func(f *fakeReader) { f.set(entPolled, polled("garbage")) }, msg: "not a timestamp"},
		{name: "404", setup: func(*fakeReader) {}, msg: "could not be read"},
	}
	for _, tc := range onceCases {
		t.Run(tc.name+" warns once per reader", func(t *testing.T) {
			logs := observeLogs(t)
			fr := baseReader()
			tc.setup(fr)
			r := NewHAForecastReader(fr, testEntities)
			for i := 0; i < 3; i++ {
				f, meta, err := r.Load(context.Background(), now)
				require.NoError(t, err)
				assert.False(t, meta.Stale)
				assert.Len(t, f.Forecasts, 96)
			}
			warns := warnLogs(logs)
			require.Len(t, warns, 1)
			assert.Contains(t, warns[0].Message, tc.msg)
			assert.Equal(t, "pv_entity_last_polled", fieldsOf(warns[0])["key"])
			assert.Len(t, logs.FilterMessage(warns[0].Message).FilterLevelExact(zapcore.DebugLevel).All(), 2)
		})
	}

	t.Run("empty last_polled entity is not read", func(t *testing.T) {
		logs := observeLogs(t)
		fr := baseReader()
		r := NewHAForecastReader(fr, HAForecastEntities{Today: entToday, Tomorrow: entTomorrow})
		_, _, err := r.Load(context.Background(), now)
		require.NoError(t, err)
		assert.Equal(t, 0, fr.count(""))
		assert.Empty(t, warnLogs(logs))
		skipped := logs.FilterMessage("PV forecast staleness check skipped").All()
		require.Len(t, skipped, 1)
		assert.Equal(t, zapcore.DebugLevel, skipped[0].Level)
	})
}

// S-026: an offending value is quoted and bounded in logs.
func TestLoadHAForecast_BoundedLogValues(t *testing.T) {
	loc := copenhagen(t)
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, loc)
	logs := observeLogs(t)
	huge := strings.Repeat("A\n", 5000)
	list := append(dayElements(loc, 2026, 10, 26, 0), element(huge, 1.0))
	r := NewHAForecastReader(newFakeReader().set(entToday, stateOf(list)).set(entTomorrow, stateOf(dayElements(loc, 2026, 10, 27, 0))),
		HAForecastEntities{Today: entToday, Tomorrow: entTomorrow})
	_, _, err := r.Load(context.Background(), now)
	require.NoError(t, err)
	dropped := logs.FilterMessage("PV forecast periods dropped").All()
	require.Len(t, dropped, 1)
	ex := fieldsOf(dropped[0])["example"].(string)
	assert.NotContains(t, ex, "\n")
	assert.LessOrEqual(t, len(ex), 256)
	assert.Equal(t, int64(1), fieldsOf(dropped[0])["count"])
}

// T-0307: day coverage in DayProduction (AC-12, S-020).
func TestHADayProduction_Coverage(t *testing.T) {
	loc := copenhagen(t)
	today := loadStateFixture(t, "ha_solcast_today.json")
	tomorrow := loadStateFixture(t, "ha_solcast_tomorrow.json")
	entities := HAForecastEntities{Today: entToday, Tomorrow: entTomorrow}
	coverageWarns := func(logs *observer.ObservedLogs) []observer.LoggedEntry {
		return logs.FilterMessage("PV forecast covers only part of the forecast day").All()
	}

	t.Run("tomorrow not covered", func(t *testing.T) {
		logs := observeLogs(t)
		now := time.Date(2026, 10, 26, 10, 0, 0, 0, loc)
		r := NewHAForecastReader(newFakeReader().set(entToday, today).set(entTomorrow, today), entities)
		wh, ok, err := r.DayProduction(context.Background(), ForecastHorizonTomorrow, now)
		require.ErrorIs(t, err, ErrForecastDayNotCovered)
		assert.False(t, ok)
		assert.Zero(t, wh)
		assert.Len(t, logs.FilterMessageSnippet("no periods for the forecast day").FilterLevelExact(zapcore.WarnLevel).All(), 1)
		assert.Empty(t, logs.FilterMessageSnippet("Total Forecast Solar Power").All())
	})

	t.Run("today from 12:00 only", func(t *testing.T) {
		logs := observeLogs(t)
		now := time.Date(2026, 10, 26, 13, 0, 0, 0, loc)
		r := NewHAForecastReader(newFakeReader().set(entToday, stateOf(dayElements(loc, 2026, 10, 26, 12))).set(entTomorrow, tomorrow), entities)
		_, ok, err := r.DayProduction(context.Background(), ForecastHorizonToday, now)
		require.NoError(t, err)
		assert.True(t, ok)
		warns := coverageWarns(logs)
		require.Len(t, warns, 1)
		assert.Equal(t, int64(24), fieldsOf(warns[0])["covered"])
		assert.Equal(t, int64(48), fieldsOf(warns[0])["expected"])
	})

	t.Run("autumn DST day, 50 periods", func(t *testing.T) {
		logs := observeLogs(t)
		now := time.Date(2026, 10, 25, 9, 0, 0, 0, loc)
		r := NewHAForecastReader(newFakeReader().set(entToday, loadStateFixture(t, "ha_solcast_dst_autumn.json")).set(entTomorrow, today), entities)
		_, ok, err := r.DayProduction(context.Background(), ForecastHorizonToday, now)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Empty(t, coverageWarns(logs))
		fp := logs.FilterMessage("PV forecast input fingerprint").All()
		require.Len(t, fp, 1)
		assert.Equal(t, int64(50), fieldsOf(fp[0])["covered"])
		assert.Equal(t, int64(50), fieldsOf(fp[0])["expected"])
	})

	t.Run("spring DST day, 46 periods", func(t *testing.T) {
		logs := observeLogs(t)
		now := time.Date(2026, 3, 29, 9, 0, 0, 0, loc)
		r := NewHAForecastReader(newFakeReader().
			set(entToday, loadStateFixture(t, "ha_solcast_dst_spring.json")).
			set(entTomorrow, stateOf(dayElements(loc, 2026, 3, 30, 0))), entities)
		_, ok, err := r.DayProduction(context.Background(), ForecastHorizonToday, now)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Empty(t, coverageWarns(logs))
		fp := logs.FilterMessage("PV forecast input fingerprint").All()
		require.Len(t, fp, 1)
		assert.Equal(t, int64(46), fieldsOf(fp[0])["expected"])
	})

	t.Run("remaining_today at 20:00", func(t *testing.T) {
		logs := observeLogs(t)
		now := time.Date(2026, 10, 26, 20, 0, 0, 0, loc)
		r := NewHAForecastReader(newFakeReader().set(entToday, today).set(entTomorrow, tomorrow), entities)
		_, ok, err := r.DayProduction(context.Background(), ForecastHorizonRemainingToday, now)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Empty(t, coverageWarns(logs))
		fp := logs.FilterMessage("PV forecast input fingerprint").All()
		require.Len(t, fp, 1)
		f := fieldsOf(fp[0])
		assert.Equal(t, int64(8), f["expected"])
		assert.Equal(t, int64(8), f["covered"])
		assert.Equal(t, PVSourceHASolcast, f["pv_source"])
		assert.Equal(t, int64(96), f["periods"])
		assert.Contains(t, f, "total_wh")
	})

	t.Run("stale forecast is an error", func(t *testing.T) {
		observeLogs(t)
		now := time.Date(2026, 10, 26, 10, 0, 0, 0, loc)
		fr := newFakeReader().set(entToday, today).set(entTomorrow, tomorrow).
			set(entPolled, EntityState{State: now.Add(-30 * time.Hour).Format(time.RFC3339)})
		wh, ok, err := NewHAForecastReader(fr, testEntities).DayProduction(context.Background(), ForecastHorizonToday, now)
		require.ErrorIs(t, err, ErrHAForecastStale)
		assert.False(t, ok)
		assert.Zero(t, wh)
	})

	t.Run("both entities missing is an error", func(t *testing.T) {
		observeLogs(t)
		now := time.Date(2026, 10, 26, 10, 0, 0, 0, loc)
		_, ok, err := NewHAForecastReader(newFakeReader(), entities).DayProduction(context.Background(), ForecastHorizonToday, now)
		require.ErrorIs(t, err, ErrEntityNotFound)
		assert.False(t, ok)
	})
}

// T-0308: the HA day total equals GetDayPowerEstimate on the same periods in
// Solcast API shape, for every horizon, on a normal and both DST days.
func TestHADayProduction_EqualsSolcastPath(t *testing.T) {
	loc := copenhagen(t)
	scenarios := []struct {
		name            string
		today, tomorrow EntityState
		y               int
		m               time.Month
		d               int
	}{
		{name: "normal", today: loadStateFixture(t, "ha_solcast_today.json"), tomorrow: loadStateFixture(t, "ha_solcast_tomorrow.json"), y: 2026, m: 10, d: 26},
		{name: "autumn DST", today: loadStateFixture(t, "ha_solcast_dst_autumn.json"), tomorrow: loadStateFixture(t, "ha_solcast_today.json"), y: 2026, m: 10, d: 25},
		{name: "spring DST", today: loadStateFixture(t, "ha_solcast_dst_spring.json"), tomorrow: stateOf(dayElements(loc, 2026, 3, 30, 0)), y: 2026, m: 3, d: 29},
	}
	horizons := []string{ForecastHorizonToday, ForecastHorizonTomorrow, ForecastHorizonRemainingToday, ForecastHorizonNextSolarDay, ForecastHorizonDefault}

	for _, sc := range scenarios {
		solcast := solcastShape(t, sc.today, sc.tomorrow)
		for _, hour := range []int{9, 15} {
			for _, h := range horizons {
				t.Run(fmt.Sprintf("%s %02d:00 %s", sc.name, hour, h), func(t *testing.T) {
					logs := observeLogs(t)
					now := time.Date(sc.y, sc.m, sc.d, hour, 0, 0, 0, loc)
					r := NewHAForecastReader(newFakeReader().set(entToday, sc.today).set(entTomorrow, sc.tomorrow),
						HAForecastEntities{Today: entToday, Tomorrow: entTomorrow})
					got, ok, err := r.DayProduction(context.Background(), h, now)
					require.NoError(t, err)
					require.True(t, ok)

					day, after, _ := ResolveForecastDay(h, now)
					want, err := GetDayPowerEstimate(solcast, day, after)
					require.NoError(t, err)
					assert.InDelta(t, want, got, 1e-9)
					if h != ForecastHorizonRemainingToday {
						assert.Greater(t, got, 0.0)
					}

					totals := logs.FilterMessageSnippet("Total Forecast Solar Power").All()
					require.Len(t, totals, 1)
					assert.Equal(t, fmt.Sprintf("Total Forecast Solar Power for %d/%d/%d: %d Wh", day.Day(), day.Month(), day.Year(), int(got)), totals[0].Message)
					assert.Equal(t, zapcore.InfoLevel, totals[0].Level)
				})
			}
		}
	}
}

// T-0309: horizon off makes no reads; days are assigned by date, not by the
// entity that delivered the period.
func TestHADayProduction_HorizonAndRollover(t *testing.T) {
	loc := copenhagen(t)
	today := loadStateFixture(t, "ha_solcast_today.json")
	tomorrow := loadStateFixture(t, "ha_solcast_tomorrow.json")

	t.Run("off", func(t *testing.T) {
		logs := observeLogs(t)
		fr := newFakeReader().set(entToday, today).set(entTomorrow, tomorrow)
		wh, ok, err := NewHAForecastReader(fr, testEntities).DayProduction(context.Background(), ForecastHorizonOff, time.Date(2026, 10, 26, 10, 0, 0, 0, loc))
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Zero(t, wh)
		assert.Zero(t, fr.count(entToday)+fr.count(entTomorrow)+fr.count(entPolled))
		assert.Len(t, logs.FilterMessage(`Forecast horizon "off": skipping forecast retrieval`).All(), 1)
	})

	t.Run("before the integration's midnight rollover", func(t *testing.T) {
		observeLogs(t)
		now := time.Date(2026, 10, 27, 0, 3, 0, 0, loc)
		// The today entity still holds the 26th, the tomorrow entity the 27th.
		fr := newFakeReader().set(entToday, today).set(entTomorrow, tomorrow)
		got, ok, err := NewHAForecastReader(fr, HAForecastEntities{Today: entToday, Tomorrow: entTomorrow}).
			DayProduction(context.Background(), ForecastHorizonToday, now)
		require.NoError(t, err)
		require.True(t, ok)

		want := 0.0
		for _, el := range detailed(tomorrow) {
			want += el.(map[string]any)["pv_estimate"].(float64) * 0.5 * 1000
		}
		assert.Greater(t, want, 0.0)
		assert.InDelta(t, want, got, 1e-6)
	})
}

func TestExpectedAndCountDayPeriods(t *testing.T) {
	loc := copenhagen(t)
	at := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, loc) }
	ptr := func(t time.Time) *time.Time { return &t }

	tests := []struct {
		name  string
		day   time.Time
		after *time.Time
		want  int
	}{
		{name: "normal day", day: at(2026, 10, 26, 0, 0), want: 48},
		{name: "day given at noon", day: at(2026, 10, 26, 12, 0), want: 48},
		{name: "spring DST", day: at(2026, 3, 29, 0, 0), want: 46},
		{name: "autumn DST", day: at(2026, 10, 25, 0, 0), want: 50},
		{name: "after 20:00", day: at(2026, 10, 26, 0, 0), after: ptr(at(2026, 10, 26, 20, 0)), want: 8},
		{name: "after 20:10", day: at(2026, 10, 26, 0, 0), after: ptr(at(2026, 10, 26, 20, 10)), want: 8},
		{name: "after 23:45", day: at(2026, 10, 26, 0, 0), after: ptr(at(2026, 10, 26, 23, 45)), want: 1},
		{name: "after before the day", day: at(2026, 10, 26, 0, 0), after: ptr(at(2026, 10, 25, 20, 0)), want: 48},
		{name: "after the day", day: at(2026, 10, 26, 0, 0), after: ptr(at(2026, 10, 27, 1, 0)), want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ExpectedDayPeriods(tc.day, tc.after))
		})
	}

	// A full day of periods covers exactly the expected half-hours.
	full := solcastShape(t, stateOf(dayElements(loc, 2026, 10, 25, 0)))
	assert.Equal(t, 50, CountDayPeriods(full, at(2026, 10, 25, 0, 0), nil))
	assert.Equal(t, 0, CountDayPeriods(full, at(2026, 10, 26, 0, 0), nil), "the 23:30 period does not cover the next day")
	assert.Equal(t, 8, CountDayPeriods(full, at(2026, 10, 25, 0, 0), ptr(at(2026, 10, 25, 20, 0))))
	bad := Forecasts{Forecasts: []Forecast{{PeriodEnd: "bad"}}}
	assert.Equal(t, 0, CountDayPeriods(bad, at(2026, 10, 25, 0, 0), nil))
}

func TestQuoteTrunc(t *testing.T) {
	assert.Equal(t, `"abc"`, quoteTrunc("abc"))
	long := strings.Repeat("é", 40) // 80 bytes
	got := quoteTrunc(long)
	assert.Contains(t, got, "(truncated)")
	assert.NotContains(t, got, "�")
	assert.True(t, math.Abs(float64(len(got))-float64(len(`"`)*2+64+len("(truncated)"))) <= 2)
}

// T-0305 / S-026: both entity errors stay on one line and remain matchable.
func TestJoinErrors_SingleLine(t *testing.T) {
	err := joinErrors(fmt.Errorf("today: %w", ErrHAForecastAttributeMissing), fmt.Errorf("tomorrow: %w", ErrEntityNotFound))
	assert.Equal(t, "today: "+ErrHAForecastAttributeMissing.Error()+"; tomorrow: "+ErrEntityNotFound.Error(), err.Error())
	assert.NotContains(t, err.Error(), "\n")
	assert.ErrorIs(t, err, ErrHAForecastAttributeMissing)
	assert.ErrorIs(t, err, ErrEntityNotFound)
	assert.NotErrorIs(t, err, ErrHAForecastStale)
}

// LastForecasts exposes the merged periods of the last successful
// DayProduction call as one site, for reserve_horizon until_solar.
func TestHAForecastReader_LastForecasts(t *testing.T) {
	loc := copenhagen(t)
	entities := HAForecastEntities{Today: entToday, Tomorrow: entTomorrow}
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, loc)
	fake := newFakeReader().
		set(entToday, stateOf(dayElements(loc, 2026, 10, 26, 0))).
		set(entTomorrow, stateOf(dayElements(loc, 2026, 10, 27, 0)))
	r := NewHAForecastReader(fake, entities)

	assert.Nil(t, r.LastForecasts(), "nil before the first read")

	_, ok, err := r.DayProduction(context.Background(), ForecastHorizonToday, now)
	require.NoError(t, err)
	require.True(t, ok)
	sites := r.LastForecasts()
	require.Len(t, sites, 1, "the integration's sites arrive merged")
	assert.Len(t, sites[0].Forecasts, 96)
	for _, f := range sites[0].Forecasts {
		_, perr := time.Parse(time.RFC3339, f.PeriodEnd)
		require.NoError(t, perr, "period_end must parse like a Solcast API period")
	}

	// A failed read keeps the previous periods; the runner uses them only
	// after a successful read.
	failing := NewHAForecastReader(newFakeReader(), entities)
	_, _, err = failing.DayProduction(context.Background(), ForecastHorizonToday, now)
	require.Error(t, err)
	assert.Nil(t, failing.LastForecasts())
}
