package mqtt

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

type Config struct {
	Enabled          bool   `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
	Broker           string `mapstructure:"broker" json:"broker" yaml:"broker"`
	ClientID         string `mapstructure:"client_id" json:"client_id" yaml:"client_id"`
	Username         string `mapstructure:"username" json:"username" yaml:"username"`
	Password         string `mapstructure:"password" json:"password" yaml:"password"`
	TLSCAFile        string `mapstructure:"tls_ca_file" json:"tls_ca_file" yaml:"tls_ca_file"`
	TLSClientCert    string `mapstructure:"tls_client_cert" json:"tls_client_cert" yaml:"tls_client_cert"`
	TLSClientCertKey string `mapstructure:"tls_client_cert_key" json:"tls_client_cert_key" yaml:"tls_client_cert_key"`
	TLSInsecureSkip  bool   `mapstructure:"tls_insecure_skip" json:"tls_insecure_skip" yaml:"tls_insecure_skip"`
	TopicPrefix      string `mapstructure:"topic_prefix" json:"topic_prefix" yaml:"topic_prefix"`
	HADiscovery      bool   `mapstructure:"ha_discovery" json:"ha_discovery" yaml:"ha_discovery"`
}

const (
	defaultTopicPrefix    = "sbam"
	defaultClientIDFmt    = "sbam-%d"
	defaultBrokerProtocol = "tcp"
	defaultBrokerHost     = "localhost"
	defaultBrokerPort     = 1883
	defaultBrokerFmt      = "%s://%s:%d"
)

func (c Config) WithDefaults() Config {
	if c.ClientID == "" {
		c.ClientID = fmt.Sprintf(defaultClientIDFmt, time.Now().Unix())
	}

	if c.Broker == "" {
		c.Broker = fmt.Sprintf(defaultBrokerFmt, defaultBrokerProtocol, defaultBrokerHost, defaultBrokerPort)
	}

	if c.TopicPrefix == "" {
		c.TopicPrefix = defaultTopicPrefix
	} else {
		c.TopicPrefix = normalizePrefix(c.TopicPrefix)
	}

	return c
}

func (c Config) Validate() error {
	if c.Enabled {
		if c.Broker == "" {
			return errors.New("mqtt broker required when enabled")
		}
		if c.TopicPrefix == "" {
			return errors.New("mqtt topic prefix required when enabled")
		}
	}
	return nil
}

// Use like this:
// cfg = cfg.WithDefaults()
// if err := cfg.Validate(); err != nil { ... }

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

type MessageHandler func(topic string, payload []byte)

type Client interface {
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error
	Subscribe(ctx context.Context, topic string, qos byte, handler MessageHandler) error
	IsConnected() bool
}

type Noop struct{}

type Paho struct {
	cfg       Config
	client    paho.Client
	closeOnce sync.Once
	closed    atomic.Bool
	reconnect atomic.Bool
}
