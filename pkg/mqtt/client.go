package mqtt

import (
	u "sbam/src/utils"
	"strings"
)

func New(cfg Config) (Client, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		u.HandleError(err, "mqtt config validation failed:")
		return nil, err
	}

	if !cfg.Enabled {
		return NewNoop(), nil
	}

	return NewPaho(cfg)

}

func normalizePrefix(prefix string) string {
	return strings.Trim(prefix, "/")
}
func stateTopic(prefix string) string
func errorTopic(prefix string) string
func availabilityTopic(prefix string) string
