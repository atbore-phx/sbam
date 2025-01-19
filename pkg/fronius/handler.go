package fronius

import u "sbam/src/utils"

func New() *Fronius {
	return &Fronius{}
}

func (fronius *Fronius) Handler(pw_forecast float64, pw_batt2charge float64, pw_batt_max float64, pw_consumption float64, max_charge float64, pw_batt_reserve float64, start_hr string, end_hr string, fronius_ip string, batt_reserve_charge_enabled bool, fronius_port ...string) (int16, error) {
	p := "502"
	if len(fronius_port) > 0 {
		p = fronius_port[0]
	}

	charge_pc, err := SetFroniusChargeBatteryMode(pw_forecast, pw_batt2charge, pw_batt_max, pw_consumption, max_charge, pw_batt_reserve, start_hr, end_hr, fronius_ip, batt_reserve_charge_enabled, p)
	if err != nil {
		u.Log.Errorln("Error setting Fronius Battery charge: %s ", err)
		return charge_pc, err
	}

	return charge_pc, nil

}
