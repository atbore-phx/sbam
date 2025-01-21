package fronius

import (
	u "sbam/src/utils"
)

func SetFroniusChargeBatteryMode(pw_forecast float64, pw_batt2charge float64, pw_batt_max float64, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, fronius_ip string, batt_reserve_charge_enabled bool, pw_lwt float64, pw_upt float64, fronius_port ...string) (int16, error) {
	p := "502"
	if len(fronius_port) > 0 {
		p = fronius_port[0]
	}
	var ch_pc int16 = 0
	pw_pv_net := pw_forecast - pw_consumption        // Net solar power
	pw_batt := pw_batt_max - pw_batt2charge          // actual battery power
	pw_net := pw_batt + pw_pv_net                    // net power available (actual battery power + Net solar power)
	pw_batt_reserve_net := pw_batt - pw_batt_reserve // net reserve power

	switch {
	case pw_batt2charge == 0: // battery 100% => do not charge
		u.Log.Info("Battery is full charged")
	case pw_net < -1*pw_lwt: // net power is not enough => charge
		u.Log.Infof("Net Power (actual battery power + Net solar power) is not enough: %f Wh", pw_net)
		ch_pc = SetChargePower(pw_batt_max, -1*pw_net+pw_upt, max_charge)
	case pw_batt_reserve_net < -1*pw_lwt && batt_reserve_charge_enabled: // battery is less than reserve and reserve battery is time enabled => charge
		u.Log.Infof("battery %f Wh < reserve %f Wh", pw_batt+pw_upt, pw_batt_reserve)
		ch_pc = SetChargePower(pw_batt_max, pw_batt_reserve-pw_batt, max_charge)
	default: // net power is enough => do not charge
		u.Log.Infof("Net Power (actual battery power + Net solar power) is enough: %f Wh", pw_net)
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
