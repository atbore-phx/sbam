package power

import (
	"fmt"
	"strings"
)

// PV forecast source constants (config key pv_source, SPEC-0003).
const (
	// PVSourceSolcastAPI downloads the forecast from the Solcast API (default).
	PVSourceSolcastAPI = "solcast_api"
	// PVSourceHASolcast reads the forecast from the Home Assistant Solcast
	// PV Forecast integration's entities.
	PVSourceHASolcast = "ha_solcast"
)

// ValidatePVSource returns the trimmed s if it names a known PV forecast
// source, or an error otherwise. The match is exact after trimming: a
// mistyped value never falls back to the default (S-027). An empty value is
// an error here; config loading maps an unset key to the default.
func ValidatePVSource(s string) (string, error) {
	v := strings.TrimSpace(s)
	switch v {
	case PVSourceSolcastAPI, PVSourceHASolcast:
		return v, nil
	default:
		return "", fmt.Errorf("unknown pv_source %s: must be one of %s, %s", quoteTrunc(s), PVSourceSolcastAPI, PVSourceHASolcast)
	}
}
