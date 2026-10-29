package cmd

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yaml "gopkg.in/yaml.v3"
)

func TestCrontabSchemaRegex(t *testing.T) {
	data, err := os.ReadFile("../../home-assistant/addons/sbam/config.yaml")
	require.NoError(t, err, "failed to read config.yaml — ensure test runs from module root")

	var config struct {
		Schema struct {
			Crontab string `yaml:"crontab"`
		} `yaml:"schema"`
	}
	err = yaml.Unmarshal(data, &config)
	require.NoError(t, err)
	require.NotEmpty(t, config.Schema.Crontab, "crontab schema entry is missing or empty")

	raw := config.Schema.Crontab
	require.True(t, len(raw) > 7, "unexpected crontab schema format")
	require.Equal(t, "match(", raw[:6], "expected match(...) wrapper")
	require.Equal(t, ")", raw[len(raw)-1:], "expected match(...) wrapper")
	regexStr := raw[6 : len(raw)-1]

	re, err := regexp.Compile(regexStr)
	require.NoError(t, err, "failed to compile crontab schema regex")

	passCases := []string{
		"0 0 0 0 0",
		"00 00-05 * * *",
		"*/5 * * * *",
		"30 14 * * *",
		"1,15,30 * * * *",
		"0 0 * * 1-5",
		"@daily",
		"@every 1h",
		"@every 5m",
	}
	for _, tc := range passCases {
		t.Run("pass/"+tc, func(t *testing.T) {
			assert.True(t, re.MatchString(tc), "expected %q to match", tc)
		})
	}

	failCases := []string{
		"",
		"not a cron expression",
		"0",
		"a b c d e",
	}
	for _, tc := range failCases {
		t.Run("fail/"+tc, func(t *testing.T) {
			assert.False(t, re.MatchString(tc), "expected %q to not match", tc)
		})
	}
}

// addonConfig is the subset of the add-on config.yaml the SPEC-0001 surface
// tests need. options/schema are decoded generically so every key is visible.
type addonConfig struct {
	HomeassistantAPI bool           `yaml:"homeassistant_api"`
	Options          map[string]any `yaml:"options"`
	Schema           map[string]any `yaml:"schema"`
}

func loadAddonConfig(t *testing.T) addonConfig {
	t.Helper()
	data, err := os.ReadFile("../../home-assistant/addons/sbam/config.yaml")
	require.NoError(t, err, "failed to read config.yaml — ensure test runs from module root")
	var cfg addonConfig
	require.NoError(t, yaml.Unmarshal(data, &cfg))
	require.NotEmpty(t, cfg.Options)
	require.NotEmpty(t, cfg.Schema)
	return cfg
}

// addonSchemaRegex extracts and compiles the regex of a match(...) schema entry.
func addonSchemaRegex(t *testing.T, cfg addonConfig, key string) *regexp.Regexp {
	t.Helper()
	raw, ok := cfg.Schema[key].(string)
	require.True(t, ok, "schema entry %s missing or not a string", key)
	require.True(t, strings.HasPrefix(raw, "match(") && strings.HasSuffix(raw, ")"), "expected match(...) wrapper for %s", key)
	re, err := regexp.Compile(raw[len("match(") : len(raw)-1])
	require.NoError(t, err, "failed to compile %s schema regex", key)
	return re
}

func surchargeCSV(sep string, vals ...string) string { return strings.Join(vals, sep) }

func surchargeRepeat(v string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// T-0170 (AC-20, UX §2.2): the opt_purchase_surcharge schema regex accepts
// exactly 24 numbers in [-5, 10] with '.' decimals, and everything it accepts
// parseSurcharge accepts too.
func TestSurchargeSchemaRegex_T0170(t *testing.T) {
	cfg := loadAddonConfig(t)
	re := addonSchemaRegex(t, cfg, "opt_purchase_surcharge")

	mixed := []string{"0.52", "-0.1", "0", "10", "-5", "1.25", "9.999", "-4.5", "5", "0.0", "2.04", "-0",
		"3", "4.75", "-3.25", "6.5", "7", "8.125", "-1", "-2.00", "0.78", "10.0", "-5.0", "1"}
	require.Len(t, mixed, 24)

	pass := map[string]string{
		"24 zeros":                    surchargeCSV(",", surchargeRepeat("0", 24)...),
		"negatives and decimals":      surchargeCSV(",", mixed...),
		"spaces after commas":         surchargeCSV(", ", mixed...),
		"spaces around commas":        surchargeCSV(" , ", surchargeRepeat("0.5", 24)...),
		"leading and trailing spaces": "  " + surchargeCSV(",", surchargeRepeat("1.5", 24)...) + " ",
		"range bounds":                surchargeCSV(",", append(surchargeRepeat("-5", 12), surchargeRepeat("10", 12)...)...),
	}
	if def, ok := cfg.Options["opt_purchase_surcharge"].(string); ok {
		pass["options default"] = def
	}
	for name, s := range pass {
		t.Run("pass/"+name, func(t *testing.T) {
			assert.True(t, re.MatchString(s), "expected %q to match", s)
			// Consistency: anything the add-on accepts, sbam accepts.
			_, err := parseSurcharge(s)
			assert.NoError(t, err, "regex accepted %q but parseSurcharge rejected it", s)
		})
	}

	fail := map[string]string{
		"23 values":            surchargeCSV(",", surchargeRepeat("0", 23)...),
		"25 values":            surchargeCSV(",", surchargeRepeat("0", 25)...),
		"decimal comma 1,5":    surchargeCSV(";", surchargeRepeat("1,5", 24)...),
		"decimal comma inline": "1,5," + surchargeCSV(",", surchargeRepeat("0", 23)...), // 1,5 + 23 values reads as 25 items
		"empty":                "",
		"only spaces":          "   ",
		"empty item":           "," + surchargeCSV(",", surchargeRepeat("0", 23)...),
		"trailing comma":       surchargeCSV(",", surchargeRepeat("0", 24)...) + ",",
		"above max 10.5":       "10.5," + surchargeCSV(",", surchargeRepeat("0", 23)...),
		"above max 11":         "11," + surchargeCSV(",", surchargeRepeat("0", 23)...),
		"below min -5.1":       "-5.1," + surchargeCSV(",", surchargeRepeat("0", 23)...),
		"below min -6":         "-6," + surchargeCSV(",", surchargeRepeat("0", 23)...),
		"non-numeric item":     "abc," + surchargeCSV(",", surchargeRepeat("0", 23)...),
		"semicolon separator":  surchargeCSV(";", surchargeRepeat("0", 24)...),
		"exponent notation":    "1e1," + surchargeCSV(",", surchargeRepeat("0", 23)...),
	}
	for name, s := range fail {
		t.Run("fail/"+name, func(t *testing.T) {
			assert.False(t, re.MatchString(s), "expected %q to not match", s)
		})
	}

	// Consistency sweep: every value in [-6, 11] in 0.25 steps that the regex
	// accepts (as 24 copies) must also be accepted by parseSurcharge.
	t.Run("consistency sweep", func(t *testing.T) {
		accepted := 0
		for i := -24; i <= 44; i++ {
			f := float64(i) / 4
			for _, v := range []string{strconv.FormatFloat(f, 'f', -1, 64), strconv.FormatFloat(f, 'f', 2, 64)} {
				s := surchargeCSV(",", surchargeRepeat(v, 24)...)
				if !re.MatchString(s) {
					continue
				}
				accepted++
				_, err := parseSurcharge(s)
				assert.NoError(t, err, "regex accepted %q but parseSurcharge rejected it", v)
			}
		}
		assert.Positive(t, accepted)
	})
}

// T-0171 (TC-013d): the add-on exposes ha_token as an optional password,
// ha_url as an optional string that is not pre-filled in options, offers
// scheduler_mode optimize, defaults opt_dry_run to true and requests
// homeassistant_api so the Supervisor token is available.
func TestAddonOptimizeSchema_T0171(t *testing.T) {
	cfg := loadAddonConfig(t)

	assert.Equal(t, "password?", cfg.Schema["ha_token"], "ha_token must be an optional password")
	assert.Equal(t, "str?", cfg.Schema["ha_url"], "ha_url must be an optional string")
	assert.NotContains(t, cfg.Options, "ha_url", "ha_url must not be pre-filled in options")
	assert.NotContains(t, cfg.Options, "ha_token", "ha_token must not be pre-filled in options")

	mode, ok := cfg.Schema["scheduler_mode"].(string)
	require.True(t, ok, "scheduler_mode schema missing")
	require.True(t, strings.HasPrefix(mode, "list(") && strings.HasSuffix(mode, ")"), "scheduler_mode must be a list(...)")
	assert.ElementsMatch(t, []string{"crontab", "windows", "optimize"}, strings.Split(mode[len("list("):len(mode)-1], "|"))
	assert.Equal(t, "crontab", cfg.Options["scheduler_mode"], "existing installs keep the crontab default")

	assert.Equal(t, true, cfg.Options["opt_dry_run"], "opt_dry_run must default to true in the add-on")
	assert.Equal(t, "bool", cfg.Schema["opt_dry_run"])
	assert.True(t, cfg.HomeassistantAPI, "homeassistant_api must be true for the Supervisor token")
}

// addonOptionString renders an add-on options value the way pflag renders a
// flag default, so the two can be compared.
func addonOptionString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

// T-0172 (surface parity): every ha_*/opt_* key registered by
// registerOptimizeFlags (enumerated from a cobra command, not a hand-written
// list) is in the add-on schema, in options (except the optional
// ha_url/ha_token), and the options default equals the flag default.
func TestAddonOptimizeParity_T0172(t *testing.T) {
	cfg := loadAddonConfig(t)
	flags := optCfgRegisteredKeys(t)

	optionalOnly := map[string]bool{keyHAURL: true, keyHAToken: true}

	for _, f := range flags {
		t.Run(f.Name, func(t *testing.T) {
			assert.Contains(t, cfg.Schema, f.Name, "missing from add-on schema")
			if optionalOnly[f.Name] {
				assert.NotContains(t, cfg.Options, f.Name, "optional key must not be in options")
				return
			}
			require.Contains(t, cfg.Options, f.Name, "missing from add-on options")
			opt := addonOptionString(cfg.Options[f.Name])
			switch {
			case f.Name == keyOptPurchaseSurcharge:
				// The add-on default is 24 explicit zeros; the flag default ""
				// means the same thing (parseSurcharge yields 24 x 0).
				a, err := parseSurcharge(opt)
				require.NoError(t, err)
				b, err := parseSurcharge(f.DefValue)
				require.NoError(t, err)
				assert.Equal(t, b, a, "options default differs from flag default")
			case f.Value.Type() == "float64" || f.Value.Type() == "int":
				want, err := strconv.ParseFloat(f.DefValue, 64)
				require.NoError(t, err)
				got, err := strconv.ParseFloat(opt, 64)
				require.NoError(t, err, "options value %q is not numeric", opt)
				assert.Equal(t, want, got, "options default differs from flag default")
			default:
				assert.Equal(t, f.DefValue, opt, "options default differs from flag default")
			}
		})
	}

	// Reverse direction: no ha_*/opt_* key in the add-on that sbam does not
	// register (a typo there would be silently ignored).
	t.Run("no unknown add-on keys", func(t *testing.T) {
		known := map[string]bool{}
		for _, f := range flags {
			known[f.Name] = true
		}
		for k := range cfg.Schema {
			if optCfgIsOptimizeFlag(k) {
				assert.True(t, known[k], "schema key %s is not a registered flag", k)
			}
		}
		for k := range cfg.Options {
			if optCfgIsOptimizeFlag(k) {
				assert.True(t, known[k], "options key %s is not a registered flag", k)
			}
		}
	})
}
