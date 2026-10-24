package optimize

import (
	"math"
	"time"
)

// Defaults for the solver dimensions (ADR-0002, design review Q1). A zero
// Params.MaxHours or Params.Steps means "use the default".
const (
	DefaultMaxHours = 36
	DefaultSteps    = 200
)

// Action is one of the five inverter actions the plan can choose (AC-14).
type Action string

// Inverter actions (AC-14, ADR-0003).
const (
	// ActionCharge forces charging at a rate, from grid and PV.
	ActionCharge Action = "charge"
	// ActionHold blocks discharge; PV surplus may still charge the battery.
	ActionHold Action = "hold"
	// ActionStandby blocks charge and discharge; PV surplus is exported.
	ActionStandby Action = "standby"
	// ActionDischarge forces discharge at a rate, to the house and the grid.
	ActionDischarge Action = "discharge"
	// ActionSelfConsume is the inverter default: absorb surplus, cover deficit.
	ActionSelfConsume Action = "self_consume"
)

// Hour is one slot of the planning grid: the absolute instant
// [Start, End) plus the expected energy flows and prices for it.
type Hour struct {
	Start     time.Time
	End       time.Time
	HourOfDay int // Start.In(loc).Hour(); indexes surcharge and profile
	// Fraction is the part of the slot still ahead of now, in (0, 1]. Only
	// slot 0 can be < 1. It already scales ConsumptionWh, PVWh and the rate
	// limits applied by the solver.
	Fraction      float64
	SpotInclVAT   float64 // hourly mean spot price incl. VAT, per kWh
	Buy           float64 // buy price per kWh (AC-6)
	Sell          float64 // sell price per kWh (AC-6), may be negative
	ConsumptionWh float64 // expected household consumption in the slot
	PVWh          float64 // expected PV production in the slot
}

// Grid is the validated planning horizon.
type Grid struct {
	Hours []Hour
	// TerminalPrice values energy left at the end of the horizon, per kWh:
	// η × mean(buy) (AC-10).
	TerminalPrice float64
	// Dropped is the number of priced slots beyond the horizon cap (Q1).
	Dropped int
}

// Battery is the usable battery state. Energies are measured above the
// minimum SoC.
type Battery struct {
	CapacityWh float64
	SocPct     float64
	MinSocPct  float64
	E0Wh       float64 // usable starting energy, clamp((soc-min)/100·cap, 0, Emax) (AC-7)
	EmaxWh     float64 // usable capacity, (100-min)/100·cap
}

// Params are the optimizer settings (US-2).
type Params struct {
	Efficiency    float64 // round-trip efficiency η in (0, 1], applied on the charge side
	MaxChargeW    float64
	MaxDischargeW float64
	MinSocPct     float64
	VATRate       float64
	SellMargin    float64     // per kWh incl. VAT, subtracted from the ex-VAT spot
	Surcharge     [24]float64 // per kWh incl. VAT, indexed by local hour of day
	MinGain       float64     // per hour: a charge or discharge must beat self_consume by at least this, hold/standby by a tenth (AC-11b); 0 disables
	WearCost      float64     // per kWh a charge or discharge moves beyond self_consume (AC-11b); 0 disables
	MaxHours      int         // horizon cap; 0 means DefaultMaxHours
	Steps         int         // SoC discretization steps; 0 means DefaultSteps
}

// PlanHour is one slot of a solved plan.
type PlanHour struct {
	Start         time.Time
	End           time.Time
	HourOfDay     int
	Fraction      float64
	Action        Action
	RateW         float64 // battery power for charge/discharge, rounded to 1 W; 0 otherwise
	StartWh       float64 // usable energy at slot start
	EndWh         float64 // usable energy at slot end
	SocEndPct     float64
	Buy           float64
	Sell          float64
	ConsumptionWh float64
	PVWh          float64
	GridWh        float64 // net grid flow: > 0 import, < 0 export
	Cost          float64 // GridWh priced at Buy (import) or Sell (export), /1000
}

// Plan is the minimum-expected-cost battery plan over the grid.
type Plan struct {
	Hours []PlanHour
	// ExpectedCost is Σ Cost − TerminalValue.
	ExpectedCost float64
	// TerminalValue is TerminalPrice × EndWh(last)/1000.
	TerminalValue float64
	StartSocPct   float64
	StepWh        float64 // SoC discretization step Δ
	// Reason is the action code for slot 0 (UX §4.4, ADR-0002).
	Reason Reason
}

func (p Params) maxHours() int {
	if p.MaxHours == 0 {
		return DefaultMaxHours
	}
	return p.MaxHours
}

func (p Params) steps() int {
	if p.Steps == 0 {
		return DefaultSteps
	}
	return p.Steps
}

// ChargeEfficiency and DischargeEfficiency split the round-trip efficiency η
// evenly (AC-11d): storing ΔE draws ΔE/√η, delivering X takes X/√η from the
// battery, so the round trip stays η.
func ChargeEfficiency(roundTrip float64) float64 { return math.Sqrt(roundTrip) }

// DischargeEfficiency is the fraction of released battery energy that reaches
// the house or grid. See ChargeEfficiency.
func DischargeEfficiency(roundTrip float64) float64 { return math.Sqrt(roundTrip) }
