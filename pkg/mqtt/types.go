package mqtt

import (
	"time"
)

type Config struct {
	Enabled          bool
	Broker           string
	ClientID         string
	Username         string
	Password         string
	TLSCAFile        string
	TLSClientCert    string
	TLSClientCertKey string
	TLSInsecureSkip  bool
	TopicPrefix      string
	HADiscovery      bool
}

type StatePayload struct {
	BatterySOCPct      float64    `json:"battery_soc_pct"`
	BatteryCapacityWh  float64    `json:"battery_capacity_wh"`
	ForecastTodayWh    float64    `json:"forecast_today_wh"`
	LastDecision       string     `json:"last_decision"`
	LastDecisionReason string     `json:"last_decision_reason,omitempty"`
	Paused             bool       `json:"paused"`
	NextRun            *time.Time `json:"next_run,omitempty"`
	Timestamp          time.Time  `json:"ts"`
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
	TargetPct     int        `json:"target_pct,omitempty"`
	DurationS     int        `json:"duration_s,omitempty"`
	PwBattReserve float64    `json:"pw_batt_reserve,omitempty"`
}

type DiscoveryEntity struct {
	Component string `json:"component"`
	ObjectID  string `json:"object_id"`
	Topic     string `json:"topic"`
	Payload   []byte `json:"payload"`
}
