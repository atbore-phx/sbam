package fronius_test

import (
	"sbam/pkg/fronius"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyDecision(t *testing.T) {
	cases := []struct {
		name                     string
		pwBatt2charge            float64
		pwForecast               float64
		pwConsumption            float64
		pwBattMax                float64
		pwBattReserve            float64
		pwLwt                    float64
		forecastChargeEnabled    bool
		battReserveChargeEnabled bool
		expectedDecision         fronius.Decision
		expectedReason           fronius.Reason
	}{
		{
			name:                     "battery full short-circuits regardless of other inputs",
			pwBatt2charge:            0,
			pwForecast:               1000,
			pwConsumption:            500,
			pwBattMax:                5000,
			pwBattReserve:            1000,
			pwLwt:                    100,
			forecastChargeEnabled:    true,
			battReserveChargeEnabled: true,
			expectedDecision:         fronius.DecisionBatteryFull,
			expectedReason:           fronius.ReasonBatteryFull,
		},
		{
			name:                     "forecast charge fires when net power is below -lwt and forecast enabled",
			pwBatt2charge:            2000,
			pwForecast:               100,
			pwConsumption:            5000,
			pwBattMax:                5000,
			pwBattReserve:            500,
			pwLwt:                    100,
			forecastChargeEnabled:    true,
			battReserveChargeEnabled: false,
			expectedDecision:         fronius.DecisionForecastCharge,
			expectedReason:           fronius.ReasonForecastCharge,
		},
		{
			name:                     "reserve charge fires when battery is below reserve and reserve charge enabled",
			pwBatt2charge:            4000,
			pwForecast:               5000,
			pwConsumption:            100,
			pwBattMax:                5000,
			pwBattReserve:            3000,
			pwLwt:                    100,
			forecastChargeEnabled:    false,
			battReserveChargeEnabled: true,
			expectedDecision:         fronius.DecisionReserveCharge,
			expectedReason:           fronius.ReasonReserveCharge,
		},
		{
			name:                     "idle when net power is fine and reserve is satisfied",
			pwBatt2charge:            500,
			pwForecast:               5000,
			pwConsumption:            100,
			pwBattMax:                5000,
			pwBattReserve:            1000,
			pwLwt:                    100,
			forecastChargeEnabled:    true,
			battReserveChargeEnabled: true,
			expectedDecision:         fronius.DecisionIdle,
			expectedReason:           fronius.ReasonIdle,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotDecision, gotReason, _, err := fronius.ClassifyDecision(
				tc.pwBatt2charge, tc.pwForecast, tc.pwConsumption, tc.pwBattMax,
				tc.pwBattReserve, tc.pwLwt,
				tc.forecastChargeEnabled, tc.battReserveChargeEnabled,
			)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedDecision, gotDecision)
			assert.Equal(t, tc.expectedReason, gotReason)
		})
	}

	t.Run("returns skip and error for unexpected power state", func(t *testing.T) {
		decision, reason, _, err := fronius.ClassifyDecision(
			2000, 100, 5000, 5000,
			1000, 100,
			false, false,
		)

		assert.Error(t, err)
		assert.Equal(t, fronius.DecisionSkip, decision)
		assert.Contains(t, reason, "unexpected power state")
	})

	t.Run("returns power state snapshot", func(t *testing.T) {
		// updated signature adds an error return; ensure it's nil
		_, _, gotPowerState, err := fronius.ClassifyDecision(
			500, 5000, 100, 5000,
			1000, 100,
			true, true,
		)
		assert.NoError(t, err)

		// SoC is provided by the storage package; classifier does not set it.
		assert.Equal(t, fronius.PowerState{
			PvNet:          4900,
			Batt:           4500,
			Net:            9400,
			BattReserveNet: 3500,
		}, gotPowerState)
	})
}
