package fronius

import "fmt"

type Decision string

const (
	decisionBatteryFull    Decision = "battery_full"
	decisionForecastCharge Decision = "forecast_charge"
	decisionReserveCharge  Decision = "reserve_charge"
	DecisionIdle           Decision = "idle"
	DecisionSkip           Decision = "skip"
)

func (d Decision) String() string {
	return string(d)
}

type Reason string

const (
	reasonBatteryFull      Reason = "Battery is full charged"
	reasonForecastCharge   Reason = "Net Power (actual battery power + Net solar power) is not enough"
	reasonReserveCharge    Reason = "Battery charge is below reserve threshold"
	reasonIdle             Reason = "Net Power (actual battery power + Net solar power) is enough"
	reasonForecastDisabled Reason = "Forecast-based charging is disabled (forecast_horizon=off or forecast retrieval failed)"
	reasonSkip             Reason = "unexpected power state"
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

// classifyDecision computes the power-derived decision and a PowerState
// snapshot. It intentionally does not compute battery SoC here — the
// storage package is the authoritative source for SoC and schedule will
// supply that value for telemetry.
func classifyDecision(
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
		return decisionBatteryFull, reasonBatteryFull, pw, nil
	case pw.Net < -1*pwLwt && forecastChargeEnabled:
		return decisionForecastCharge, reasonForecastCharge, pw, nil
	case pw.BattReserveNet < -1*pwLwt && battReserveChargeEnabled:
		return decisionReserveCharge, reasonReserveCharge, pw, nil
	case pw.Net >= -1*pwLwt:
		return DecisionIdle, reasonIdle, pw, nil
	case !forecastChargeEnabled:
		return DecisionIdle, reasonForecastDisabled, pw, nil
	default:
		return DecisionSkip, reasonSkip, pw, fmt.Errorf("unexpected power state: %+v", pw)
	}
}
