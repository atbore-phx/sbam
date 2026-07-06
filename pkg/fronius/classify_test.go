package fronius

import (
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
		expectedDecision         Decision
		expectedReason           Reason
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
			expectedDecision:         decisionBatteryFull,
			expectedReason:           reasonBatteryFull,
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
			expectedDecision:         decisionForecastCharge,
			expectedReason:           reasonForecastCharge,
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
			expectedDecision:         decisionReserveCharge,
			expectedReason:           reasonReserveCharge,
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
			expectedDecision:         DecisionIdle,
			expectedReason:           reasonIdle,
		},
		{
			name:                     "idle with forecast-disabled reason when forecast is off and net power is negative",
			pwBatt2charge:            2000,
			pwForecast:               100,
			pwConsumption:            5000,
			pwBattMax:                5000,
			pwBattReserve:            1000,
			pwLwt:                    100,
			forecastChargeEnabled:    false,
			battReserveChargeEnabled: false,
			expectedDecision:         DecisionIdle,
			expectedReason:           reasonForecastDisabled,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotDecision, gotReason, _, err := classifyDecision(
				tc.pwBatt2charge, tc.pwForecast, tc.pwConsumption, tc.pwBattMax,
				tc.pwBattReserve, tc.pwLwt,
				tc.forecastChargeEnabled, tc.battReserveChargeEnabled,
			)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedDecision, gotDecision)
			assert.Equal(t, tc.expectedReason, gotReason)
		})
	}

	t.Run("returns power state snapshot", func(t *testing.T) {
		// updated signature adds an error return; ensure it's nil
		_, _, gotPowerState, err := classifyDecision(
			500, 5000, 100, 5000,
			1000, 100,
			true, true,
		)
		assert.NoError(t, err)

		// SoC is provided by the storage package; classifier does not set it.
		assert.Equal(t, PowerState{
			PvNet:          4900,
			Batt:           4500,
			Net:            9400,
			BattReserveNet: 3500,
		}, gotPowerState)
	})
}
