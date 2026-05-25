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
) (Decision, string, PowerState, error) {
	pw := PowerState{
		PvNet: pwForecast - pwConsumption,
		Batt:  pwBattMax - pwBatt2charge,
	}
	pw.Net = pw.Batt + pw.PvNet
	pw.BattReserveNet = pw.Batt - pwBattReserve

	// Do not compute SoC here; storage provides the authoritative SoC.

	switch {
	case pwBatt2charge == 0:
		return DecisionBatteryFull, "Battery is full charged", pw, nil
	case pw.Net < -1*pwLwt && forecastChargeEnabled:
		return DecisionForecastCharge, "Net Power (actual battery power + Net solar power) is not enough", pw, nil
	case pw.BattReserveNet < -1*pwLwt && battReserveChargeEnabled:
		return DecisionReserveCharge, "Battery charge is below reserve threshold", pw, nil
	case pw.Net >= -1*pwLwt:
		return DecisionIdle, "Net Power (actual battery power + Net solar power) is enough", pw, nil
	default:
		reason := fmt.Sprintf("unexpected power state: %+v", pw)
		return DecisionSkip, reason, pw, fmt.Errorf("%s", reason)
	}
}
