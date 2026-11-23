package power

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	u "sbam/src/utils"
)

// Home Assistant Solcast integration constants (SPEC-0003).
const (
	// HAForecastAttribute is the state attribute holding the half-hourly
	// forecast of the Solcast PV Forecast integration.
	HAForecastAttribute = "detailedForecast"
	// HAForecastPeriod is the length of one detailedForecast element.
	HAForecastPeriod = 30 * time.Minute
	// HAForecastMaxAge is the age of the integration's last poll beyond which
	// the forecast is not used (AC-8).
	HAForecastMaxAge = 24 * time.Hour
	// HAForecastMaxElements is the largest accepted detailedForecast list;
	// a longer list fails the entity (AC-13, S-022).
	HAForecastMaxElements = 1000
	// HAForecastMaxKW is the largest accepted pv_estimate in kW. Larger
	// values are dropped, not clamped (AC-13, S-022).
	HAForecastMaxKW = 50.0
	// HAForecastPastWindow and HAForecastFutureWindow bound period_start
	// around now (AC-13).
	HAForecastPastWindow   = 48 * time.Hour
	HAForecastFutureWindow = 72 * time.Hour
	// HALastPolledMaxSkew is how far the last-polled timestamp may lie in the
	// future before the staleness check is skipped (S-024).
	HALastPolledMaxSkew = 10 * time.Minute
)

// Config key names used in log lines so the user knows which key to fix.
const (
	keyEntityToday      = "pv_entity_today"
	keyEntityTomorrow   = "pv_entity_tomorrow"
	keyEntityLastPolled = "pv_entity_last_polled"
)

// maxQuotedBytes bounds any offending value quoted in an error or log (S-026).
const maxQuotedBytes = 64

// Sentinel errors of the Home Assistant forecast reader.
var (
	// ErrEntityNotFound is wrapped by EntityReader implementations when the
	// entity does not exist (HTTP 404), so the reader can log the
	// "check the entity ID" hint.
	ErrEntityNotFound = errors.New("entity not found")
	// ErrHAForecastAttributeMissing: the entity has no detailedForecast
	// attribute (the integration's half-hourly breakdown is off).
	ErrHAForecastAttributeMissing = errors.New("detailedForecast attribute missing; enable attr_brk_halfhourly")
	// ErrHAForecastFormat: detailedForecast is not a list or is too long.
	ErrHAForecastFormat = errors.New("unexpected detailedForecast format")
	// ErrHAForecastNoValidPeriods: detailedForecast holds no valid element.
	ErrHAForecastNoValidPeriods = errors.New("no usable detailedForecast periods")
	// ErrHAForecastStale: the integration last polled more than
	// HAForecastMaxAge ago.
	ErrHAForecastStale = errors.New("stale")
	// ErrForecastDayNotCovered: no valid period falls on the resolved
	// forecast day (AC-12, S-020).
	ErrForecastDayNotCovered = errors.New("no forecast periods for the forecast day")
)

// EntityState is the part of a Home Assistant entity state the forecast
// reader needs. Attributes is the JSON object decoded with encoding/json.
type EntityState struct {
	State      string
	Attributes map[string]any
}

// EntityReader reads one Home Assistant entity state. pkg/cmd adapts the HA
// client to it; pkg/power does not import pkg/homeassistant. Implementations
// should wrap ErrEntityNotFound for a missing entity.
type EntityReader interface {
	ReadEntity(ctx context.Context, entityID string) (EntityState, error)
}

// HAForecastEntities names the integration's entities. LastPolled may be
// empty, which disables the staleness check.
type HAForecastEntities struct {
	Today      string
	Tomorrow   string
	LastPolled string
}

// HAForecastReader reads the PV forecast from the Home Assistant Solcast
// integration. Create one per process with NewHAForecastReader: it remembers
// which last-polled problems were already logged at Warn (once per process,
// S-024). It is safe for concurrent use.
type HAForecastReader struct {
	reader   EntityReader
	entities HAForecastEntities

	mu     sync.Mutex
	warned map[string]bool
	last   *Forecasts // periods behind the last successful DayProduction
}

// NewHAForecastReader returns a reader for the given entities.
func NewHAForecastReader(r EntityReader, e HAForecastEntities) *HAForecastReader {
	return &HAForecastReader{reader: r, entities: e, warned: map[string]bool{}}
}

// LastForecasts returns the merged periods behind the last successful
// DayProduction call as a single site (nil before that), in the same shape
// as power.LastForecasts, so reserve_horizon until_solar can use them.
func (h *HAForecastReader) LastForecasts() []Forecasts {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last == nil {
		return nil
	}
	return []Forecasts{*h.last}
}

// Load reads, validates and merges the today and tomorrow forecasts.
//
//   - A last-polled timestamp older than HAForecastMaxAge returns
//     meta.Stale and an error wrapping ErrHAForecastStale; the forecast
//     entities are not read.
//   - When both entities fail, meta.Missing is set and the error joins both
//     entity errors. When one fails, the other's periods are returned with a
//     nil error (the Warn is logged here).
//   - A cancelled context returns meta.Missing and the context error.
//
// err is non-nil exactly when no forecast is returned.
func (h *HAForecastReader) Load(ctx context.Context, now time.Time) (Forecasts, ForecastSourceMeta, error) {
	if h == nil || h.reader == nil {
		return Forecasts{}, ForecastSourceMeta{Missing: true}, errors.New("pv forecast: no Home Assistant reader configured")
	}
	if err := ctx.Err(); err != nil {
		return Forecasts{}, ForecastSourceMeta{Missing: true}, fmt.Errorf("pv forecast: %w", err)
	}

	poll, err := h.checkLastPolled(ctx, now)
	if err != nil {
		return Forecasts{}, ForecastSourceMeta{Missing: true}, err
	}
	if poll.checked && poll.age > HAForecastMaxAge {
		return Forecasts{}, ForecastSourceMeta{Stale: true, Age: poll.age, LastPolled: poll.polled}, h.staleError(poll, now)
	}

	today, errToday := h.readForecastEntity(ctx, keyEntityToday, h.entities.Today, now)
	if err := ctx.Err(); err != nil {
		return Forecasts{}, ForecastSourceMeta{Missing: true, Age: poll.age}, fmt.Errorf("pv forecast: %w", err)
	}
	tomorrow, errTomorrow := h.readForecastEntity(ctx, keyEntityTomorrow, h.entities.Tomorrow, now)
	if err := ctx.Err(); err != nil {
		return Forecasts{}, ForecastSourceMeta{Missing: true, Age: poll.age}, fmt.Errorf("pv forecast: %w", err)
	}
	if errToday != nil && errTomorrow != nil {
		return Forecasts{}, ForecastSourceMeta{Missing: true, Age: poll.age, LastPolled: poll.polled}, joinErrors(errToday, errTomorrow)
	}

	res := mergeForecastPeriods(today, tomorrow)
	if res.duplicates > 0 {
		u.Log.Debugw("PV forecast duplicate periods ignored", "count", res.duplicates, "first", res.firstDuplicate)
	}
	if res.overlaps > 0 {
		u.Log.Debugw("PV forecast overlapping periods ignored", "count", res.overlaps)
	}
	h.logReadSuccess(res.merged, poll)
	return Forecasts{Forecasts: res.merged}, ForecastSourceMeta{Age: poll.age, LastPolled: poll.polled}, nil
}

// DayProduction returns the PV production in Wh for the day resolved by
// ResolveForecastDay(horizon, now), computed by GetDayPowerEstimate over the
// merged HA periods (AC-3). ok is false when no forecast is used:
//
//   - horizon off: no HA read, nil error;
//   - stale or missing forecast: the Load error;
//   - no valid period on the resolved day while some are expected: an error
//     wrapping ErrForecastDayNotCovered (AC-12).
//
// Partial coverage is used with a Warn naming the covered and expected
// half-hours. Each call with a forecast logs one Info fingerprint (S-028).
func (h *HAForecastReader) DayProduction(ctx context.Context, horizon string, now time.Time) (float64, bool, error) {
	day, after, skip := ResolveForecastDay(horizon, now)
	if skip {
		u.Log.Infof("Forecast horizon %q: skipping forecast retrieval", horizon)
		return 0, false, nil
	}

	f, _, err := h.Load(ctx, now)
	if err != nil {
		return 0, false, err
	}

	covered := CountDayPeriods(f, day, after)
	expected := ExpectedDayPeriods(day, after)
	dayText := day.Format("2006-01-02")
	if covered == 0 && expected > 0 {
		u.Log.Warnw("PV forecast has no periods for the forecast day; forecast charging is disabled for this run",
			"day", dayText, "horizon", horizon, keyEntityToday, h.entities.Today, keyEntityTomorrow, h.entities.Tomorrow)
		return 0, false, fmt.Errorf("pv forecast: %w: %s (horizon %s)", ErrForecastDayNotCovered, dayText, horizon)
	}
	if covered < expected {
		u.Log.Warnw("PV forecast covers only part of the forecast day", "day", dayText, "covered", covered, "expected", expected)
	}

	wh, err := GetDayPowerEstimate(f, day, after)
	if err != nil {
		return 0, false, fmt.Errorf("pv forecast: %w", err)
	}
	logTotalForecast(day, wh)
	h.mu.Lock()
	h.last = &f
	h.mu.Unlock()
	u.Log.Infow("PV forecast input fingerprint", "pv_source", PVSourceHASolcast, "periods", len(f.Forecasts),
		"total_wh", int(wh), "covered", covered, "expected", expected)
	return wh, true, nil
}

// lastPolled is the outcome of the staleness check.
type lastPolled struct {
	checked bool
	polled  time.Time
	age     time.Duration
}

// checkLastPolled reads the last-polled entity. Only a cancelled context
// returns an error; every other problem skips the check with a log line.
func (h *HAForecastReader) checkLastPolled(ctx context.Context, now time.Time) (lastPolled, error) {
	entity := h.entities.LastPolled
	if entity == "" {
		u.Log.Debugw("PV forecast staleness check skipped", "reason", "pv_entity_last_polled is empty")
		return lastPolled{}, nil
	}

	st, err := h.reader.ReadEntity(ctx, entity)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return lastPolled{}, fmt.Errorf("pv forecast: %w", ctxErr)
		}
		h.warnOnce("unreadable", "PV forecast staleness check skipped: pv_entity_last_polled could not be read",
			"key", keyEntityLastPolled, "entity", entity, "error", err)
		return lastPolled{}, nil
	}

	state := strings.TrimSpace(st.State)
	polled, err := time.Parse(time.RFC3339Nano, state)
	if err != nil {
		h.warnOnce("unparseable", "PV forecast staleness check skipped: pv_entity_last_polled state is not a timestamp",
			"key", keyEntityLastPolled, "entity", entity, "state", quoteTrunc(state))
		return lastPolled{}, nil
	}

	age := now.Sub(polled)
	if age < -HALastPolledMaxSkew {
		h.warnOnce("future", "PV forecast staleness check skipped: pv_entity_last_polled is in the future",
			"key", keyEntityLastPolled, "entity", entity, "last_polled", polled.In(now.Location()).Format(time.RFC3339), "ahead", (-age).Round(time.Minute))
		return lastPolled{}, nil
	}
	if age < 0 {
		age = 0
	}
	return lastPolled{checked: true, polled: polled, age: age}, nil
}

// staleError logs the stale Warn and returns the error published by the
// caller (AC-8).
func (h *HAForecastReader) staleError(p lastPolled, now time.Time) error {
	polled := p.polled.In(now.Location()).Format(time.RFC3339)
	u.Log.Warnw("PV forecast not used: the Solcast integration last polled more than 24 h ago; check its API quota and log in Home Assistant",
		"key", keyEntityLastPolled, "entity", h.entities.LastPolled, "last_polled", polled, "age", p.age.Round(time.Minute), "max_age", HAForecastMaxAge)
	return fmt.Errorf("pv forecast: %w: last polled %s (%dh ago)", ErrHAForecastStale, polled, int(p.age.Hours()))
}

// warnOnce logs msg at Warn the first time cause occurs for this reader and
// at Debug afterwards (S-024).
func (h *HAForecastReader) warnOnce(cause, msg string, kv ...any) {
	h.mu.Lock()
	first := !h.warned[cause]
	h.warned[cause] = true
	h.mu.Unlock()
	if first {
		u.Log.Warnw(msg, kv...)
		return
	}
	u.Log.Debugw(msg, kv...)
}

// readForecastEntity reads and parses one forecast entity, logging the
// AC-7 Warn lines. The returned error names the entity.
func (h *HAForecastReader) readForecastEntity(ctx context.Context, key, entity string, now time.Time) ([]Forecast, error) {
	if entity == "" {
		err := fmt.Errorf("pv forecast: %s is not configured", key)
		u.Log.Warnw("PV forecast entity could not be read from Home Assistant", "key", key, "entity", entity, "error", err)
		return nil, err
	}

	st, err := h.reader.ReadEntity(ctx, entity)
	if err != nil {
		if ctx.Err() == nil {
			logReadFailure(key, entity, err)
		}
		return nil, fmt.Errorf("pv forecast: %s: %w", entity, err)
	}

	periods, drops, elements, err := parseDetailedForecast(st.Attributes, now)
	if drops.total() > 0 {
		u.Log.Debugw("PV forecast periods dropped", "entity", entity, "count", drops.total(),
			"not_object", drops.notObject, "bad_period_start", drops.periodStart,
			"out_of_window", drops.outOfWindow, "bad_pv_estimate", drops.estimate, "example", drops.example)
	}
	if err != nil {
		logParseFailure(key, entity, elements, err)
		return nil, fmt.Errorf("pv forecast: %s: %w", entity, err)
	}
	return periods, nil
}

// logReadFailure logs the Warn for a failed entity read (AC-7).
func logReadFailure(key, entity string, err error) {
	if errors.Is(err, ErrEntityNotFound) {
		u.Log.Warnw("PV forecast entity not found in Home Assistant; check the entity ID and that the Solcast PV Forecast integration is loaded",
			"key", key, "entity", entity)
		return
	}
	u.Log.Warnw("PV forecast entity could not be read from Home Assistant", "key", key, "entity", entity, "error", err)
}

// logParseFailure logs the Warn for an unusable attribute (AC-7, AC-13).
func logParseFailure(key, entity string, elements int, err error) {
	switch {
	case errors.Is(err, ErrHAForecastAttributeMissing):
		u.Log.Warnw("PV forecast entity has no detailedForecast attribute; enable the half-hourly breakdown (attr_brk_halfhourly) in the Solcast PV Forecast integration options",
			"key", key, "entity", entity)
	case errors.Is(err, ErrHAForecastFormat):
		u.Log.Warnw("PV forecast entity has an unexpected detailedForecast format", "key", key, "entity", entity, "error", err)
	default:
		u.Log.Warnw("PV forecast entity has no usable detailedForecast periods", "key", key, "entity", entity, "elements", elements)
	}
}

// logReadSuccess logs the Debug summary of a successful read.
func (h *HAForecastReader) logReadSuccess(merged []Forecast, p lastPolled) {
	polled := "unchecked"
	if p.checked {
		polled = p.polled.Format(time.RFC3339)
	}
	first, last := "", ""
	if len(merged) > 0 {
		first, last = merged[0].PeriodEnd, merged[len(merged)-1].PeriodEnd
	}
	u.Log.Debugw("PV forecast read from Home Assistant", "periods", len(merged), "first_period_end", first, "last_period_end", last, "last_polled", polled)
}

// dropStats counts dropped detailedForecast elements per reason.
type dropStats struct {
	notObject   int
	periodStart int
	outOfWindow int
	estimate    int
	// example is the first offending value, quoted and bounded (S-026).
	example string
}

func (d dropStats) total() int {
	return d.notObject + d.periodStart + d.outOfWindow + d.estimate
}

// Drop reasons returned by parseElement.
const (
	dropNotObject   = "not_object"
	dropPeriodStart = "period_start"
	dropOutOfWindow = "out_of_window"
	dropEstimate    = "pv_estimate"
)

func (d *dropStats) add(reason, example string) {
	switch reason {
	case dropNotObject:
		d.notObject++
	case dropPeriodStart:
		d.periodStart++
	case dropOutOfWindow:
		d.outOfWindow++
	default:
		d.estimate++
	}
	if d.example == "" {
		d.example = reason + "=" + example
	}
}

// ParseDetailedForecast converts the detailedForecast attribute into
// Solcast-shaped forecast periods (period_end = period_start + 30 min in
// UTC, period PT30M, pv_estimate in kW) and returns the number of dropped
// elements.
//
// The attribute must be a list of at most HAForecastMaxElements elements
// (ErrHAForecastFormat otherwise; ErrHAForecastAttributeMissing when it is
// absent). An element is kept only when period_start is RFC 3339 with an
// explicit offset within [now−48 h, now+72 h] and pv_estimate is a finite
// JSON number in [0, 50] kW (AC-13). No valid element gives
// ErrHAForecastNoValidPeriods.
func ParseDetailedForecast(attrs map[string]any, now time.Time) ([]Forecast, int, error) {
	periods, drops, _, err := parseDetailedForecast(attrs, now)
	return periods, drops.total(), err
}

func parseDetailedForecast(attrs map[string]any, now time.Time) ([]Forecast, dropStats, int, error) {
	var drops dropStats
	raw, ok := attrs[HAForecastAttribute]
	if !ok {
		return nil, drops, 0, ErrHAForecastAttributeMissing
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, drops, 0, fmt.Errorf("%w: got %s, want a list", ErrHAForecastFormat, jsonKind(raw))
	}
	if len(list) > HAForecastMaxElements {
		return nil, drops, len(list), fmt.Errorf("%w: %d elements, at most %d", ErrHAForecastFormat, len(list), HAForecastMaxElements)
	}

	lo, hi := now.Add(-HAForecastPastWindow), now.Add(HAForecastFutureWindow)
	periods := make([]Forecast, 0, len(list))
	for _, el := range list {
		f, reason, example := parseElement(el, lo, hi)
		if reason != "" {
			drops.add(reason, example)
			continue
		}
		periods = append(periods, f)
	}
	if len(periods) == 0 {
		return nil, drops, len(list), fmt.Errorf("%w: %d elements, none valid", ErrHAForecastNoValidPeriods, len(list))
	}
	return periods, drops, len(list), nil
}

// parseElement validates one element. reason is empty for a valid element;
// otherwise it names the drop reason and example the offending value.
func parseElement(el any, lo, hi time.Time) (Forecast, string, string) {
	obj, ok := el.(map[string]any)
	if !ok {
		return Forecast{}, dropNotObject, jsonKind(el)
	}
	ps, ok := obj["period_start"].(string)
	if !ok {
		return Forecast{}, dropPeriodStart, describeValue(obj["period_start"])
	}
	start, err := time.Parse(time.RFC3339Nano, ps)
	if err != nil {
		return Forecast{}, dropPeriodStart, quoteTrunc(ps)
	}
	if start.Before(lo) || start.After(hi) {
		return Forecast{}, dropOutOfWindow, quoteTrunc(ps)
	}
	kw, ok := obj["pv_estimate"].(float64)
	if !ok {
		return Forecast{}, dropEstimate, describeValue(obj["pv_estimate"])
	}
	if math.IsNaN(kw) || math.IsInf(kw, 0) || kw < 0 || kw > HAForecastMaxKW {
		return Forecast{}, dropEstimate, describeValue(kw)
	}
	return Forecast{
		PVEstimate: kw,
		PeriodEnd:  start.Add(HAForecastPeriod).UTC().Format(time.RFC3339Nano),
		Period:     "PT30M",
	}, "", ""
}

// mergeResult is the outcome of mergeForecastPeriods.
type mergeResult struct {
	merged         []Forecast
	duplicates     int
	overlaps       int
	firstDuplicate string
}

// MergeForecastPeriods merges 30-minute forecast periods (as returned by
// ParseDetailedForecast) from several lists. Periods are keyed by instant,
// not by string, and the first occurrence wins in list order, then element
// order. A period that overlaps an already kept one is dropped. The result
// is sorted by time. Entries with an unparseable period_end are skipped.
func MergeForecastPeriods(lists ...[]Forecast) (merged []Forecast, duplicates, overlaps int) {
	res := mergeForecastPeriods(lists...)
	return res.merged, res.duplicates, res.overlaps
}

func mergeForecastPeriods(lists ...[]Forecast) mergeResult {
	type kept struct {
		start, end time.Time
		f          Forecast
	}
	var ks []kept // sorted by start
	var res mergeResult
	for _, list := range lists {
		for _, f := range list {
			end, err := time.Parse(time.RFC3339Nano, f.PeriodEnd)
			if err != nil {
				continue
			}
			start := end.Add(-HAForecastPeriod)
			i := sort.Search(len(ks), func(i int) bool { return !ks[i].start.Before(start) })
			switch {
			case i < len(ks) && ks[i].start.Equal(start):
				res.duplicates++
				if res.firstDuplicate == "" {
					res.firstDuplicate = start.Format(time.RFC3339)
				}
				continue
			case i > 0 && ks[i-1].end.After(start), i < len(ks) && ks[i].start.Before(end):
				res.overlaps++
				continue
			}
			ks = slices.Insert(ks, i, kept{start: start, end: end, f: f})
		}
	}
	res.merged = make([]Forecast, len(ks))
	for i, k := range ks {
		res.merged[i] = k.f
	}
	return res
}

// CountDayPeriods counts the 30-minute periods that cover a half-hour of day
// (local timezone of day): the period's start (period_end − 30 min) falls on
// day and, when after is set, its end lies after it. It is the "covered"
// count of AC-12 and pairs with ExpectedDayPeriods.
//
// Coverage is counted by start, not by the end-date rule GetDayPowerEstimate
// uses for the sum: by end date, yesterday's 23:30 period would cover a day
// with no other data, and the today entity (00:00–23:30 starts) would
// always look one period short.
func CountDayPeriods(f Forecasts, day time.Time, after *time.Time) int {
	loc := day.Location()
	n := 0
	for _, p := range f.Forecasts {
		end, err := time.Parse(time.RFC3339Nano, p.PeriodEnd)
		if err != nil {
			continue
		}
		start := end.Add(-HAForecastPeriod).In(loc)
		if !sameDay(start, day) {
			continue
		}
		if after != nil && !end.After(*after) {
			continue
		}
		n++
	}
	return n
}

// ExpectedDayPeriods returns the number of half-hours of day (local timezone
// of day) whose end lies after max(after, day start): 48 on a normal day,
// 46 or 50 on a DST change day.
func ExpectedDayPeriods(day time.Time, after *time.Time) int {
	y, m, d := day.Date()
	loc := day.Location()
	start := time.Date(y, m, d, 0, 0, 0, 0, loc)
	next := time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	n := 0
	for s := start; s.Before(next); s = s.Add(HAForecastPeriod) {
		if after != nil && !s.Add(HAForecastPeriod).After(*after) {
			continue
		}
		n++
	}
	return n
}

// sameDay reports whether t (already in day's location) is on day.
func sameDay(t, day time.Time) bool {
	return t.Year() == day.Year() && t.YearDay() == day.YearDay()
}

// singleLineErrors is errors.Join with "; " instead of a newline, so the
// combined error stays on one log line and one MQTT error field (S-026).
type singleLineErrors []error

func (e singleLineErrors) Error() string {
	msgs := make([]string, len(e))
	for i, err := range e {
		msgs[i] = err.Error()
	}
	return strings.Join(msgs, "; ")
}

func (e singleLineErrors) Unwrap() []error { return e }

// joinErrors combines both entity errors; errors.Is still matches each one.
func joinErrors(errs ...error) error {
	return singleLineErrors(errs)
}

// quoteTrunc quotes s with %q semantics after truncating it to
// maxQuotedBytes bytes on a rune boundary (S-026).
func quoteTrunc(s string) string {
	if len(s) <= maxQuotedBytes {
		return strconv.Quote(s)
	}
	cut := maxQuotedBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strconv.Quote(s[:cut]) + "(truncated)"
}

// describeValue renders a scalar JSON value for a log line, bounded.
func describeValue(v any) string {
	switch x := v.(type) {
	case string:
		return quoteTrunc(x)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return "null"
	default:
		return jsonKind(v)
	}
}

// jsonKind names the JSON type of a value decoded by encoding/json.
func jsonKind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "list"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	default:
		return fmt.Sprintf("%T", v)
	}
}
