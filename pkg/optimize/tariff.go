package optimize

import "time"

// Tariff is a fixed time-of-use tariff: prices per kWh incl. tax, indexed by local hour of day.
type Tariff struct {
	Buy      [24]float64
	Sell     [24]float64
	Currency string
}

// HourlyPrices returns n hours from slot0Start, each at the buy price of its local hour.
func (t Tariff) HourlyPrices(slot0Start time.Time, loc *time.Location, n int) []HourPrice {
	if loc == nil {
		loc = time.Local
	}
	var out []HourPrice
	for k := 0; k < n; k++ {
		start := slot0Start.Add(time.Duration(k) * time.Hour)
		out = append(out, HourPrice{Start: start, PriceInclV: t.Buy[start.In(loc).Hour()]})
	}
	return out
}

// BuySell is the GridInput.BuySell of the tariff. HourOfDay is taken modulo 24.
func (t Tariff) BuySell(h Hour) (buy, sell float64) {
	hod := ((h.HourOfDay % 24) + 24) % 24
	return t.Buy[hod], t.Sell[hod]
}
