package cmd

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"sbam/pkg/optimize"

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

// addonOptimizer returns the optimizer: map of the add-on schema and options,
// with nested maps (static:) flattened to dotted keys.
func addonOptimizer(t *testing.T, cfg addonConfig) (schema, options map[string]any) {
	t.Helper()
	schema, ok := cfg.Schema[optimizerGroup].(map[string]any)
	require.True(t, ok, "schema.optimizer must be a map")
	options, ok = cfg.Options[optimizerGroup].(map[string]any)
	require.True(t, ok, "options.optimizer must be a map")
	return addonFlatten(schema), addonFlatten(options)
}

func addonFlatten(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		sub, ok := v.(map[string]any)
		if !ok {
			out[k] = v
			continue
		}
		for sk, sv := range addonFlatten(sub) {
			out[k+"."+sk] = sv
		}
	}
	return out
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

// The optimizer.static price schema regex accepts an empty value or exactly
// 24 numbers in [-5, 50], and everything it accepts parseHourly accepts too.
func TestStaticPriceSchemaRegex(t *testing.T) {
	cfg := loadAddonConfig(t)
	optSchema, optOptions := addonOptimizer(t, cfg)
	parse := func(s string) error {
		_, _, err := parseHourly(keyOptStaticBuyPrice, s, optimize.MinPriceKWh, optimize.MaxPriceKWh)
		return err
	}

	for _, name := range []string{"static.buy_price", "static.sell_price"} {
		re := addonSchemaRegex(t, optSchema, name)
		pass := map[string]string{
			"unset":                "",
			"three-band buy":       optCfgTestBuy,
			"three-band sell":      optCfgTestSell,
			"spaces around commas": surchargeCSV(" , ", surchargeRepeat("0.5", 24)...),
			"range bounds":         surchargeCSV(",", append(surchargeRepeat("-5", 12), surchargeRepeat("50", 12)...)...),
			"bounds with decimals": surchargeCSV(",", append(surchargeRepeat("-5.0", 12), surchargeRepeat("50.00", 12)...)...),
			"options default":      addonOptionString(optOptions[name]),
		}
		for label, s := range pass {
			t.Run(name+"/pass/"+label, func(t *testing.T) {
				assert.True(t, re.MatchString(s), "expected %q to match", s)
				assert.NoError(t, parse(s), "regex accepted %q but parseHourly rejected it", s)
			})
		}

		fail := map[string]string{
			"23 values":        surchargeCSV(",", surchargeRepeat("0", 23)...),
			"25 values":        surchargeCSV(",", surchargeRepeat("0", 25)...),
			"only spaces":      "   ",
			"trailing comma":   surchargeCSV(",", surchargeRepeat("0", 24)...) + ",",
			"above max 50.1":   "50.1," + surchargeCSV(",", surchargeRepeat("0", 23)...),
			"above max 51":     "51," + surchargeCSV(",", surchargeRepeat("0", 23)...),
			"above max 100":    "100," + surchargeCSV(",", surchargeRepeat("0", 23)...),
			"below min -5.1":   "-5.1," + surchargeCSV(",", surchargeRepeat("0", 23)...),
			"below min -10":    "-10," + surchargeCSV(",", surchargeRepeat("0", 23)...),
			"non-numeric item": "abc," + surchargeCSV(",", surchargeRepeat("0", 23)...),
			"decimal comma":    surchargeCSV(";", surchargeRepeat("0,5", 24)...),
		}
		for label, s := range fail {
			t.Run(name+"/fail/"+label, func(t *testing.T) {
				assert.False(t, re.MatchString(s), "expected %q to not match", s)
			})
		}

		t.Run(name+"/consistency sweep", func(t *testing.T) {
			accepted := 0
			for i := -24; i <= 204; i++ {
				f := float64(i) / 4
				for _, v := range []string{strconv.FormatFloat(f, 'f', -1, 64), strconv.FormatFloat(f, 'f', 2, 64)} {
					s := surchargeCSV(",", surchargeRepeat(v, 24)...)
					if !re.MatchString(s) {
						continue
					}
					accepted++
					assert.NoError(t, parse(s), "regex accepted %q but parseHourly rejected it", v)
				}
			}
			assert.Positive(t, accepted)
		})
	}

	t.Run("currency", func(t *testing.T) {
		re := addonSchemaRegex(t, optSchema, "static.currency")
		for _, c := range []string{"", "AUD", "EUR", "øre", "12345678"} {
			assert.True(t, re.MatchString(c), "expected %q to match", c)
			_, err := optCfgCurrency(c)
			assert.NoError(t, err, "regex accepted %q but optCfgCurrency rejected it", c)
		}
		for _, c := range []string{"A UD", " AUD", "123456789"} {
			assert.False(t, re.MatchString(c), "expected %q to not match", c)
		}
	})

	t.Run("price source", func(t *testing.T) {
		assert.Equal(t, "list("+optPriceSourceNordpool+"|"+optPriceSourceStatic+")", optSchema["price_source"])
		assert.Equal(t, defOptPriceSource, optOptions["price_source"], "existing installs keep nordpool")
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

// addonTranslation is one entry of the add-on translations; a nested map of
// options (static:) carries its own fields.
type addonTranslation struct {
	Name        string                      `yaml:"name"`
	Description string                      `yaml:"description"`
	Fields      map[string]addonTranslation `yaml:"fields"`
}

// addonTranslationLeaves flattens nested fields to dotted keys and checks
// that every group on the way has a name.
func addonTranslationLeaves(t *testing.T, prefix string, fields map[string]addonTranslation) map[string]addonTranslation {
	t.Helper()
	out := map[string]addonTranslation{}
	for k, f := range fields {
		if len(f.Fields) == 0 {
			out[prefix+k] = f
			continue
		}
		assert.NotEmpty(t, f.Name, "optimizer.%s%s name", prefix, k)
		for sk, sf := range addonTranslationLeaves(t, prefix+k+".", f.Fields) {
			out[sk] = sf
		}
	}
	return out
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
		Configuration map[string]addonTranslation `yaml:"configuration"`
	}
	require.NoError(t, yaml.Unmarshal(data, &tr))
	group, ok := tr.Configuration[optimizerGroup]
	require.True(t, ok, "translations must have configuration.optimizer")
	assert.NotEmpty(t, group.Name)
	fields := addonTranslationLeaves(t, "", group.Fields)

	for k := range optSchema {
		f, ok := fields[k]
		if assert.True(t, ok, "optimizer.%s has no translation", k) {
			assert.NotEmpty(t, f.Name, "optimizer.%s name", k)
			assert.NotEmpty(t, f.Description, "optimizer.%s description", k)
		}
	}
	for k := range fields {
		assert.Contains(t, optSchema, k, "translation for unknown option optimizer.%s", k)
	}
	for k := range tr.Configuration {
		assert.False(t, optCfgIsOptimizeFlag(k), "flat translation key %s belongs under optimizer.fields", k)
	}
}
