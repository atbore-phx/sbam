package power

import (
	"fmt"
	"sort"
	"time"
)

// Reserve horizon mode constants.
const (
	// ReserveHorizonUntilSolar sizes the battery reserve to cover the base
	// load from the end of the charge window until the forecast PV output
	// covers that load.
	ReserveHorizonUntilSolar = "until_solar"

	// maxSolarGapSearch bounds how far after the window end the solar gap
	// search looks for a period where PV covers the base load.
	maxSolarGapSearch = 24 * time.Hour

	forecastPeriod = 30 * time.Minute
)

// ValidateReserveHorizon returns s if it names a known reserve horizon mode,
// or an error otherwise.
func ValidateReserveHorizon(s string) (string, error) {
	switch s {
	case ReserveHorizonUntilSolar:
		return s, nil
	default:
		return "", fmt.Errorf("unknown reserve_horizon %q: must be until_solar", s)
	}
}

// SolarGap is the result of SolarGapEnergy.
type SolarGap struct {
	// EnergyWh is the base-load energy not covered by forecast PV between
	// From and CoveredAt.
	EnergyWh float64
	// CoveredAt is the end of the first forecast period, at or after From,
	// whose PV estimate covers the base load. Zero when no such period was
	// found within the search horizon.
	CoveredAt time.Time
	// Covered reports whether such a period was found.
	Covered bool
}

// SolarGapEnergy returns the energy the battery has to supply, at a constant
// baseLoadW, from `from` until the first 30-minute forecast period whose PV
// estimate (summed across all sites) reaches baseLoadW.
//
// Periods are identified by period_end; each covers the 30 minutes before
// it. A period that started before `from` only counts its remaining part.
// When no period covers the load within maxSolarGapSearch, the energy up to
// the last available period is returned with Covered=false.
func SolarGapEnergy(sites []Forecasts, from time.Time, baseLoadW float64) (SolarGap, error) {
	if baseLoadW <= 0 {
		return SolarGap{Covered: true, CoveredAt: from}, nil
	}

	pvByEnd := map[time.Time]float64{}
	for _, site := range sites {
		for _, f := range site.Forecasts {
			end, err := time.Parse(time.RFC3339, f.PeriodEnd)
			if err != nil {
				return SolarGap{}, fmt.Errorf("parse period_end %q: %w", f.PeriodEnd, err)
			}
			pvByEnd[end.UTC()] += f.PVEstimate * 1000 // kW -> W
		}
	}
	ends := make([]time.Time, 0, len(pvByEnd))
	for end := range pvByEnd {
		ends = append(ends, end)
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i].Before(ends[j]) })

	gap := SolarGap{}
	limit := from.Add(maxSolarGapSearch)
	for _, end := range ends {
		if !end.After(from) {
			continue
		}
		if end.After(limit) {
			break
		}
		pvW := pvByEnd[end]
		if pvW >= baseLoadW {
			gap.Covered = true
			gap.CoveredAt = end.Add(-forecastPeriod).In(from.Location())
			if gap.CoveredAt.Before(from) {
				gap.CoveredAt = from
			}
			return gap, nil
		}
		start := end.Add(-forecastPeriod)
		if start.Before(from) {
			start = from
		}
		gap.EnergyWh += (baseLoadW - pvW) * end.Sub(start).Hours()
	}
	return gap, nil
}

// UntilSolarReserve returns the battery reserve in Wh needed to reach the
// time the sun covers the base load: the inverter's minimum SoC share of
// the capacity plus the uncovered base-load energy.
func UntilSolarReserve(capacityWh, minSoCPct float64, gap SolarGap) float64 {
	return capacityWh*minSoCPct/100 + gap.EnergyWh
}
