package optimize

import "time"

// Sample is one numeric state change from Home Assistant history. A gap
// (unavailable, unknown, non-numeric, non-finite) is represented by Valid=false
// and is never treated as zero.
type Sample struct {
	At    time.Time
	Value float64
	Valid bool
}

// PVPeriod is one Solcast forecast period: [End-Period, End) with an average
// power of PVEstimateKW.
type PVPeriod struct {
	End          time.Time
	Period       time.Duration
	PVEstimateKW float64
}

// PricePoint is one price entry from the Nordpool sensor, incl. VAT, per kWh.
type PricePoint struct {
	Start time.Time
	End   time.Time
	Value float64
	Valid bool
}

// PriceMeta carries parse-time facts used by validation and logging.
type PriceMeta struct {
	Unit        string
	Currency    string
	Resolution  time.Duration
	LastUpdated time.Time
	TZMismatch  bool
}

// HourPrice is the hourly mean spot price incl. VAT for one absolute hour.
type HourPrice struct {
	Start      time.Time
	PriceInclV float64
}

// Profile is the expected household consumption per local hour-of-day, in Wh.
type Profile [24]float64
