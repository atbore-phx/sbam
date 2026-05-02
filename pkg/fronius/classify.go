package fronius

import "fmt"

type Decision string

const (
	DecisionBatteryFull    Decision = "battery_full"
	DecisionForecastCharge Decision = "forecast_charge"
	DecisionReserveCharge  Decision = "reserve_charge"
	DecisionIdle           Decision = "idle"
)

func ClassifyDecision(
	pwBatt2charge, pwForecast, pwConsumption, pwBattMax,
	pwBattReserve, pwLwt float64,
	forecastChargeEnabled, battReserveChargeEnabled bool,
) (Decision, string) {
	pwPvNet := pwForecast - pwConsumption
	pwBatt := pwBattMax - pwBatt2charge
	pwNet := pwBatt + pwPvNet
	pwBattReserveNet := pwBatt - pwBattReserve

	switch {
	case pwBatt2charge == 0:
		return DecisionBatteryFull, "Battery is full charged"
	case pwNet < -1*pwLwt && forecastChargeEnabled:
		return DecisionForecastCharge, fmt.Sprintf("Net Power (actual battery power + Net solar power) is not enough: %f Wh", pwNet)
	case pwBattReserveNet < -1*pwLwt && battReserveChargeEnabled:
		return DecisionReserveCharge, fmt.Sprintf("battery %f Wh < reserve %f Wh", pwBatt, pwBattReserve)
	default:
		return DecisionIdle, fmt.Sprintf("Net Power (actual battery power + Net solar power) is enough: %f Wh", pwNet)
	}
}
