package fronius

import (
	u "ha-fronius-bm/src/utils"
	"time"
)

func SetFroniusChargeBatteryMode(pw_forecast float64, pw_batt2charge float64, pw_consumption float64, max_charge int, start_hr string, end_hr string, fronius_ip string) (int16, error) {
	var ch_pc int16 = 0
	time, _ := CheckTimeRange(start_hr, end_hr)

	if !time { // out of the time range => do not charge
		u.Log.Infof("Out time range start_time: %s - end_time: %s", start_hr, end_hr)
		Setdefaults(fronius_ip)
	} else if pw_batt2charge == 0 { // battery 100% => do not charge
		u.Log.Infof("Battery to charge: %f%", pw_batt2charge)
		Setdefaults(fronius_ip)
	} else { // in the time range
		pw_pv_net := pw_forecast - pw_consumption
		// TODO: we know that from Estimate via Solar API, use that vaule instead.
		OpenModbusClient(fronius_ip)
		pw_max, _ := ReadFroniusModbusRegister(WChaMax)
		ClosemodbusClient()
		if pw_pv_net <= 0 { // net pv power is not enough => charge the diff
			u.Log.Infof("Net Forecast Power is not enough: %f W", pw_pv_net)
			ch_pc = SetChargePower(float64(pw_max), -1*pw_pv_net, float64(max_charge))
			ForceCharge(fronius_ip, ch_pc)
		} else { // there is pv power available
			u.Log.Infof("Net Forecast Power is enough: %f W", pw_pv_net)
			pw_grid := pw_batt2charge - pw_pv_net
			if pw_grid > 0 { // it's less than the battery's capacity to charge => charge the the diff
				ch_pc = SetChargePower(float64(pw_max), pw_grid, float64(max_charge))
				ForceCharge(fronius_ip, ch_pc)
			} else { // it is bigger than than battery capacity to charge => do not charge
				Setdefaults(fronius_ip)
			}
		}
	}
	return ch_pc, nil
}

func SetChargePower(max float64, load float64, limit float64) int16 {

	return int16(min(load*100/max, limit*100/max))

}

func CheckTimeRange(start_hr string, end_hr string) (bool, error) {
	now := time.Now()

	layout := "15:04"
	startTime, err := time.Parse(layout, start_hr)
	if err != nil {
		u.Log.Error("Something goes wrong parsing start time")
		panic(err)
	}

	endTime, err := time.Parse(layout, end_hr)
	if err != nil {
		u.Log.Error("Something goes wrong parsing end time")
		panic(err)
	}

	// Convert the current time to a time.Time value for today's date with the hour and minute set to the parsed start and end times
	startTime = time.Date(now.Year(), now.Month(), now.Day(), startTime.Hour(), startTime.Minute(), 0, 0, now.Location())
	endTime = time.Date(now.Year(), now.Month(), now.Day(), endTime.Hour(), endTime.Minute(), 0, 0, now.Location())

	// If the end time is before the start time, add 24 hours to it
	if endTime.Before(startTime) {
		endTime = endTime.Add(24 * time.Hour)
	}

	return (now.After(startTime) || now.Equal(startTime)) && (now.Before(endTime) || now.Equal(endTime)), nil
}
