package fronius

import u "sbam/src/utils"

func SetFroniusChargeBatteryMode(pw_forecast float64, pw_batt2charge float64, pw_batt_max float64, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, fronius_ip string, batt_reserve_charge_enabled bool, pw_lwt float64, pw_upt float64, forecast_charge_enabled bool, fronius_port ...string) (int16, error) {
	p := "502"
	if len(fronius_port) > 0 {
		p = fronius_port[0]
	}
	var ch_pc int16 = 0
	pw_pv_net := pw_forecast - pw_consumption // Net solar power
	pw_batt := pw_batt_max - pw_batt2charge   // actual battery power
	pw_net := pw_batt + pw_pv_net             // net power available (actual battery power + Net solar power)

	decision, reason := ClassifyDecision(
		pw_batt2charge, pw_forecast, pw_consumption, pw_batt_max,
		pw_batt_reserve, pw_lwt,
		forecast_charge_enabled, batt_reserve_charge_enabled,
	)
	switch decision {
	case DecisionBatteryFull:
		u.Log.Info(reason)
	case DecisionForecastCharge:
		u.Log.Info(reason)
		ch_pc = SetChargePower(pw_batt_max, -1*pw_net+pw_upt, max_charge)
	case DecisionReserveCharge:
		u.Log.Info(reason)
		ch_pc = SetChargePower(pw_batt_max, pw_batt_reserve-pw_batt, max_charge)
	default:
		u.Log.Info(reason)
	}

	err = ForceCharge(fronius_ip, ch_pc, p)
	if err != nil {
		u.Log.Errorln("Error forcing charge: %s ", err)
		return ch_pc, err
	}

	return ch_pc, nil
}

func SetChargePower(max float64, load float64, limit float64) int16 {

	return int16(min(load*100/max, limit*100/max))

}
