package fronius

import "fmt"

type Decision string

const (
	DecisionBatteryFull    Decision = "battery_full"
	DecisionForecastCharge Decision = "forecast_charge"
	DecisionReserveCharge  Decision = "reserve_charge"
	DecisionIdle           Decision = "idle"
	DecisionSkip           Decision = "skip"
)

func (d Decision) String() string {
	return string(d)
}

type Reason string

const (
	ReasonBatteryFull    Reason = "Battery is full charged"
	ReasonForecastCharge Reason = "Net Power (actual battery power + Net solar power) is not enough"
	ReasonReserveCharge  Reason = "Battery charge is below reserve threshold"
	ReasonIdle           Reason = "Net Power (actual battery power + Net solar power) is enough"
	ReasonSkip           Reason = "unexpected power state"
)

func (r Reason) String() string {
	return string(r)
}

type PowerState struct {
	PvNet          float64
	Batt           float64
	Net            float64
	BattReserveNet float64
}

// ClassifyDecision computes the power-derived decision and a PowerState
// snapshot. It intentionally does not compute battery SoC here — the
// storage package is the authoritative source for SoC and schedule will
// supply that value for telemetry.
func ClassifyDecision(
	pwBatt2charge, pwForecast, pwConsumption, pwBattMax,
	pwBattReserve, pwLwt float64,
	forecastChargeEnabled, battReserveChargeEnabled bool,
) (Decision, Reason, PowerState, error) {
	pw := PowerState{
		PvNet: pwForecast - pwConsumption,
		Batt:  pwBattMax - pwBatt2charge,
	}
	pw.Net = pw.Batt + pw.PvNet
	pw.BattReserveNet = pw.Batt - pwBattReserve

	// Do not compute SoC here; storage provides the authoritative SoC.

	switch {
	case pwBatt2charge == 0:
		return DecisionBatteryFull, ReasonBatteryFull, pw, nil
	case pw.Net < -1*pwLwt && forecastChargeEnabled:
		return DecisionForecastCharge, ReasonForecastCharge, pw, nil
	case pw.BattReserveNet < -1*pwLwt && battReserveChargeEnabled:
		return DecisionReserveCharge, ReasonReserveCharge, pw, nil
	case pw.Net >= -1*pwLwt:
		return DecisionIdle, ReasonIdle, pw, nil
	default:
		return DecisionSkip, ReasonSkip, pw, fmt.Errorf("unexpected power state: %+v", pw)
	}
}
