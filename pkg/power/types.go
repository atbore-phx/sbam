package power

// Power retrieves Solcast forecasts. The forecasts fetched by the last
// Handler call are kept so callers can inspect individual periods.
type Power struct {
	lastForecasts []Forecasts
}

// LastForecasts returns the per-site forecasts retrieved by the last
// successful Handler call (nil before that).
func (power *Power) LastForecasts() []Forecasts {
	return power.lastForecasts
}

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
