package optimize

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultPVPeriod is the Solcast period length used when `period` is empty.
const DefaultPVPeriod = 30 * time.Minute

// ErrPVPeriod is returned for an unparseable or invalid Solcast period.
var ErrPVPeriod = errors.New("invalid solcast period")

// BucketPV returns the expected PV energy in Wh for each one-hour slot
// starting at slots[k] (AC-4). Each period [End−Period, End) contributes
// PVEstimateKW·1000·overlap_h to every slot it overlaps, so periods straddling
// a slot boundary are split by overlap and multiple forecasts (URLs) are
// summed. Periods with a non-finite or negative estimate, or a non-positive
// period, are dropped. A slot not fully covered by valid periods adds
// ReasonPVForecastMissing; its uncovered part counts as 0 Wh.
func BucketPV(periods []PVPeriod, slots []time.Time) ([]float64, []Reason) {
	if len(slots) == 0 {
		return nil, nil
	}
	out := make([]float64, len(slots))
	cover := make([][]pvSpan, len(slots))
	for _, p := range periods {
		if !pvValid(p) {
			continue
		}
		ps := p.End.Add(-p.Period)
		for k, s := range slots {
			e := s.Add(time.Hour)
			from, to := pvMaxTime(ps, s), pvMinTime(p.End, e)
			if !to.After(from) {
				continue
			}
			out[k] += p.PVEstimateKW * 1000 * to.Sub(from).Hours()
			cover[k] = append(cover[k], pvSpan{from, to})
		}
	}
	for k := range slots {
		if pvUnion(cover[k]) < time.Hour {
			return out, []Reason{ReasonPVForecastMissing}
		}
	}
	return out, nil
}

// PVPeriodsFromSolcast converts one Solcast forecast entry into a PVPeriod.
// periodEnd is the UTC `period_end` (RFC 3339, up to 9 fractional digits);
// period is an ISO 8601 duration such as "PT30M" (empty ⇒ 30 min).
func PVPeriodsFromSolcast(periodEnd string, period string, pvEstimateKW float64) (PVPeriod, error) {
	end, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(periodEnd))
	if err != nil {
		return PVPeriod{}, fmt.Errorf("solcast period_end %q: %w", periodEnd, err)
	}
	d := DefaultPVPeriod
	if strings.TrimSpace(period) != "" {
		d, err = pvParseISODuration(period)
		if err != nil {
			return PVPeriod{}, err
		}
	}
	if math.IsNaN(pvEstimateKW) || math.IsInf(pvEstimateKW, 0) || pvEstimateKW < 0 {
		return PVPeriod{}, fmt.Errorf("solcast pv_estimate must be finite and >= 0")
	}
	return PVPeriod{End: end.UTC(), Period: d, PVEstimateKW: pvEstimateKW}, nil
}

// pvParseISODuration parses the time part of an ISO 8601 duration
// (PTnHnMnS, S may be fractional). The result must be in (0, 24h].
func pvParseISODuration(s string) (time.Duration, error) {
	str := strings.ToUpper(strings.TrimSpace(s))
	if !strings.HasPrefix(str, "PT") || len(str) == 2 {
		return 0, fmt.Errorf("%w: %q", ErrPVPeriod, s)
	}
	rest := str[2:]
	var total time.Duration
	seen := ""
	for rest != "" {
		i := strings.IndexAny(rest, "HMS")
		if i <= 0 {
			return 0, fmt.Errorf("%w: %q", ErrPVPeriod, s)
		}
		unit := rest[i : i+1]
		if strings.Contains(seen, unit) || (seen != "" && strings.Index("HMS", unit) <= strings.Index("HMS", seen[len(seen)-1:])) {
			return 0, fmt.Errorf("%w: %q", ErrPVPeriod, s)
		}
		seen += unit
		v, err := strconv.ParseFloat(rest[:i], 64)
		if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) || (unit != "S" && v != math.Trunc(v)) {
			return 0, fmt.Errorf("%w: %q", ErrPVPeriod, s)
		}
		switch unit {
		case "H":
			total += time.Duration(v) * time.Hour
		case "M":
			total += time.Duration(v) * time.Minute
		case "S":
			total += time.Duration(v * float64(time.Second))
		}
		rest = rest[i+1:]
	}
	if total <= 0 || total > 24*time.Hour {
		return 0, fmt.Errorf("%w: %q", ErrPVPeriod, s)
	}
	return total, nil
}

func pvValid(p PVPeriod) bool {
	return p.Period > 0 && !math.IsNaN(p.PVEstimateKW) && !math.IsInf(p.PVEstimateKW, 0) && p.PVEstimateKW >= 0
}

type pvSpan struct{ from, to time.Time }

// pvUnion returns the total duration covered by the union of spans.
func pvUnion(spans []pvSpan) time.Duration {
	if len(spans) == 0 {
		return 0
	}
	sorted := append([]pvSpan(nil), spans...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].from.Before(sorted[j].from) })
	var total time.Duration
	cur := sorted[0]
	for _, sp := range sorted[1:] {
		if sp.from.After(cur.to) {
			total += cur.to.Sub(cur.from)
			cur = sp
			continue
		}
		if sp.to.After(cur.to) {
			cur.to = sp.to
		}
	}
	return total + cur.to.Sub(cur.from)
}

func pvMaxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func pvMinTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
