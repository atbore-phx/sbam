package cmd

import (
	"context"
	"time"

	"sbam/pkg/fronius"

	"github.com/spf13/cobra"
)

const const_sunspec_type = string(fronius.SunSpecIntSF)

// const_sunspec_verify_timeout bounds the read-only startup check. It is far
// above what a healthy inverter needs, and a failed check only logs a warning.
const const_sunspec_verify_timeout = 10 * time.Second

var sunspec_type string

// verifySunSpecType compares the configured SunSpec type with the inverter
// (read-only). Tests replace it to avoid Modbus connections.
var verifySunSpecType = fronius.VerifySunSpecType

// setupSunSpecType validates the configured sunspec_type and returns the type
// to use for all battery reads and writes. When verify is true it also checks,
// read-only, that the inverter reports the same model type and logs a warning
// otherwise.
func setupSunSpecType(ctx context.Context, value, froniusIP string, verify bool) (fronius.SunSpecType, error) {
	t, err := fronius.ParseSunSpecType(value)
	if err != nil {
		return "", err
	}
	if verify {
		verifyCtx, cancel := context.WithTimeout(ctx, const_sunspec_verify_timeout)
		defer cancel()
		verifySunSpecType(verifyCtx, t, froniusIP)
	}
	return t, nil
}

// commandContext returns the context of a cobra command, which is nil when a
// test calls its Run function directly.
func commandContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}
