package cmd

import (
	"os"
	"regexp"
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
