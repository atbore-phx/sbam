package mqtt

import (
	"errors"
	"fmt"
	"time"
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
