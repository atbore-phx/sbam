package mqtt

import (
	"encoding/json"
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
	// Optimizer enables the cost-optimizer discovery entities (UX §3). When
	// false, PublishDiscovery removes them and clears <prefix>/plan.
	Optimizer bool
	// Currency is the unit of the Expected Cost sensor (e.g. "DKK"). Empty
	// means no unit and no device class are set on that sensor.
	Currency string
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
	// Optimizer fields (UX §2.5). All are omitted unless set. OptExpectedCost
	// is additionally emitted as JSON null whenever OptAction is set and the
	// cost is nil (fallback); see MarshalJSON.
	OptAction       *string  `json:"opt_action,omitempty"`
	OptRateW        *float64 `json:"opt_rate_w,omitempty"`
	OptExpectedCost *float64 `json:"opt_expected_cost,omitempty"`
	OptHorizonH     *int     `json:"opt_horizon_h,omitempty"`
	OptDryRun       *bool    `json:"opt_dry_run,omitempty"`
	OptReasons      []string `json:"opt_reasons,omitempty"`
}

// MarshalJSON serializes the state payload. Without optimizer data
// (OptAction == nil) the output is identical to the plain struct encoding.
// With optimizer data, opt_expected_cost is always present and is null when
// OptExpectedCost is nil, which signals a fallback run (UX §2.5).
func (p StatePayload) MarshalJSON() ([]byte, error) {
	type plain StatePayload
	if p.OptAction == nil {
		return json.Marshal(plain(p))
	}
	// The outer field shadows the embedded omitempty field of the same name.
	return json.Marshal(struct {
		plain
		OptExpectedCost *float64 `json:"opt_expected_cost"`
	}{plain: plain(p), OptExpectedCost: p.OptExpectedCost})
}

// PlanEntry is one hour of the published optimizer plan (UX §7.2). Values are
// rounded by PublishPlan: prices to 4 decimals, Wh and W to integers, SoC to 1
// decimal.
type PlanEntry struct {
	Start         time.Time `json:"start"`
	Action        string    `json:"action"`
	RateW         float64   `json:"rate_w"`
	SocEndPct     float64   `json:"soc_end_pct"`
	Buy           float64   `json:"buy"`
	Sell          float64   `json:"sell"`
	ConsumptionWh float64   `json:"consumption_wh"`
	PvWh          float64   `json:"pv_wh"`
}

// PlanPayload is the retained <prefix>/plan message (UX §7.2). It carries
// hourly aggregates only (S-017). Nil pointers serialize as null (fallback);
// nil Hours/Reasons serialize as empty arrays.
type PlanPayload struct {
	Timestamp     time.Time   `json:"ts"`
	DryRun        bool        `json:"dry_run"`
	Currency      string      `json:"currency"`
	HorizonH      int         `json:"horizon_h"`
	HorizonEnd    *time.Time  `json:"horizon_end"`
	StartSocPct   *float64    `json:"start_soc_pct"`
	ExpectedCost  *float64    `json:"expected_cost"`
	TerminalValue *float64    `json:"terminal_value"`
	Reason        string      `json:"reason"`
	Reasons       []string    `json:"reasons"`
	Hours         []PlanEntry `json:"hours"`
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
	// IntentWatchdog is internal: it is submitted by the runner's watchdog
	// timer and is never produced by ParseIntent, so it cannot arrive over MQTT.
	IntentWatchdog IntentKind = "watchdog"
	// IntentEVPoll is internal: it is submitted by the runner's EV guard poll
	// timer (SPEC-0002) and is never produced by ParseIntent.
	IntentEVPoll IntentKind = "ev_poll"
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
