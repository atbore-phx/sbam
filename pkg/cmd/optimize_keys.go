package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cast"
	"github.com/spf13/viper"
)

// The optimizer options are grouped under one `optimizer:` map in config.yaml
// and the HA add-on. The CLI keeps the flat --opt_* / --ha_* flags; they are
// bound to the nested viper keys (flagAnnotationViperKey). Environment
// variables are OPTIMIZER_<NAME> (viper's "." → "_" key replacer), with
// HA_URL / HA_TOKEN kept as the documented names for the HA access.
//
// The flat keys of the first dev builds (opt_* in config.yaml, OPT_* in the
// environment) are still read for one release, with a deprecation warning,
// when the nested key is not set.

// optimizerGroup is the config map that holds the optimizer options.
const optimizerGroup = "optimizer"

// flagAnnotationViperKey names the viper key a flag is bound to when it
// differs from the flag name.
const flagAnnotationViperKey = "sbam_viper_key"

// optKeySpec maps one nested optimizer key to its flat flag name, which is
// also the deprecated flat config key, and its environment variables.
type optKeySpec struct {
	key       string // nested viper key, e.g. optimizer.price_entity
	flag      string // CLI flag and deprecated flat key, e.g. opt_price_entity
	env       string // primary env var, e.g. OPTIMIZER_PRICE_ENTITY
	aliasEnv  string // also accepted: HA_URL/HA_TOKEN (current) or OPT_* (deprecated)
	aliasDepr bool   // aliasEnv is deprecated
}

func optNestedKey(name string) string { return optimizerGroup + "." + name }

func optEnvName(key string) string {
	return strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

// optKeySpecs lists every optimizer key in add-on order.
var optKeySpecs = func() []optKeySpec {
	type entry struct {
		name, flag, alias string
		depr              bool
	}
	entries := []entry{
		{"price_entity", "opt_price_entity", "", true},
		{"consumption_entity", "opt_consumption_entity", "", true},
		{"ev_entity", "opt_ev_entity", "", true},
		{"ev_guard_w", "opt_ev_guard_w", "", true},
		{"purchase_surcharge", "opt_purchase_surcharge", "", true},
		{"vat_rate", "opt_vat_rate", "", true},
		{"sell_margin", "opt_sell_margin", "", true},
		{"min_gain", "opt_min_gain", "", true},
		{"wear_cost", "opt_wear_cost", "", true},
		{"battery_efficiency", "opt_battery_efficiency", "", true},
		{"max_charge_rate_w", "opt_max_charge_rate_w", "", true},
		{"max_discharge_rate_w", "opt_max_discharge_rate_w", "", true},
		{"min_soc_pct", "opt_min_soc_pct", "", true},
		{"history_days", "opt_history_days", "", true},
		{"dry_run", "opt_dry_run", "", true},
		{"ha_url", "ha_url", "HA_URL", false},
		{"ha_token", "ha_token", "HA_TOKEN", false},
	}
	out := make([]optKeySpec, 0, len(entries))
	for _, e := range entries {
		s := optKeySpec{key: optNestedKey(e.name), flag: e.flag}
		s.env = optEnvName(s.key)
		s.aliasEnv = e.alias
		s.aliasDepr = e.depr
		if s.aliasEnv == "" {
			s.aliasEnv = strings.ToUpper(e.flag) // OPT_PRICE_ENTITY
		}
		out = append(out, s)
	}
	return out
}()

// optSpecByKey returns the spec of a nested key; ok is false for keys outside
// the optimizer group (such as pw_consumption).
func optSpecByKey(key string) (optKeySpec, bool) {
	for _, s := range optKeySpecs {
		if s.key == key {
			return s, true
		}
	}
	return optKeySpec{}, false
}

// bindOptimizeEnv binds every nested optimizer key to its environment
// variables (primary first). Safe to call more than once.
func bindOptimizeEnv() {
	for _, s := range optKeySpecs {
		_ = viper.BindEnv(s.key, s.env, s.aliasEnv)
	}
}

// optGet reads a key with flag > env > nested yaml > deprecated flat yaml >
// flag default precedence. It reports via deprecated when the value came
// from a deprecated flat config key.
func optGet(key string, deprecated func(string)) any {
	if viper.IsSet(key) {
		return viper.Get(key)
	}
	if s, ok := optSpecByKey(key); ok && viper.InConfig(s.flag) {
		if deprecated != nil {
			deprecated(fmt.Sprintf("config key %s is deprecated; move it to %s:", s.flag, s.key))
		}
		return viper.Get(s.flag)
	}
	return viper.Get(key)
}

func optGetString(key string, deprecated func(string)) string {
	return strings.TrimSpace(cast.ToString(optGet(key, deprecated)))
}

// optDeprecatedEnvWarnings reports deprecated OPT_* variables that are set
// while their OPTIMIZER_* replacement is not.
func optDeprecatedEnvWarnings(getenv func(string) string) []string {
	var out []string
	for _, s := range optKeySpecs {
		if s.aliasDepr && getenv(s.aliasEnv) != "" && getenv(s.env) == "" {
			out = append(out, fmt.Sprintf("environment variable %s is deprecated; use %s", s.aliasEnv, s.env))
		}
	}
	return out
}
