package fronius

import (
	"fmt"
	"time"
)

func SetFroniusChargeBatteryMode(pw_forecast float64, pw_batt2charge float64, pw_consumption float64, max_charge int, start_hr string, end_hr string, fronius_ip string) (int16, error) {
	var ch_pc int16 = 0
	time, _ := CheckTimeRange(start_hr, end_hr)

	if !time { // out of the time range => do not charge
		Setdefaults(fronius_ip, "502")
	} else { // in the time range
		pw_pv_av, pw_pv_net, _ := CheckPowerPVAvailability(pw_forecast, pw_batt2charge, pw_consumption)
		pw_max, _ := ReadFroniusModbusRegister(WChaMax)
		if pw_pv_av { // there is pv power available
			pw_grid := pw_batt2charge - pw_pv_net
			if pw_grid > 0 { // and it's less than battery capacity to charge => charge the the diff
				ch_pc = pw_max / int16(pw_grid) * 100
				ch_pc = min(ch_pc, int16(max_charge))
				ForceCharge(fronius_ip, "502", ch_pc)
			} else { // or it is bigger than than battery capacity to charge => do not charge
				Setdefaults(fronius_ip, "502")
			}
		} else { // or pv power is not enough => charge the diff
			ch_pc = pw_max / int16(pw_pv_net) * -100
			ch_pc = min(ch_pc, int16(max_charge))
			ForceCharge(fronius_ip, "502", ch_pc)
		}
	}
	return ch_pc, nil
}

func CheckPowerPVAvailability(pw_forecast float64, pw_batt2charge float64, pw_consumption float64) (bool, float64, error) {
	day_net_power := pw_forecast - pw_consumption

	if day_net_power < 0 {
		return false, day_net_power, nil
	}
	return true, day_net_power, nil

}

func CheckTimeRange(start_hr string, end_hr string) (bool, error) {
	now := time.Now()

	layout := "15:04"
	startTime, err := time.Parse(layout, start_hr)
	if err != nil {
		fmt.Print("Something goes wrong parsing start time")
		panic(err)
	}

	endTime, err := time.Parse(layout, end_hr)
	if err != nil {
		fmt.Print("Something goes wrong parsing end time")
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
