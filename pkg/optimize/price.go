package optimize

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Price plausibility bounds and limits (design review §Inbound contract,
// S-004, S-009, S-010).
const (
	MinPriceKWh = -5.0
	MaxPriceKWh = 50.0
	PriceMaxAge = 26 * time.Hour

	priceStateRelTolerance = 0.01
	// priceStateAbsTolerance absorbs the sensor's display rounding (3 decimals)
	// for prices close to zero, where 1 % is smaller than the rounding step.
	priceStateAbsTolerance = 0.001
)

// Sentinel errors returned by ParseNordpool and PriceHorizon. Map them to a
// fallback reason with ReasonForPriceError.
var (
	ErrPriceFormat       = errors.New("price format")
	ErrPriceImplausible  = errors.New("implausible price")
	ErrPriceStale        = errors.New("price entity stale")
	ErrPriceMissing      = errors.New("no price for the current hour")
	ErrPriceInconsistent = errors.New("price state inconsistent with entries")
)

// ReasonForPriceError maps a price error to its AC-16 fallback reason code.
// Unknown errors map to ReasonFallbackPriceFormat.
func ReasonForPriceError(err error) Reason {
	switch {
	case errors.Is(err, ErrPriceImplausible):
		return ReasonFallbackImplausibleInput
	case errors.Is(err, ErrPriceStale), errors.Is(err, ErrPriceMissing):
		return ReasonFallbackMissingPrices
	case errors.Is(err, ErrPriceInconsistent):
		return ReasonFallbackPriceInconsistent
	default:
		return ReasonFallbackPriceFormat
	}
}

// ParseNordpool parses the HA Nordpool sensor state and attributes into price
// points per kWh, sorted by start.
//
// Accepted shapes, in order of preference:
//  1. raw_today / raw_tomorrow: [{start, end?, value}], value a number, a
//     numeric string or null (null = unpriced entry);
//  2. today / tomorrow: number arrays anchored at local midnight in loc.
//
// Tomorrow is ignored when tomorrow_valid is false. The entity must be at most
// PriceMaxAge old, the unit must end in /kWh (or /MWh, divided by 1000), each
// day must have 23/24/25 hourly or 92/96/100 quarter-hour entries matching the
// local day length, every value must be finite and in [MinPriceKWh,
// MaxPriceKWh], and the entity state must match the entry covering now within
// 1 %. Meta.TZMismatch is set when the entry offsets differ from loc.
func ParseNordpool(attrs map[string]any, state string, lastUpdated, now time.Time, loc *time.Location) ([]PricePoint, PriceMeta, error) {
	meta := PriceMeta{LastUpdated: lastUpdated}
	if loc == nil {
		return nil, meta, fmt.Errorf("%w: nil location", ErrPriceFormat)
	}
	if attrs == nil {
		return nil, meta, fmt.Errorf("%w: no attributes", ErrPriceFormat)
	}
	if lastUpdated.IsZero() || now.Sub(lastUpdated) > PriceMaxAge {
		return nil, meta, fmt.Errorf("%w: last_updated older than %s", ErrPriceStale, PriceMaxAge)
	}
	unit, currency, scale, err := priceUnit(attrs)
	if err != nil {
		return nil, meta, err
	}
	meta.Unit, meta.Currency = unit, currency
	if cents, ok := attrs["price_in_cents"].(bool); ok && cents {
		return nil, meta, fmt.Errorf("%w: price_in_cents is not supported", ErrPriceFormat)
	}

	points, res, mismatch, err := priceCollect(attrs, scale, now, loc)
	if err != nil {
		return nil, meta, err
	}
	meta.Resolution, meta.TZMismatch = res, mismatch

	if err := priceCheckState(points, state, scale, now); err != nil {
		return nil, meta, err
	}
	return points, meta, nil
}

// priceCollect builds the sorted price points from whichever shape is present.
func priceCollect(attrs map[string]any, scale float64, now time.Time, loc *time.Location) ([]PricePoint, time.Duration, bool, error) {
	tomorrowValid := true
	if b, ok := attrs["tomorrow_valid"].(bool); ok && !b {
		tomorrowValid = false
	}

	var (
		points   []PricePoint
		res      time.Duration
		mismatch bool
	)
	switch {
	case attrs["raw_today"] != nil:
		today, r, mm, err := priceRawDay(attrs["raw_today"], scale, loc, true)
		if err != nil {
			return nil, 0, false, fmt.Errorf("raw_today: %w", err)
		}
		points, res, mismatch = today, r, mm
		if tomorrowValid && attrs["raw_tomorrow"] != nil {
			tomorrow, _, mm2, err := priceRawDay(attrs["raw_tomorrow"], scale, loc, false)
			if err != nil {
				return nil, 0, false, fmt.Errorf("raw_tomorrow: %w", err)
			}
			points = append(points, tomorrow...)
			mismatch = mismatch || mm2
		}
	case attrs["today"] != nil:
		mid := priceMidnight(now, loc, 0)
		today, r, err := priceArrayDay(attrs["today"], mid, priceMidnight(now, loc, 1), scale, true)
		if err != nil {
			return nil, 0, false, fmt.Errorf("today: %w", err)
		}
		points, res = today, r
		if tomorrowValid && attrs["tomorrow"] != nil {
			tomorrow, _, err := priceArrayDay(attrs["tomorrow"], priceMidnight(now, loc, 1), priceMidnight(now, loc, 2), scale, false)
			if err != nil {
				return nil, 0, false, fmt.Errorf("tomorrow: %w", err)
			}
			points = append(points, tomorrow...)
		}
	default:
		return nil, 0, false, fmt.Errorf("%w: neither raw_today nor today present", ErrPriceFormat)
	}

	sort.SliceStable(points, func(i, j int) bool { return points[i].Start.Before(points[j].Start) })
	for i := 1; i < len(points); i++ {
		if points[i].Start.Before(points[i-1].End) {
			return nil, 0, false, fmt.Errorf("%w: overlapping entries at %s", ErrPriceFormat, points[i].Start.Format(time.RFC3339))
		}
	}
	return points, res, mismatch, nil
}

// priceUnit returns the unit, currency and the factor converting to per kWh.
func priceUnit(attrs map[string]any) (string, string, float64, error) {
	unit, _ := attrs["unit_of_measurement"].(string)
	unit = strings.TrimSpace(unit)
	if unit == "" {
		cur, _ := attrs["currency"].(string)
		u, _ := attrs["unit"].(string)
		if cur != "" && u != "" {
			unit = strings.TrimSpace(cur) + "/" + strings.TrimSpace(u)
		}
	}
	lower := strings.ToLower(unit)
	var scale float64
	switch {
	case strings.HasSuffix(lower, "/kwh"):
		scale = 1
	case strings.HasSuffix(lower, "/mwh"):
		scale = 0.001
	default:
		return "", "", 0, fmt.Errorf("%w: unit %q does not end in /kWh or /MWh", ErrPriceFormat, unit)
	}
	currency, _ := attrs["currency"].(string)
	currency = strings.TrimSpace(currency)
	if currency == "" {
		currency = strings.TrimSpace(unit[:len(unit)-len("/kWh")])
	}
	return unit, currency, scale, nil
}

// priceResolution maps a per-day entry count to the entry resolution.
func priceResolution(n int) (time.Duration, bool) {
	switch n {
	case 92, 96, 100:
		return 15 * time.Minute, true
	case 23, 24, 25:
		return time.Hour, true
	default:
		return 0, false
	}
}

// priceMidnight returns local midnight of now's date in loc, plus addDays.
func priceMidnight(now time.Time, loc *time.Location, addDays int) time.Time {
	l := now.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day()+addDays, 0, 0, 0, 0, loc)
}

// priceDayHours returns the number of hours in the local day containing t.
func priceDayHours(t time.Time, loc *time.Location) int {
	l := t.In(loc)
	mid := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	next := time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, loc)
	return int(math.Round(next.Sub(mid).Hours()))
}

// priceCountMatchesDay reports whether n entries fit a day of dayHours hours.
func priceCountMatchesDay(n, dayHours int) bool {
	return n == dayHours || n == 4*dayHours
}

// priceRawDay parses one raw_* list.
func priceRawDay(v any, scale float64, loc *time.Location, required bool) ([]PricePoint, time.Duration, bool, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, 0, false, fmt.Errorf("%w: not a list", ErrPriceFormat)
	}
	if len(list) == 0 && !required {
		return nil, 0, false, nil
	}
	res, ok := priceResolution(len(list))
	if !ok {
		return nil, 0, false, fmt.Errorf("%w: %d entries per day", ErrPriceFormat, len(list))
	}
	points := make([]PricePoint, 0, len(list))
	mismatch := false
	for i, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, 0, false, fmt.Errorf("%w: entry %d is not an object", ErrPriceFormat, i)
		}
		start, err := priceTime(entry["start"])
		if err != nil {
			return nil, 0, false, fmt.Errorf("entry %d start: %w", i, err)
		}
		end := start.Add(res)
		if entry["end"] != nil {
			e, err := priceTime(entry["end"])
			if err != nil {
				return nil, 0, false, fmt.Errorf("entry %d end: %w", i, err)
			}
			if !e.Equal(end) {
				return nil, 0, false, fmt.Errorf("%w: entry %d spans %s, want %s", ErrPriceFormat, i, e.Sub(start), res)
			}
		}
		value, valid, err := priceValue(entry["value"], scale)
		if err != nil {
			return nil, 0, false, fmt.Errorf("entry %d value: %w", i, err)
		}
		_, off := start.Zone()
		if _, locOff := start.In(loc).Zone(); off != locOff {
			mismatch = true
		}
		points = append(points, PricePoint{Start: start, End: end, Value: value, Valid: valid})
	}
	// The DST day-length check needs the entries' own zone to agree with loc;
	// on a mismatch the local date of the first entry is not trustworthy.
	if !mismatch && !priceCountMatchesDay(len(list), priceDayHours(points[0].Start, loc)) {
		return nil, 0, false, fmt.Errorf("%w: %d entries do not match the local day length", ErrPriceFormat, len(list))
	}
	return points, res, mismatch, nil
}

// priceArrayDay parses one today/tomorrow number array anchored at mid.
func priceArrayDay(v any, mid, nextMid time.Time, scale float64, required bool) ([]PricePoint, time.Duration, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, 0, fmt.Errorf("%w: not a list", ErrPriceFormat)
	}
	if len(list) == 0 && !required {
		return nil, 0, nil
	}
	res, ok := priceResolution(len(list))
	if !ok {
		return nil, 0, fmt.Errorf("%w: %d entries per day", ErrPriceFormat, len(list))
	}
	dayHours := int(math.Round(nextMid.Sub(mid).Hours()))
	if !priceCountMatchesDay(len(list), dayHours) {
		return nil, 0, fmt.Errorf("%w: %d entries do not match the local day length", ErrPriceFormat, len(list))
	}
	points := make([]PricePoint, 0, len(list))
	for i, item := range list {
		value, valid, err := priceValue(item, scale)
		if err != nil {
			return nil, 0, fmt.Errorf("entry %d value: %w", i, err)
		}
		start := mid.Add(time.Duration(i) * res)
		points = append(points, PricePoint{Start: start, End: start.Add(res), Value: value, Valid: valid})
	}
	return points, res, nil
}

// priceTime parses an entry timestamp (ISO 8601 with offset).
func priceTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t, nil
	case string:
		s := strings.TrimSpace(t)
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05.999999999Z07:00"} {
			if parsed, err := time.Parse(layout, s); err == nil {
				return parsed, nil
			}
		}
		return time.Time{}, fmt.Errorf("%w: timestamp %q", ErrPriceFormat, s)
	default:
		return time.Time{}, fmt.Errorf("%w: timestamp of type %T", ErrPriceFormat, v)
	}
}

// priceValue parses one entry value. nil (and an empty string) is an unpriced
// entry; non-finite or out-of-bounds values are implausible.
func priceValue(v any, scale float64) (float64, bool, error) {
	var f float64
	switch x := v.(type) {
	case nil:
		return 0, false, nil
	case float64:
		f = x
	case float32:
		f = float64(x)
	case int:
		f = float64(x)
	case int64:
		f = float64(x)
	case json.Number:
		return priceValue(x.String(), scale)
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false, nil
		}
		parsed, err := strconv.ParseFloat(s, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return 0, false, fmt.Errorf("%w: value %q", ErrPriceFormat, s)
		}
		f = parsed
	default:
		return 0, false, fmt.Errorf("%w: value of type %T", ErrPriceFormat, v)
	}
	f *= scale
	if math.IsNaN(f) || math.IsInf(f, 0) || f < MinPriceKWh || f > MaxPriceKWh {
		return 0, false, fmt.Errorf("%w: value outside [%g, %g] per kWh", ErrPriceImplausible, MinPriceKWh, MaxPriceKWh)
	}
	return f, true, nil
}

// priceCheckState compares the entity state with the entry covering now. The
// state may be the current entry or the current hour's mean. The previous
// entry and the previous hour's mean are accepted too: the hourly run starts
// exactly at xx:00:00, and the Nordpool integration updates its state a few
// seconds after each boundary, so at run time the state usually still shows
// the previous quarter or hour. When no valid entry covers now, the check is
// skipped: the current hour is then unpriced and PriceHorizon reports
// ErrPriceMissing.
func priceCheckState(points []PricePoint, state string, scale float64, now time.Time) error {
	var current *PricePoint
	for i := range points {
		if !now.Before(points[i].Start) && now.Before(points[i].End) {
			current = &points[i]
			break
		}
	}
	if current == nil || !current.Valid {
		return nil
	}
	s, ok := ParseHAFloat(state)
	if !ok {
		return fmt.Errorf("%w: state is not numeric", ErrPriceInconsistent)
	}
	s *= scale
	if priceClose(s, current.Value) {
		return nil
	}
	hour := HourlyPrices(points, now.Truncate(time.Hour), 1)
	if len(hour) == 1 && priceClose(s, hour[0].PriceInclV) {
		return nil
	}
	for i := range points {
		if points[i].Valid && points[i].End.Equal(current.Start) && priceClose(s, points[i].Value) {
			return nil
		}
	}
	prevHour := HourlyPrices(points, now.Truncate(time.Hour).Add(-time.Hour), 1)
	if len(prevHour) == 1 && priceClose(s, prevHour[0].PriceInclV) {
		return nil
	}
	return fmt.Errorf("%w: state %.4f vs entry %.4f", ErrPriceInconsistent, s, current.Value)
}

func priceClose(a, b float64) bool {
	tol := math.Max(priceStateRelTolerance*math.Abs(b), priceStateAbsTolerance)
	return math.Abs(a-b) <= tol
}

// HourlyPrices returns the hourly mean price for consecutive absolute hours
// from slot0Start, at most maxHours. An hour is priced only when valid entries
// cover it completely (4 × 15 min or 1 × 60 min); the result stops at the
// first unpriced hour. An empty result means the current hour is unpriced.
func HourlyPrices(points []PricePoint, slot0Start time.Time, maxHours int) []HourPrice {
	var out []HourPrice
	for k := 0; k < maxHours; k++ {
		hs := slot0Start.Add(time.Duration(k) * time.Hour)
		mean, ok := priceHourMean(points, hs, hs.Add(time.Hour))
		if !ok {
			break
		}
		out = append(out, HourPrice{Start: hs, PriceInclV: mean})
	}
	return out
}

// priceHourMean returns the arithmetic mean of the entries inside [hs, he)
// when they are all valid and cover the hour exactly.
func priceHourMean(points []PricePoint, hs, he time.Time) (float64, bool) {
	var (
		covered time.Duration
		sum     float64
		n       int
	)
	for _, p := range points {
		if !p.Start.Before(he) || !p.End.After(hs) {
			continue
		}
		if !p.Valid || p.Start.Before(hs) || p.End.After(he) {
			return 0, false
		}
		covered += p.End.Sub(p.Start)
		sum += p.Value
		n++
	}
	if n == 0 || covered != he.Sub(hs) {
		return 0, false
	}
	return sum / float64(n), true
}

// PriceHorizon is HourlyPrices plus the AC-16 rule: when the current hour is
// unpriced it returns ErrPriceMissing.
func PriceHorizon(points []PricePoint, slot0Start time.Time, maxHours int) ([]HourPrice, error) {
	hp := HourlyPrices(points, slot0Start, maxHours)
	if len(hp) == 0 {
		return nil, ErrPriceMissing
	}
	return hp, nil
}

// BuySell derives the buy and sell price for one hour (AC-6):
// buy = spot incl. VAT + surcharge[hod]; sell = spot / (1 + vat) − margin.
// The sell price may be negative. hourOfDay is taken modulo 24.
func BuySell(priceInclVAT float64, hourOfDay int, surcharge [24]float64, vatRate, sellMargin float64) (buy, sell float64) {
	hod := ((hourOfDay % 24) + 24) % 24
	buy = priceInclVAT + surcharge[hod]
	sell = priceInclVAT/(1+vatRate) - sellMargin
	return buy, sell
}
