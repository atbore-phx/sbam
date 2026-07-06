package fronius

import u "sbam/src/utils"

func SetFroniusChargeBatteryMode(pw_forecast float64, pw_batt2charge float64, pw_batt_max float64, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, fronius_ip string, batt_reserve_charge_enabled bool, pw_lwt float64, pw_upt float64, forecast_charge_enabled bool, fronius_port ...string) (int16, Decision, Reason, PowerState, error) {
	p := "502"
	if len(fronius_port) > 0 {
		p = fronius_port[0]
	}
	var ch_pc int16 = 0

	decision, reason, pw, cerr := classifyDecision(
		pw_batt2charge, pw_forecast, pw_consumption, pw_batt_max,
		pw_batt_reserve, pw_lwt,
		forecast_charge_enabled, batt_reserve_charge_enabled,
	)
	u.Log.Infof("Decision: %s - %s", decision, reason)
	u.Log.Infof("Net Power: %.2f Wh", pw.Net)
	u.Log.Infof("Battery: %.2f Wh, Reserve: %.2f Wh", pw.Batt, pw_batt_reserve)

	if cerr != nil {
		u.Log.Errorf("Classifier error: %s - checking inverter status before resetting defaults", cerr)
		storCtlVal, readErr := ReadModbusRegister(fronius_ip, StorCtl_Mod, p)
		if readErr == nil && storCtlVal == 0 {
			u.Log.Info("Inverter is not force-charging (StorCtl_Mod=0), skipping defaults write")
			return 0, decision, reason, pw, nil
		}
		modbusErr = ForceCharge(fronius_ip, 0, p)
		if modbusErr != nil {
			u.Log.Errorf("Error setting defaults after classifier error: %s", modbusErr)
			return 0, decision, reason, pw, modbusErr
		}
		return 0, decision, reason, pw, nil
	}

	switch decision {
	case decisionForecastCharge:
		ch_pc = SetChargePower(pw_batt_max, -1*pw.Net+pw_upt, max_charge)
	case decisionReserveCharge:
		ch_pc = SetChargePower(pw_batt_max, pw_batt_reserve-pw.Batt, max_charge)
	}

	modbusErr = ForceCharge(fronius_ip, ch_pc, p)
	if modbusErr != nil {
		u.Log.Errorln("Error forcing charge: %s ", modbusErr)
		return ch_pc, decision, reason, pw, modbusErr
	}

	return ch_pc, decision, reason, pw, nil
}

func SetChargePower(max float64, load float64, limit float64) int16 {

	return int16(min(load*100/max, limit*100/max))

}
