package mqtt

import (
	"os"
	"regexp"
	"strings"

	"sbam/src/utils"
)

// Persisted in the working directory (/data for the add-on) so the identity
// survives fronius_ip changes - see issue #194 and docs/site/mqtt.md.
var (
	identityFilePath = "mqtt_device_id"
	identityPattern  = regexp.MustCompile(`^sbam_[A-Za-z0-9_-]{1,64}$`)
)

// EnsureStableDeviceID returns the persisted discovery device identifier,
// seeding identityFilePath from the config-derived value on first use.
func EnsureStableDeviceID(cfg Config) string {
	derived := discoveryDeviceIdentifier(cfg)

	raw, err := os.ReadFile(identityFilePath)
	if err == nil {
		persisted := strings.TrimSpace(string(raw))
		if identityPattern.MatchString(persisted) {
			if persisted != derived {
				utils.Log.Infow("mqtt discovery identity kept from file", "file", identityFilePath, "identity", persisted, "derived", derived)
			}
			return persisted
		}
		utils.Log.Warnw("mqtt discovery identity file invalid, re-seeding", "file", identityFilePath)
	} else if !os.IsNotExist(err) {
		utils.Log.Warnw("mqtt discovery identity file unreadable, using derived value", "file", identityFilePath, "error", err)
		return derived
	}

	if err := os.WriteFile(identityFilePath, []byte(derived+"\n"), 0o644); err != nil {
		utils.Log.Warnw("mqtt discovery identity not persisted, it will change if fronius_ip changes", "file", identityFilePath, "error", err)
	} else {
		utils.Log.Infow("mqtt discovery identity persisted", "file", identityFilePath, "identity", derived)
	}

	return derived
}
