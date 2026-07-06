// Package utils — startup parameters dump.
//
// DumpStartupParams produces a deterministic, multi-line, human-friendly
// listing of every viper key reachable from the executing cobra subcommand,
// together with its effective value and the source from which the value was
// resolved (flag | env | yaml | default). Secret keys (registered in
// SecretKeys) are redacted to the fixed literal "***".
//
// The set of keys is auto-discovered from viper.AllKeys() at call time, so
// adding a new flag to any subcommand makes that key appear in the dump
// automatically. The only manual step required when introducing a new flag
// that carries sensitive material is appending its key name to SecretKeys.
//
// Note: viper.AllKeys() also surfaces keys present in config.yaml that the
// current subcommand does not consume. This is intentional — it makes stale
// config visible — but consumers should be aware of it.
package utils

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// SecretKeys is the registry of viper keys whose values must be redacted in
// any user-visible dump. Append to this map when introducing a new flag that
// carries sensitive material.
var SecretKeys = map[string]struct{}{
	"apikey":                   {},
	"mqtt_password":            {},
	"mqtt_tls_client_cert_key": {},
}

const redacted = "***"

// SourceOf returns one of "flag", "env", "yaml", "default" describing where
// the effective value of key was resolved from. cmd may be nil; in that case
// the "flag" check is skipped. The env probe uses os.LookupEnv with the
// upper-cased key name to mirror viper.AutomaticEnv() behavior in absence of
// a SetEnvPrefix call (which sbam does not use).
func SourceOf(cmd *cobra.Command, key string) string {
	if cmd != nil {
		if f := cmd.Flags().Lookup(key); f != nil && f.Changed {
			return "flag"
		}
	}
	if _, ok := os.LookupEnv(strings.ToUpper(key)); ok {
		return "env"
	}
	if viper.InConfig(key) {
		return "yaml"
	}
	return "default"
}

// DumpStartupParams returns a deterministic, multi-line block listing every
// viper key together with its effective value and resolution source. Secret
// keys are redacted. Any entries in extras are appended after the viper keys
// with source=computed. The function never panics on nil/empty inputs.
func DumpStartupParams(cmd *cobra.Command, extras map[string]interface{}) string {
	var b strings.Builder

	header := "effective startup parameters"
	if cmd != nil && cmd.Name() != "" {
		header = fmt.Sprintf("%s (subcommand: %s)", header, cmd.Name())
	}
	b.WriteString(header)

	keys := viper.AllKeys()
	// Determine max key length across viper keys and extras for alignment.
	maxKeyLen := 0
	for _, k := range keys {
		if len(k) > maxKeyLen {
			maxKeyLen = len(k)
		}
	}
	for k := range extras {
		if len(k) > maxKeyLen {
			maxKeyLen = len(k)
		}
	}

	if len(keys) > 0 {
		sort.Strings(keys)
		for _, k := range keys {
			var rendered string
			if _, secret := SecretKeys[k]; secret {
				rendered = redacted
			} else {
				rendered = fmt.Sprintf("%#v", viper.Get(k))
			}
			b.WriteString(fmt.Sprintf("\n  %-*s = %-24s source=%s",
				maxKeyLen, k, rendered, SourceOf(cmd, k)))
		}
	}

	if len(extras) > 0 {
		extraKeys := make([]string, 0, len(extras))
		for k := range extras {
			extraKeys = append(extraKeys, k)
		}
		sort.Strings(extraKeys)
		for _, k := range extraKeys {
			b.WriteString(fmt.Sprintf("\n  %-*s = %-24s source=computed",
				maxKeyLen, k, fmt.Sprintf("%#v", extras[k])))
		}
	}

	return b.String()
}

// LogStartupParams emits DumpStartupParams via Log.Debug. Safe to call from
// any subcommand's Run; produces no output when zap level is above Debug.
// extras are appended after the viper keys with source=computed.
func LogStartupParams(cmd *cobra.Command, extras map[string]interface{}) {
	Log.Debug("\n" + DumpStartupParams(cmd, extras))
}
