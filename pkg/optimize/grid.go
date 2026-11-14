package optimize

import (
	"errors"
	"fmt"
	"time"
)

// GridInput holds the already-derived inputs of BuildGrid.
type GridInput struct {
	// Prices are hourly spot prices incl. VAT keyed by absolute hour start.
	// Order does not matter; slots are matched by instant.
	Prices []HourPrice
	// Profile is the expected consumption per local hour of day, in Wh.
	Profile Profile
	// PV returns the expected full-slot PV energy (Wh) per slot, one value per
	// slot, plus any degraded-input reasons. BuildGrid scales slot 0 by its
	// Fraction. A nil PV means 0 Wh everywhere.
	PV func(slots []Hour) ([]float64, []Reason)
	// SocPct and CapacityWh come from the Fronius Solar API.
	SocPct     float64
	CapacityWh float64
	Params     Params
	Now        time.Time
	Loc        *time.Location
}

// HourSlots builds the horizon of absolute one-hour slots starting at the
// wall-clock hour that contains now (S-008). The horizon runs to the first
// unpriced hour and is capped at maxHours (0 means DefaultMaxHours); it
// returns the slots and the number of priced slots dropped by the cap.
// Slots carry Start, End, HourOfDay, Fraction and SpotInclVAT only.
func HourSlots(now time.Time, loc *time.Location, priced []HourPrice, maxHours int) ([]Hour, int) {
	if loc == nil {
		loc = time.Local
	}
	if maxHours <= 0 {
		maxHours = DefaultMaxHours
	}
	local := now.In(loc)
	t0 := local.Add(-time.Duration(local.Minute())*time.Minute -
		time.Duration(local.Second())*time.Second -
		time.Duration(local.Nanosecond()))

	var slots []Hour
	dropped := 0
	for k := 0; k <= len(priced); k++ {
		start := t0.Add(time.Duration(k) * time.Hour)
		price, ok := priceAt(priced, start)
		if !ok {
			break
		}
		if len(slots) == maxHours {
			dropped++
			continue
		}
		end := start.Add(time.Hour)
		frac := 1.0
		if k == 0 {
			frac = float64(end.Sub(now)) / float64(time.Hour)
		}
		slots = append(slots, Hour{
			Start:       start,
			End:         end,
			HourOfDay:   start.In(loc).Hour(),
			Fraction:    frac,
			SpotInclVAT: price,
		})
	}
	return slots, dropped
}

func priceAt(priced []HourPrice, start time.Time) (float64, bool) {
	for _, p := range priced {
		if p.Start.Equal(start) {
			return p.PriceInclV, true
		}
	}
	return 0, false
}

// NewBattery derives the usable battery state (AC-7): Emax = (100−min)/100·cap
// and E0 = clamp((soc−min)/100·cap, 0, Emax).
func NewBattery(socPct, capacityWh, minSocPct float64) (Battery, error) {
	if err := ValidateBattery(socPct, capacityWh); err != nil {
		return Battery{}, err
	}
	if !finiteAll(minSocPct) || minSocPct < 0 || minSocPct >= 100 {
		return Battery{}, fmt.Errorf("%w: min SoC %v not in [0, 100)", ErrInvalidBattery, minSocPct)
	}
	emax := (100 - minSocPct) / 100 * capacityWh
	e0 := (socPct - minSocPct) / 100 * capacityWh
	if e0 < 0 {
		e0 = 0
	}
	if e0 > emax {
		e0 = emax
	}
	return Battery{
		CapacityWh: capacityWh,
		SocPct:     socPct,
		MinSocPct:  minSocPct,
		E0Wh:       e0,
		EmaxWh:     emax,
	}, nil
}

// BuildGrid builds the validated planning grid and battery state from
// already-derived inputs. It returns ErrNoPricedHours when the current hour
// has no price, ErrInvalidParams / ErrInvalidBattery / ErrInvalidGrid on
// invalid input, and the degraded-input reasons reported by the PV func.
func BuildGrid(in GridInput) (Grid, Battery, []Reason, error) {
	if in.Loc == nil {
		return Grid{}, Battery{}, nil, errors.New("build grid: nil location")
	}
	if err := ValidateParams(in.Params); err != nil {
		return Grid{}, Battery{}, nil, fmt.Errorf("build grid: %w", err)
	}
	b, err := NewBattery(in.SocPct, in.CapacityWh, in.Params.MinSocPct)
	if err != nil {
		return Grid{}, Battery{}, nil, fmt.Errorf("build grid: %w", err)
	}
	slots, dropped := HourSlots(in.Now, in.Loc, in.Prices, in.Params.maxHours())
	if len(slots) == 0 {
		return Grid{}, Battery{}, nil, fmt.Errorf("build grid: %w", ErrNoPricedHours)
	}

	pv := make([]float64, len(slots))
	var reasons []Reason
	if in.PV != nil {
		got, rs := in.PV(slots)
		if len(got) != len(slots) {
			return Grid{}, Battery{}, nil, fmt.Errorf("build grid: %w: %d PV values for %d slots", ErrInvalidGrid, len(got), len(slots))
		}
		copy(pv, got)
		reasons = rs
	}

	sumBuy := 0.0
	for k := range slots {
		h := &slots[k]
		h.Buy, h.Sell = BuySell(h.SpotInclVAT, h.HourOfDay, in.Params.Surcharge, in.Params.VATRate, in.Params.SellMargin)
		h.ConsumptionWh = in.Profile[h.HourOfDay] * h.Fraction
		h.PVWh = pv[k] * h.Fraction
		sumBuy += h.Buy
	}
	g := Grid{
		Hours:         slots,
		TerminalPrice: in.Params.Efficiency * sumBuy / float64(len(slots)),
		Dropped:       dropped,
	}
	if err := ValidateGrid(g); err != nil {
		return Grid{}, Battery{}, nil, fmt.Errorf("build grid: %w", err)
	}
	return g, b, reasons, nil
}
