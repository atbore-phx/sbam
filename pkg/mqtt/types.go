package mqtt

import (
	"time"
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
}

type StatePayload struct {
	BatterySOCPct               *float64   `json:"battery_soc_pct"`
	BatteryCapacityWh           *float64   `json:"battery_capacity_wh"`
	ForecastTodayWh             *float64   `json:"forecast_today_wh"`
	PwNetWh                     *float64   `json:"pw_net_wh"`
	ChargePct                   *int16     `json:"charge_pct"`
	LastDecision                string     `json:"last_decision"`
	LastDecisionReason          string     `json:"last_decision_reason"`
	ForecastHorizon             string     `json:"forecast_horizon"`
	ConsumptionHorizon          string     `json:"consumption_horizon"`
	ActiveWindow                *string    `json:"active_window,omitempty"`
	ActiveWindowMaxCharge       *float64   `json:"active_window_max_charge,omitempty"`
	ActiveWindowForecastHorizon *string    `json:"active_window_forecast_horizon,omitempty"`
	ChargeWindowActive          *bool      `json:"charge_window_active"`
	ReserveWindowActive         *bool      `json:"batt_reserve_window_active"`
	SchedulerMode               *string    `json:"scheduler_mode,omitempty"`
	DeprecationWarning          *string    `json:"deprecation_warning,omitempty"`
	Paused                      bool       `json:"paused"`
	NextRun                     *time.Time `json:"next_run"`
	Timestamp                   time.Time  `json:"ts"`
}

type ErrorPayload struct {
	Error     string    `json:"error"`
	Source    string    `json:"source,omitempty"`
	Timestamp time.Time `json:"ts"`
}

type AckPayload struct {
	Timestamp time.Time `json:"ts"`
	Command   string    `json:"command"`
	Accepted  bool      `json:"accepted"`
	Error     string    `json:"error,omitempty"`
}

type IntentKind string

const (
	IntentTick        IntentKind = "tick"
	IntentShutdown    IntentKind = "shutdown"
	IntentPause       IntentKind = "pause"
	IntentResume      IntentKind = "resume"
	IntentForceCharge IntentKind = "force_charge"
	IntentSetDefaults IntentKind = "set_defaults"
	IntentSetReserve  IntentKind = "set_reserve"
	IntentTriggerNow  IntentKind = "trigger_now"
)

// Intent describes a validated command request routed through the schedule runner.
//
// Intent carries normalized command kind and optional command-specific fields
// (for example force-charge target and pause deadline) plus the originating
// MQTT command topic used for acknowledgement publication.
type Intent struct {
	Kind            IntentKind `json:"kind"`
	TargetPct       int16      `json:"target_pct,omitempty"`
	IgnoreMaxCharge bool       `json:"ignore_max_charge,omitempty"`
	DurationS       int        `json:"duration_s,omitempty"`
	PauseUntil      *time.Time `json:"pause_until,omitempty"`
	PwBattReserve   float64    `json:"pw_batt_reserve,omitempty"`
	CommandTopic    string     `json:"-"`
}

type DiscoveryEntity struct {
	Component string `json:"component"`
	ObjectID  string `json:"object_id"`
	Topic     string `json:"topic"`
	Payload   []byte `json:"payload"`
}
