package fronius

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyHoldIntSFWritesLegacyAddresses(t *testing.T) {
	setup()
	defer teardown()
	setHolding(OutWRte, uint16(0xEC78)) // -5000: a force charge in progress

	err := ApplyHold(SunSpecIntSF, modbus_ip, modbus_port)

	require.NoError(t, err)
	assert.Equal(t, int16(2), getHolding(StorCtl_Mod), "discharge limited")
	assert.Equal(t, int16(0), getHolding(OutWRte), "to 0 %")
	assert.Equal(t, int16(10000), getHolding(InWRte), "charging from PV still allowed")
	assert.Equal(t, int16(1), getHolding(ChaGriSet))
}

func TestApplyHoldFloatWritesDiscoveredRegisters(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)

	err := ApplyHold(SunSpecAuto, modbus_ip, modbus_port)

	require.NoError(t, err)
	assert.Equal(t, int16(2), getHolding(40359), "StorCtl_Mod")
	assert.Equal(t, int16(0), getHolding(40366), "OutWRte")
	assert.Equal(t, int16(10000), getHolding(40367), "InWRte")
	for _, addr := range legacyAddrs {
		assert.Equal(t, int16(0), getHolding(addr), "legacy address %d", addr)
	}
}

func TestApplyHoldWritesNothingWithoutModel(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain[:len(floatChain)-1])
	before := snapshot()

	err := ApplyHold(SunSpecFloat, modbus_ip, modbus_port)

	assert.ErrorIs(t, err, ErrStorageModelNotFound)
	assert.Equal(t, before, mockServer.HoldingRegisters)
}

func TestApplyHoldUnreachable(t *testing.T) {
	assert.Error(t, ApplyHold(SunSpecIntSF, modbus_ip, "6599"))
}

// With hold enabled, a tick that needs no charge holds the battery instead of
// returning it to defaults; a tick that needs a charge still force-charges.
func TestChargeModeHold(t *testing.T) {
	t.Run("idle holds", func(t *testing.T) {
		setup()
		defer teardown()

		// Plenty of forecast: idle.
		pct, decision, _, _, err := setFroniusChargeBatteryMode(SunSpecIntSF, true, 20000, 5000, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, modbus_port)

		require.NoError(t, err)
		assert.Equal(t, DecisionIdle, decision)
		assert.Equal(t, int16(0), pct)
		assert.Equal(t, int16(2), getHolding(StorCtl_Mod))
		assert.Equal(t, int16(0), getHolding(OutWRte))
	})
	t.Run("battery full holds", func(t *testing.T) {
		setup()
		defer teardown()

		_, decision, _, _, err := setFroniusChargeBatteryMode(SunSpecIntSF, true, 1000, 0, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, modbus_port)

		require.NoError(t, err)
		assert.Equal(t, decisionBatteryFull, decision)
		assert.Equal(t, int16(2), getHolding(StorCtl_Mod))
		assert.Equal(t, int16(0), getHolding(OutWRte))
	})
	t.Run("charge still forces", func(t *testing.T) {
		setup()
		defer teardown()

		pct, decision, _, _, err := setFroniusChargeBatteryMode(SunSpecIntSF, true, 1000, 11000, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, modbus_port)

		require.NoError(t, err)
		assert.Equal(t, decisionForecastCharge, decision)
		assert.Equal(t, int16(31), pct)
		assert.Equal(t, int16(-3100), getHolding(OutWRte))
	})
	t.Run("without hold idle restores defaults", func(t *testing.T) {
		setup()
		defer teardown()
		setHolding(StorCtl_Mod, 2)

		_, _, _, _, err := setFroniusChargeBatteryMode(SunSpecIntSF, false, 20000, 5000, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, modbus_port)

		require.NoError(t, err)
		assert.Equal(t, int16(0), getHolding(StorCtl_Mod))
		assert.Equal(t, int16(10000), getHolding(OutWRte))
	})
}

func TestHandlerPassesHold(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)
	f := New()
	f.SunSpecType = SunSpecFloat

	_, _, _, _, err := f.Handler(20000, 5000, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, true, modbus_port)

	require.NoError(t, err)
	assert.Equal(t, int16(2), getHolding(40359), "held at the discovered StorCtl_Mod")
	assert.Equal(t, int16(0), getHolding(40366))
}
