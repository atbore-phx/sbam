package storage

type Storage struct{}

type Batteries struct {
	Body struct {
		Data map[string]Battery `json:"Data"`
	} `json:"Body"`
	Head struct {
		RequestArguments struct {
			Scope string `json:"Scope"`
		} `json:"RequestArguments"`
		Status struct {
			Code        int    `json:"Code"`
			Reason      string `json:"Reason"`
			UserMessage string `json:"UserMessage"`
		} `json:"Status"`
		Timestamp string `json:"Timestamp"`
	} `json:"Head"`
}

type Battery struct {
	Controller Controller    `json:"Controller"`
	Modules    []interface{} `json:"Modules"`
}

type Controller struct {
	CapacityMaximum  float64 `json:"Capacity_Maximum"`
	CurrentDC        float64 `json:"Current_DC"`
	DesignedCapacity float64 `json:"DesignedCapacity"`
	Details          struct {
		Manufacturer string `json:"Manufacturer"`
		Model        string `json:"Model"`
		Serial       string `json:"Serial"`
	} `json:"Details"`
	Enable                int     `json:"Enable"`
	StateOfChargeRelative float64 `json:"StateOfCharge_Relative"`
	StatusBatteryCell     float64 `json:"Status_BatteryCell"`
	TemperatureCell       float64 `json:"Temperature_Cell"`
	TimeStamp             int     `json:"TimeStamp"`
	VoltageDC             float64 `json:"Voltage_DC"`
}
