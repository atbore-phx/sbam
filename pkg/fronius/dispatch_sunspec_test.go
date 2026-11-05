package fronius

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Model 124 addresses with sunspec_type float on a Gen24 (floatChain): every
// storage register sits 10 registers above its int+SF position. floatOutWRte
// and floatInOutWRteSF are declared in faults_test.go.
const (
	floatWChaMax    = floatStorageModelAddr + offWChaMax          // 40356
	floatStorCtlMod = floatStorageModelAddr + offStorCtlMod       // 40359
	floatMinRsvPct  = floatStorageModelAddr + offMinRsvPct        // 40361
	floatInWRte     = floatStorageModelAddr + offInWRte           // 40367
	floatRvrtTms    = floatStorageModelAddr + offInOutWRteRvrtTms // 40369
	floatChaGriSet  = floatStorageModelAddr + offChaGriSet        // 40371
	floatWChaMaxSF  = floatStorageModelAddr + offWChaMaxSF        // 40372
)

// seedFloatInverter seeds the float model chain with WChaMax = 5000 W.
func seedFloatInverter() {
	seedSunSpec(floatChain)
	setHolding(floatWChaMax, 5000)
	setHolding(floatWChaMaxSF, 0)
}

// assertLegacyStorageUntouched checks that the int+SF control registers that
// do not overlap the float storage model were not written.
func assertLegacyStorageUntouched(t *testing.T) {
	t.Helper()
	for _, addr := range []uint16{StorCtl_Mod, MinRsvPct, InWRte} {
		assert.Equal(t, int16(0), getHolding(addr), "legacy address %d", addr)
	}
	// Legacy OutWRte (40356) is the float WChaMax: it must keep its value.
	assert.Equal(t, int16(5000), getHolding(floatWChaMax), "float WChaMax must never be written")
}

// The legacy register set equals the int+SF constants the dispatch path used
// before sunspec_type existed, so int_sf behaviour is unchanged.
func TestLegacyStorageRegistersMatchConstants(t *testing.T) {
	assert.Equal(t, StorageRegisters{
		StorCtlMod:       StorCtl_Mod,
		MinRsvPct:        MinRsvPct,
		OutWRte:          OutWRte,
		InWRte:           InWRte,
		ChaGriSet:        ChaGriSet,
		WChaMax:          WChaMax,
		InOutWRteRvrtTms: InOutWRte_RvrtTms,
		WChaMaxSF:        WChaMax_SF,
		InOutWRteSF:      InOutWRte_SF,
	}, LegacyStorageRegisters())
}

func TestBuildDispatchRegistersAtFloatAddresses(t *testing.T) {
	r := storageRegistersAt(floatStorageModelAddr)
	w, err := BuildDispatchRegistersAt(r, DispatchCommand{Mode: DispatchCharge, RateW: 2500}, validLimits)
	require.NoError(t, err)
	assert.Equal(t, []uint16{floatOutWRte, floatInWRte, floatChaGriSet, floatMinRsvPct, floatRvrtTms, floatStorCtlMod}, addrOrder(w))
	assert.Equal(t, int16(-5000), regsOf(w)[floatOutWRte])
	assert.Equal(t, int16(RevertSeconds), regsOf(w)[floatRvrtTms])
}

// Expected use: every optimizer action lands on the float storage model, the
// limits are read from it, and the legacy addresses stay untouched.
func TestApplyDispatchFloatWritesDiscoveredRegisters(t *testing.T) {
	cases := []struct {
		name    string
		cmd     DispatchCommand
		out     int16
		in      int16
		storCtl int16
		revert  bool
	}{
		{"self_consume", DispatchCommand{Mode: DispatchSelfConsume}, 10000, 10000, 0, false},
		{"charge 2500W", DispatchCommand{Mode: DispatchCharge, RateW: 2500}, -5000, 10000, 2, true},
		{"hold", DispatchCommand{Mode: DispatchHold}, 0, 10000, 2, true},
		{"standby", DispatchCommand{Mode: DispatchStandby}, 0, 0, 3, true},
		{"discharge 3000W", DispatchCommand{Mode: DispatchDischarge, RateW: 3000}, 10000, -6000, 1, true},
	}
	for _, st := range []SunSpecType{SunSpecFloat, SunSpecAuto} {
		for _, tc := range cases {
			t.Run(string(st)+"/"+tc.name, func(t *testing.T) {
				setup()
				defer teardown()
				seedFloatInverter()

				applied, err := ApplyDispatch(st, modbus_ip, tc.cmd, modbus_port)

				require.NoError(t, err)
				assert.Equal(t, validLimits, applied.Limits)
				assert.Equal(t, tc.storCtl, getHolding(floatStorCtlMod), "StorCtl_Mod")
				assert.Equal(t, tc.out, getHolding(floatOutWRte), "OutWRte")
				assert.Equal(t, tc.in, getHolding(floatInWRte), "InWRte")
				assert.Equal(t, int16(0), getHolding(floatMinRsvPct), "MinRsvPct")
				assert.Equal(t, int16(1), getHolding(floatChaGriSet), "ChaGriSet")
				if tc.revert {
					assert.Equal(t, int16(RevertSeconds), getHolding(floatRvrtTms), "InOutWRte_RvrtTms")
				} else {
					assert.Equal(t, int16(0), getHolding(floatRvrtTms), "InOutWRte_RvrtTms")
				}
				assert.Equal(t, int16(-2), getHolding(floatInOutWRteSF), "InOutWRte_SF is read only")
				assertLegacyStorageUntouched(t)
			})
		}
	}
}

func TestSetDefaultsOrderedFloat(t *testing.T) {
	setup()
	defer teardown()
	seedFloatInverter()
	setHolding(floatStorCtlMod, 2)
	setHolding(floatOutWRte, uint16(0xEC78)) // -5000

	require.NoError(t, SetDefaultsOrdered(SunSpecFloat, modbus_ip, modbus_port))

	assert.Equal(t, int16(0), getHolding(floatStorCtlMod))
	assert.Equal(t, int16(10000), getHolding(floatOutWRte))
	assert.Equal(t, int16(10000), getHolding(floatInWRte))
	assert.Equal(t, int16(1), getHolding(floatChaGriSet))
	assertLegacyStorageUntouched(t)
}

func TestForceChargeWithRevertFloat(t *testing.T) {
	setup()
	defer teardown()
	seedFloatInverter()

	revert := ForceChargeRevertSeconds(1800, time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC))
	require.NoError(t, ForceChargeWithRevert(SunSpecFloat, modbus_ip, 50, revert, modbus_port))

	assert.Equal(t, int16(2), getHolding(floatStorCtlMod))
	assert.Equal(t, int16(-5000), getHolding(floatOutWRte))
	assert.Equal(t, int16(revert), getHolding(floatRvrtTms))
	assertLegacyStorageUntouched(t)
}

func TestReadInverterLimitsFloat(t *testing.T) {
	setup()
	defer teardown()
	seedFloatInverter()

	lim, err := ReadInverterLimits(SunSpecFloat, modbus_ip, modbus_port)

	require.NoError(t, err)
	assert.Equal(t, validLimits, lim)
}

// Failure case: when the storage model cannot be located or validated nothing
// is written, not even the defaults fallback.
func TestApplyDispatchFloatWritesNothingOnBadModel(t *testing.T) {
	cmd := DispatchCommand{Mode: DispatchCharge, RateW: 2500}
	t.Run("missing model 124", func(t *testing.T) {
		setup()
		defer teardown()
		seedSunSpec(floatChain[:len(floatChain)-1])
		before := snapshot()

		_, err := ApplyDispatch(SunSpecFloat, modbus_ip, cmd, modbus_port)

		assert.ErrorIs(t, err, ErrStorageModelNotFound)
		assert.Equal(t, before, mockServer.HoldingRegisters)
	})
	t.Run("unexpected scale factor", func(t *testing.T) {
		setup()
		defer teardown()
		seedFloatInverter()
		setHolding(floatInOutWRteSF, 0)
		before := snapshot()

		_, err := ApplyDispatch(SunSpecFloat, modbus_ip, cmd, modbus_port)

		assert.ErrorIs(t, err, ErrUnexpectedStorageModel)
		assert.Equal(t, before, mockServer.HoldingRegisters)
	})
	t.Run("float without SunS marker", func(t *testing.T) {
		setup()
		defer teardown()
		before := snapshot()

		_, err := ApplyDispatch(SunSpecFloat, modbus_ip, cmd, modbus_port)
		assert.ErrorIs(t, err, ErrSunSpecMarkerNotFound)
		err = SetDefaultsOrdered(SunSpecFloat, modbus_ip, modbus_port)
		assert.ErrorIs(t, err, ErrSunSpecMarkerNotFound)
		err = ForceChargeWithRevert(SunSpecFloat, modbus_ip, 50, 600, modbus_port)
		assert.ErrorIs(t, err, ErrSunSpecMarkerNotFound)
		assert.Equal(t, before, mockServer.HoldingRegisters)
	})
}

// Edge case: auto without the SunS marker falls back to the int+SF addresses,
// like the legacy write path does.
func TestApplyDispatchAutoWithoutMarkerUsesLegacyAddresses(t *testing.T) {
	setup()
	defer teardown()
	setHolding(WChaMax, 5000)
	setHolding(WChaMax_SF, 0)
	setHolding(InOutWRte_SF, uint16(0xFFFE)) // -2

	applied, err := ApplyDispatch(SunSpecAuto, modbus_ip, DispatchCommand{Mode: DispatchCharge, RateW: 2500}, modbus_port)

	require.NoError(t, err)
	assert.Equal(t, validLimits, applied.Limits)
	assert.Equal(t, int16(2), getHolding(StorCtl_Mod))
	assert.Equal(t, int16(-5000), getHolding(OutWRte))
	assert.Equal(t, int16(RevertSeconds), getHolding(InOutWRte_RvrtTms))
}
