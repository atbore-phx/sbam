package power

type Power struct{}

type Forecast struct {
	PVEstimate   float64 `json:"pv_estimate"`
	PVEstimate10 float64 `json:"pv_estimate10"`
	PVEstimate90 float64 `json:"pv_estimate90"`
	PeriodEnd    string  `json:"period_end"`
	Period       string  `json:"period"`
}

type Forecasts struct {
	Forecasts []Forecast `json:"forecasts"`
}
