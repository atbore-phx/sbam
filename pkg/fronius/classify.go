package fronius

import "fmt"

type Decision string

const (
	DecisionBatteryFull    Decision = "battery_full"
	DecisionForecastCharge Decision = "forecast_charge"
	DecisionReserveCharge  Decision = "reserve_charge"
	DecisionIdle           Decision = "idle"
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

func ClassifyDecision(
	pwBatt2charge, pwForecast, pwConsumption, pwBattMax,
	pwBattReserve, pwLwt float64,
	forecastChargeEnabled, battReserveChargeEnabled bool,
) (Decision, string, PowerState) {
	pw := PowerState{
		PvNet: pwForecast - pwConsumption,
		Batt:  pwBattMax - pwBatt2charge,
	}
	pw.Net = pw.Batt + pw.PvNet
	pw.BattReserveNet = pw.Batt - pwBattReserve

	switch {
	case pwBatt2charge == 0:
		return DecisionBatteryFull, "Battery is full charged", pw
	case pw.Net < -1*pwLwt && forecastChargeEnabled:
		return DecisionForecastCharge, fmt.Sprintf("Net Power (actual battery power + Net solar power) is not enough: %f Wh", pw.Net), pw
	case pw.BattReserveNet < -1*pwLwt && battReserveChargeEnabled:
		return DecisionReserveCharge, fmt.Sprintf("battery %f Wh < reserve %f Wh", pw.Batt, pwBattReserve), pw
	default:
		return DecisionIdle, fmt.Sprintf("Net Power (actual battery power + Net solar power) is enough: %f Wh", pw.Net), pw
	}
}
