package mqtt

import (
	"time"
)

type ReconnectStrategy string

const (
	ReconnectStrategyCustom ReconnectStrategy = "custom"
	ReconnectStrategyPaho   ReconnectStrategy = "paho"
)

type Config struct {
	Enabled           bool
	Broker            string
	ClientID          string
	Username          string
	Password          string
	TLSCAFile         string
	TLSClientCert     string
	TLSClientCertKey  string
	TLSInsecureSkip   bool
	TopicPrefix       string
	HADiscoveryPrefix string
	FroniusIP         string
	HADiscovery       bool
	ReconnectStrategy ReconnectStrategy
}

type StatePayload struct {
	BatterySOCPct       *float64   `json:"battery_soc_pct"`
	BatteryCapacityWh   *float64   `json:"battery_capacity_wh"`
	ForecastTodayWh     *float64   `json:"forecast_today_wh"`
	PwNetWh             *float64   `json:"pw_net_wh"`
	ChargePct           *int16     `json:"charge_pct"`
	LastDecision        string     `json:"last_decision"`
	LastDecisionReason  string     `json:"last_decision_reason"`
	ChargeWindowActive  *bool      `json:"charge_window_active"`
	ReserveWindowActive *bool      `json:"batt_reserve_window_active"`
	Paused              bool       `json:"paused"`
	NextRun             *time.Time `json:"next_run"`
	Timestamp           time.Time  `json:"ts"`
}

type ErrorPayload struct {
	Error     string    `json:"error"`
	Source    string    `json:"source,omitempty"`
	Timestamp time.Time `json:"ts"`
}

type AckPayload struct {
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	Timestamp time.Time `json:"ts"`
}

type IntentKind string

const (
	IntentPause       IntentKind = "pause"
	IntentResume      IntentKind = "resume"
	IntentForceCharge IntentKind = "force_charge"
	IntentSetDefaults IntentKind = "set_defaults"
	IntentSetReserve  IntentKind = "set_reserve"
	IntentTriggerNow  IntentKind = "trigger_now"
)

type Intent struct {
	Kind          IntentKind `json:"kind"`
	TargetPct     int16      `json:"target_pct,omitempty"`
	DurationS     int        `json:"duration_s,omitempty"`
	PwBattReserve float64    `json:"pw_batt_reserve,omitempty"`
}

type DiscoveryEntity struct {
	Component string `json:"component"`
	ObjectID  string `json:"object_id"`
	Topic     string `json:"topic"`
	Payload   []byte `json:"payload"`
}
