package cmd

import "sbam/pkg/fronius"

const const_sunspec_type = string(fronius.SunSpecIntSF)

var sunspec_type string

// verifySunSpecType compares the configured SunSpec type with the inverter
// (read-only). Tests replace it to avoid Modbus connections.
var verifySunSpecType = fronius.VerifySunSpecType

// setupSunSpecType validates the configured sunspec_type and makes it the
// type used for all battery reads and writes. When verify is true it also
// checks, read-only, that the inverter reports the same model type and logs a
// warning otherwise.
func setupSunSpecType(value, froniusIP string, verify bool) error {
	t, err := fronius.ParseSunSpecType(value)
	if err != nil {
		return err
	}
	fronius.SetSunSpecType(t)
	if verify {
		verifySunSpecType(t, froniusIP)
	}
	return nil
}
