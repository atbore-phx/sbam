package fronius

import (
	u "sbam/src/utils"
	"time"
)

func SetFroniusChargeBatteryMode(pw_forecast float64, pw_batt2charge float64, pw_batt_max float64, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, fronius_ip string, fronius_port ...string) (int16, error) {
	p := "502"
	if len(fronius_port) > 0 {
		p = fronius_port[0]
	}
	var ch_pc int16 = 0
	pw_pv_net := pw_forecast - pw_consumption // Net solar power
	pw_batt := pw_batt_max - pw_batt2charge   // actual battery power

	time, err := CheckTimeRange(start_hr, end_hr)
	if err != nil {
		u.Log.Errorln("Error checking time range: %s ", err)
		return ch_pc, err
	}
	switch {
	case !time: // out of the time range => do not charge
		u.Log.Infof("Out time range start_time: %s - end_time: %s", start_hr, end_hr)
	case pw_batt2charge == 0: // battery 100% => do not charge
		u.Log.Info("Battery is full charged")
	case pw_batt < pw_batt_reserve: // battery is less than reserve => charge
		u.Log.Infof("battery %f W < reserve %f W", pw_batt, pw_batt_reserve)
		ch_pc = SetChargePower(pw_batt_max, pw_batt_reserve-pw_batt, max_charge)
	default:
		pw_grid, charge_enabled := ChargeBattery(pw_pv_net, pw_batt)
		if charge_enabled {
			ch_pc = SetChargePower(pw_batt_max, -1*pw_grid, max_charge)
		}
	}

	if ch_pc != 0 {
		err = ForceCharge(fronius_ip, ch_pc, p)
		if err != nil {
			u.Log.Errorln("Error forcing charge: %s ", err)
			return ch_pc, err
		}
	} else {
		err = Setdefaults(fronius_ip, p)
		if err != nil {
			u.Log.Errorln("Error Setting Defaults: %s ", err)
			return ch_pc, err
		}
	}

	return ch_pc, nil
}

func SetChargePower(max float64, load float64, limit float64) int16 {

	return int16(min(load*100/max, limit*100/max))

}

func ChargeBattery(pw_pv_net float64, pw_batt float64) (float64, bool) {
	enabled := false
	pw_grid := pw_batt + pw_pv_net

	if pw_pv_net <= 0 { // net pv power is not enough
		u.Log.Infof("Net Forecast Power is not enough: %f W", pw_pv_net)
		if pw_grid < 0 { // Battery Capacity is not enough => charge the diff
			u.Log.Infof("Battery Capacity is not enough: %f W", pw_batt)
			enabled = true
		} else { // Battery Capacity is enough => do not charge
			u.Log.Infof("Battery Capacity is enough: %f W", pw_batt)
		}
	} else { // net pv power is enough => do not charge
		u.Log.Infof("Net Forecast Power is enough: %f W", pw_pv_net)
	}

	return pw_grid, enabled
}

func CheckTimeRange(start_hr string, end_hr string) (bool, error) {
	now := time.Now()

	layout := "15:04"
	startTime, err := time.Parse(layout, start_hr)
	if err != nil {
		u.Log.Error("Something goes wrong parsing start time")
		return false, err
	}

	endTime, err := time.Parse(layout, end_hr)
	if err != nil {
		u.Log.Error("Something goes wrong parsing end time")
		return false, err
	}

	// Convert the current time to a time.Time value for today's date with the hour and minute set to the parsed start and end times
	startTime = time.Date(now.Year(), now.Month(), now.Day(), startTime.Hour(), startTime.Minute(), 0, 0, now.Location())
	endTime = time.Date(now.Year(), now.Month(), now.Day(), endTime.Hour(), endTime.Minute(), 0, 0, now.Location())

	return (now.After(startTime) || now.Equal(startTime)) && (now.Before(endTime) || now.Equal(endTime)), nil
}
