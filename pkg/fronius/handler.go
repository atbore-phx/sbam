package fronius

func New() *Fronius {
	return &Fronius{}
}

func (fronius *Fronius) Handler(pw_forecast float64, pw_batt2charge float64, pw_consumption float64, max_charge int, start_hr string, end_hr string, fronius_ip string, fronius_port ...string) (int16, error) {
	p := "502"
	if len(fronius_port) > 0 {
		p = fronius_port[0]
	}

	charge_pc, _ := SetFroniusChargeBatteryMode(pw_forecast, pw_batt2charge, pw_consumption, max_charge, start_hr, end_hr, fronius_ip, p)

	return charge_pc, nil

}
