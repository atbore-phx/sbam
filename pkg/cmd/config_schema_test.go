package cmd

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

// addonOptimizer returns the optimizer: map of the add-on schema and options.
func addonOptimizer(t *testing.T, cfg addonConfig) (schema, options map[string]any) {
	t.Helper()
	schema, ok := cfg.Schema[optimizerGroup].(map[string]any)
	require.True(t, ok, "schema.optimizer must be a map")
	options, ok = cfg.Options[optimizerGroup].(map[string]any)
	require.True(t, ok, "options.optimizer must be a map")
	return schema, options
}

// addonSchemaRegex extracts and compiles the regex of a match(...) schema
// entry of the given schema map.
func addonSchemaRegex(t *testing.T, schema map[string]any, key string) *regexp.Regexp {
	t.Helper()
	raw, ok := schema[key].(string)
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

// T-0170 (AC-20, UX §2.2): the optimizer.purchase_surcharge schema regex accepts
// exactly 24 numbers in [-5, 10] with '.' decimals, and everything it accepts
// parseSurcharge accepts too.
func TestSurchargeSchemaRegex_T0170(t *testing.T) {
	cfg := loadAddonConfig(t)
	optSchema, optOptions := addonOptimizer(t, cfg)
	re := addonSchemaRegex(t, optSchema, "purchase_surcharge")

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
	if def, ok := optOptions["purchase_surcharge"].(string); ok {
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

// T-0171 (TC-013d): the add-on groups the optimizer options under
// optimizer:, exposes optimizer.ha_token as an optional password and
// optimizer.ha_url as an optional string that is not pre-filled in options,
// offers scheduler_mode optimize, defaults optimizer.dry_run to true and
// requests homeassistant_api so the Supervisor token is available.
func TestAddonOptimizeSchema_T0171(t *testing.T) {
	cfg := loadAddonConfig(t)
	optSchema, optOptions := addonOptimizer(t, cfg)

	assert.Equal(t, "password?", optSchema["ha_token"], "ha_token must be an optional password")
	assert.Equal(t, "str?", optSchema["ha_url"], "ha_url must be an optional string")
	assert.NotContains(t, optOptions, "ha_url", "ha_url must not be pre-filled in options")
	assert.NotContains(t, optOptions, "ha_token", "ha_token must not be pre-filled in options")

	mode, ok := cfg.Schema["scheduler_mode"].(string)
	require.True(t, ok, "scheduler_mode schema missing")
	require.True(t, strings.HasPrefix(mode, "list(") && strings.HasSuffix(mode, ")"), "scheduler_mode must be a list(...)")
	assert.ElementsMatch(t, []string{"crontab", "windows", "optimize"}, strings.Split(mode[len("list("):len(mode)-1], "|"))
	assert.Equal(t, "crontab", cfg.Options["scheduler_mode"], "existing installs keep the crontab default")

	assert.Equal(t, true, optOptions["dry_run"], "optimizer.dry_run must default to true in the add-on")
	assert.Equal(t, "bool", optSchema["dry_run"])
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

// addonOptimizerName returns the key inside the optimizer: map for a flag
// registered by registerOptimizeFlags (from its viper key annotation).
func addonOptimizerName(t *testing.T, f *pflag.Flag) string {
	t.Helper()
	ann := f.Annotations[flagAnnotationViperKey]
	require.Len(t, ann, 1, "flag %s has no viper key annotation", f.Name)
	require.True(t, strings.HasPrefix(ann[0], optimizerGroup+"."), "flag %s is not bound to the optimizer group", f.Name)
	return strings.TrimPrefix(ann[0], optimizerGroup+".")
}

// T-0172 (surface parity): every ha_*/opt_* flag registered by
// registerOptimizeFlags (enumerated from a cobra command, not a hand-written
// list) has its nested key in the add-on optimizer: schema and options
// (except the optional ha_url/ha_token), and the options default equals the
// flag default.
func TestAddonOptimizeParity_T0172(t *testing.T) {
	cfg := loadAddonConfig(t)
	optSchema, optOptions := addonOptimizer(t, cfg)
	flags := optCfgRegisteredKeys(t)

	optionalOnly := map[string]bool{keyHAURL: true, keyHAToken: true}

	for _, f := range flags {
		t.Run(f.Name, func(t *testing.T) {
			name := addonOptimizerName(t, f)
			key := optimizerGroup + "." + name
			assert.Contains(t, optSchema, name, "missing from add-on optimizer schema")
			if optionalOnly[key] {
				assert.NotContains(t, optOptions, name, "optional key must not be in options")
				return
			}
			require.Contains(t, optOptions, name, "missing from add-on optimizer options")
			opt := addonOptionString(optOptions[name])
			switch {
			case key == keyOptPurchaseSurcharge:
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

	// Reverse direction: no key in the optimizer: map that sbam does not
	// register (a typo there would be silently ignored), and no flat
	// ha_*/opt_* keys left at the top level.
	t.Run("no unknown add-on keys", func(t *testing.T) {
		known := map[string]bool{}
		for _, f := range flags {
			known[addonOptimizerName(t, f)] = true
		}
		for k := range optSchema {
			assert.True(t, known[k], "schema key optimizer.%s is not a registered option", k)
		}
		for k := range optOptions {
			assert.True(t, known[k], "options key optimizer.%s is not a registered option", k)
		}
		for k := range cfg.Schema {
			assert.False(t, optCfgIsOptimizeFlag(k), "flat schema key %s belongs under optimizer:", k)
		}
		for k := range cfg.Options {
			assert.False(t, optCfgIsOptimizeFlag(k), "flat options key %s belongs under optimizer:", k)
		}
	})
}

// Every option in the add-on optimizer: schema has a translated name and
// description under configuration.optimizer.fields, and no translation is
// left for an option that does not exist.
func TestAddonOptimizerTranslations(t *testing.T) {
	cfg := loadAddonConfig(t)
	optSchema, _ := addonOptimizer(t, cfg)

	data, err := os.ReadFile("../../home-assistant/addons/sbam/translations/en.yaml")
	require.NoError(t, err)
	var tr struct {
		Configuration map[string]struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
			Fields      map[string]struct {
				Name        string `yaml:"name"`
				Description string `yaml:"description"`
			} `yaml:"fields"`
		} `yaml:"configuration"`
	}
	require.NoError(t, yaml.Unmarshal(data, &tr))
	group, ok := tr.Configuration[optimizerGroup]
	require.True(t, ok, "translations must have configuration.optimizer")
	assert.NotEmpty(t, group.Name)

	for k := range optSchema {
		f, ok := group.Fields[k]
		if assert.True(t, ok, "optimizer.%s has no translation", k) {
			assert.NotEmpty(t, f.Name, "optimizer.%s name", k)
			assert.NotEmpty(t, f.Description, "optimizer.%s description", k)
		}
	}
	for k := range group.Fields {
		assert.Contains(t, optSchema, k, "translation for unknown option optimizer.%s", k)
	}
	for k := range tr.Configuration {
		assert.False(t, optCfgIsOptimizeFlag(k), "flat translation key %s belongs under optimizer.fields", k)
	}
}

// pvAddonKeys are the SPEC-0003 add-on options (UX §2.2).
var pvAddonKeys = []string{"pv_source", "pv_entity_today", "pv_entity_tomorrow", "pv_entity_last_polled"}

// pvAddonDefaults are the option defaults from UX §2.1/§2.2. Wave 2 registers
// the flags with the same defaults; TestAddonPVFlagParity_T0323 compares them.
var pvAddonDefaults = map[string]string{
	"pv_source":             "solcast_api",
	"pv_entity_today":       "sensor.solcast_pv_forecast_forecast_today",
	"pv_entity_tomorrow":    "sensor.solcast_pv_forecast_forecast_tomorrow",
	"pv_entity_last_polled": "sensor.solcast_pv_forecast_api_last_polled",
}

// T-0323 (AC-11, S-027 TC-027e, UX §2.2): pv_source is a required list with
// the solcast_api default, the three entity regexes accept the defaults and ""
// and reject non-sensor, upper-case and path-like IDs, and url/apikey became
// optional so pv_source=ha_solcast users can save the form without them.
func TestAddonSchema_PVSource_T0323(t *testing.T) {
	cfg := loadAddonConfig(t)

	src, ok := cfg.Schema["pv_source"].(string)
	require.True(t, ok, "pv_source schema missing")
	require.True(t, strings.HasPrefix(src, "list(") && strings.HasSuffix(src, ")"), "pv_source must be a required list(...), got %q", src)
	assert.ElementsMatch(t, []string{"solcast_api", "ha_solcast"}, strings.Split(src[len("list("):len(src)-1], "|"))

	for _, k := range pvAddonKeys {
		assert.Equal(t, pvAddonDefaults[k], cfg.Options[k], "options default for %s", k)
	}

	assert.Equal(t, "str?", cfg.Schema["url"], "url must be an optional string")
	assert.Equal(t, "password?", cfg.Schema["apikey"], "apikey must stay a password, now optional")
	assert.Contains(t, cfg.Options, "url", "url placeholder stays visible for solcast_api users")

	for _, k := range pvAddonKeys[1:] {
		t.Run(k, func(t *testing.T) {
			re := addonSchemaRegex(t, cfg.Schema, k)
			for _, s := range []string{pvAddonDefaults[k], "", "sensor.solcast_pv_forecast_prognose_heute", "sensor.a1_b2"} {
				assert.True(t, re.MatchString(s), "expected %q to match", s)
			}
			for _, s := range []string{"light.x", "sensor.Upper", "sensor.a/b", "sensor.x/../../config", "sensor.", "binary_sensor.x", " sensor.x", "sensor.x ", "sensor.a-b"} {
				assert.False(t, re.MatchString(s), "expected %q to not match", s)
			}
		})
	}
}

// T-0323 (UX §2.3): every SPEC-0003 add-on option, plus url and apikey, has a
// name and description in translations/en.yaml, and ha_url mentions
// ha_solcast now that it is used outside optimize mode.
func TestAddonTranslations_PVSource_T0323(t *testing.T) {
	data, err := os.ReadFile("../../home-assistant/addons/sbam/translations/en.yaml")
	require.NoError(t, err)
	type entry struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	var tr struct {
		Configuration map[string]struct {
			Name        string           `yaml:"name"`
			Description string           `yaml:"description"`
			Fields      map[string]entry `yaml:"fields"`
		} `yaml:"configuration"`
	}
	require.NoError(t, yaml.Unmarshal(data, &tr))

	cfg := loadAddonConfig(t)
	for _, k := range append([]string{"url", "apikey"}, pvAddonKeys...) {
		t.Run(k, func(t *testing.T) {
			require.Contains(t, cfg.Schema, k, "translated key must exist in the add-on schema")
			e, ok := tr.Configuration[k]
			require.True(t, ok, "missing translation entry")
			assert.NotEmpty(t, strings.TrimSpace(e.Name))
			assert.NotEmpty(t, strings.TrimSpace(e.Description))
		})
	}
	assert.Contains(t, tr.Configuration[optimizerGroup].Fields["ha_url"].Description, "ha_solcast")

	// No translation for a key the add-on does not have (a typo there is
	// silently ignored by the Supervisor).
	for k := range tr.Configuration {
		assert.Contains(t, cfg.Schema, k, "translation for unknown add-on key %s", k)
	}
}

// T-0323 (surface parity, AC-11): every pv_* add-on option is a flag on
// schedule, estimate and optimize with the same default, and every pv_* flag
// is an add-on option.
func TestAddonPVFlagParity_T0323(t *testing.T) {
	cfg := loadAddonConfig(t)
	cmds := map[string]*cobra.Command{"schedule": scdCmd, "estimate": estCmd, "optimize": optCmd}

	for _, k := range pvAddonKeys {
		require.Contains(t, cfg.Schema, k)
		require.Contains(t, cfg.Options, k)
		for name, c := range cmds {
			t.Run(name+"/"+k, func(t *testing.T) {
				f := c.Flags().Lookup(k)
				require.NotNil(t, f, "add-on option %s has no flag on %s", k, name)
				assert.Equal(t, addonOptionString(cfg.Options[k]), f.DefValue, "options default differs from flag default")
				assert.Empty(t, f.Shorthand, "pv_* flags are long-only")
			})
		}
	}

	t.Run("no unknown pv_ add-on keys", func(t *testing.T) {
		known := map[string]bool{}
		for _, k := range pvAddonKeys {
			known[k] = true
		}
		for k := range cfg.Schema {
			if strings.HasPrefix(k, "pv_") {
				assert.True(t, known[k], "schema key %s is not a SPEC-0003 key", k)
			}
		}
		for _, c := range cmds {
			c.Flags().VisitAll(func(f *pflag.Flag) {
				if strings.HasPrefix(f.Name, "pv_") {
					assert.True(t, known[f.Name], "flag %s on %s is not an add-on option", f.Name, c.Name())
				}
			})
		}
	})
}
