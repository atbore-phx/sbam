package fronius

import (
	"testing"

	u "sbam/src/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

type sunspecModel struct {
	id     uint16
	length uint16
}

// Model chains as reported by a Fronius Gen24 with four MPPT trackers.
var intSFChain = []sunspecModel{
	{1, 65}, {103, 50}, {120, 26}, {121, 30}, {122, 44}, {123, 24}, {160, 88}, {124, 24},
}

var floatChain = []sunspecModel{
	{1, 65}, {113, 60}, {120, 26}, {121, 30}, {122, 44}, {123, 24}, {160, 88}, {124, 24},
}

const floatStorageModelAddr = 40354

// Legacy int+SF addresses that fall inside the MPPT model in float mode.
var legacyAddrs = []uint16{StorCtl_Mod, MinRsvPct, OutWRte, InWRte}

func setHolding(address uint16, value uint16) {
	mockServer.HoldingRegisters[address-1] = value
}

func getHolding(address uint16) int16 {
	return int16(mockServer.HoldingRegisters[address-1])
}

func snapshot() []uint16 {
	return append([]uint16(nil), mockServer.HoldingRegisters...)
}

// seedSunSpec writes the "SunS" marker and the model headers of chain into
// the mock server, terminated by the end model. Model 124 gets the
// InOutWRte_SF a Gen24 reports (-2).
func seedSunSpec(chain []sunspecModel) {
	setHolding(sunspecMarkerAddr, sunspecMarkerHi)
	setHolding(sunspecMarkerAddr+1, sunspecMarkerLo)
	addr := uint16(sunspecFirstModelAddr)
	for _, m := range chain {
		setHolding(addr, m.id)
		setHolding(addr+1, m.length)
		if m.id == sunspecStorageModelID && m.length >= storageModelLen {
			setHolding(addr+offInOutWRteSF, uint16(0xFFFE)) // -2
		}
		addr += 2 + m.length
	}
	setHolding(addr, sunspecEndModelID)
	setHolding(addr+1, 0)
}

func discover(t *testing.T, st SunSpecType) (StorageRegisters, error) {
	t.Helper()
	require.NoError(t, OpenModbusClient("tcp", modbus_ip, modbus_port))
	defer ClosemodbusClient()
	return DiscoverStorageRegisters(st)
}

// observeLogs routes u.Log to an in-memory observer for one test.
func observeLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	previous := u.Log
	core, logs := observer.New(zap.DebugLevel)
	u.Log = zap.New(core).Sugar()
	t.Cleanup(func() { u.Log = previous })
	return logs
}

func TestParseSunSpecType(t *testing.T) {
	cases := map[string]SunSpecType{
		"":         SunSpecIntSF,
		"int_sf":   SunSpecIntSF,
		"float":    SunSpecFloat,
		" AUTO ":   SunSpecAuto,
		"Float":    SunSpecFloat,
		"  int_sf": SunSpecIntSF,
	}
	for in, want := range cases {
		got, err := ParseSunSpecType(in)
		assert.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	_, err := ParseSunSpecType("int+sf")
	assert.ErrorContains(t, err, "invalid sunspec_type")
}

func TestZeroSunSpecTypeIsIntSF(t *testing.T) {
	var zero SunSpecType
	assert.Equal(t, SunSpecIntSF, zero.orDefault())
	assert.Equal(t, SunSpecFloat, SunSpecFloat.orDefault())
	assert.Equal(t, SunSpecType(""), New().SunSpecType, "New keeps the legacy default")
}

func TestDiscoverStorageRegistersIntSFMatchesLegacyConstants(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(intSFChain)

	regs, err := discover(t, SunSpecAuto)

	require.NoError(t, err)
	assert.Equal(t, StorageRegisters{
		StorCtlMod: StorCtl_Mod,
		MinRsvPct:  MinRsvPct,
		OutWRte:    OutWRte,
		InWRte:     InWRte,
		ChaGriSet:  ChaGriSet,
	}, regs)
}

func TestDiscoverStorageRegistersFloat(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)

	regs, err := discover(t, SunSpecFloat)

	require.NoError(t, err)
	assert.Equal(t, StorageRegisters{
		StorCtlMod: 40359,
		MinRsvPct:  40361,
		OutWRte:    40366,
		InWRte:     40367,
		ChaGriSet:  40371,
	}, regs)
}

func TestDiscoverStorageRegistersWithoutMarker(t *testing.T) {
	setup()
	defer teardown()

	regs, err := discover(t, SunSpecAuto)
	assert.NoError(t, err)
	assert.Equal(t, uint16(StorCtl_Mod), regs.StorCtlMod, "auto falls back to legacy addresses")

	_, err = discover(t, SunSpecFloat)
	assert.ErrorIs(t, err, ErrSunSpecMarkerNotFound, "float requires the marker")
}

func TestDiscoverStorageRegistersMissingModel(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain[:len(floatChain)-1])

	_, err := discover(t, SunSpecFloat)

	assert.ErrorIs(t, err, ErrStorageModelNotFound)
}

func TestDiscoverStorageRegistersRejectsUnexpectedLayout(t *testing.T) {
	t.Run("short model", func(t *testing.T) {
		setup()
		defer teardown()
		chain := append([]sunspecModel(nil), floatChain...)
		chain[len(chain)-1] = sunspecModel{124, 16}
		seedSunSpec(chain)

		_, err := discover(t, SunSpecAuto)

		assert.ErrorIs(t, err, ErrUnexpectedStorageModel)
		assert.ErrorContains(t, err, "length 16")
	})

	t.Run("InOutWRte_SF", func(t *testing.T) {
		setup()
		defer teardown()
		seedSunSpec(floatChain)
		setHolding(floatStorageModelAddr+offInOutWRteSF, 0) // scale factor 0 instead of -2

		_, err := discover(t, SunSpecFloat)

		assert.ErrorIs(t, err, ErrUnexpectedStorageModel)
		assert.ErrorContains(t, err, "InOutWRte_SF")
	})
}

// The discovery path on an int+SF inverter must produce exactly the same
// register writes as the untouched legacy implementation.
func TestDiscoveryMatchesLegacyWritesOnIntSF(t *testing.T) {
	run := func(t *testing.T, write func() error) []uint16 {
		t.Helper()
		setup()
		defer teardown()
		seedSunSpec(intSFChain)
		require.NoError(t, write())
		return snapshot()
	}
	for _, pct := range []int16{0, 1, 48, 100} {
		legacy := run(t, func() error { return ForceCharge(modbus_ip, pct, modbus_port) })
		discovered := run(t, func() error { return ApplyForceCharge(SunSpecAuto, modbus_ip, pct, modbus_port) })
		assert.Equal(t, legacy, discovered, "force charge %d%%", pct)
	}
	legacy := run(t, func() error { return Setdefaults(modbus_ip, modbus_port) })
	discovered := run(t, func() error { return ApplyDefaults(SunSpecAuto, modbus_ip, modbus_port) })
	assert.Equal(t, legacy, discovered, "defaults")
}

func TestApplyForceChargeIntSFUsesLegacyAddresses(t *testing.T) {
	for _, st := range []SunSpecType{SunSpecIntSF, ""} {
		t.Run(string(st), func(t *testing.T) {
			setup()
			defer teardown()
			seedSunSpec(floatChain)

			err := ApplyForceCharge(st, modbus_ip, 50, modbus_port)

			assert.NoError(t, err)
			assert.Equal(t, int16(2), getHolding(StorCtl_Mod), "int_sf keeps writing the legacy StorCtl_Mod")
			assert.Equal(t, int16(-5000), getHolding(OutWRte), "int_sf keeps writing the legacy OutWRte")
			assert.Equal(t, int16(0), getHolding(40359), "discovered StorCtl_Mod untouched")
		})
	}
}

func TestApplyForceChargeFloatWritesDiscoveredRegisters(t *testing.T) {
	for _, st := range []SunSpecType{SunSpecFloat, SunSpecAuto} {
		t.Run(string(st), func(t *testing.T) {
			setup()
			defer teardown()
			seedSunSpec(floatChain)

			err := ApplyForceCharge(st, modbus_ip, 50, modbus_port)

			assert.NoError(t, err)
			assert.Equal(t, int16(2), getHolding(40359), "StorCtl_Mod")
			assert.Equal(t, int16(-5000), getHolding(40366), "OutWRte")
			assert.Equal(t, int16(10000), getHolding(40367), "InWRte")
			assert.Equal(t, int16(0), getHolding(40361), "MinRsvPct")
			assert.Equal(t, int16(1), getHolding(40371), "ChaGriSet")
			for _, addr := range legacyAddrs {
				assert.Equal(t, int16(0), getHolding(addr), "legacy address %d", addr)
			}
		})
	}
}

func TestApplyForceChargeCapsAt100(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)

	err := ApplyForceCharge(SunSpecFloat, modbus_ip, 117, modbus_port)

	assert.NoError(t, err)
	assert.Equal(t, int16(-10000), getHolding(40366), "OutWRte capped at -100 %")
}

func TestApplyForceChargeZeroSetsDefaults(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)
	setHolding(40359, 2)
	setHolding(40366, uint16(0xEC78)) // -5000

	err := ApplyForceCharge(SunSpecFloat, modbus_ip, 0, modbus_port)

	assert.NoError(t, err)
	assert.Equal(t, int16(0), getHolding(40359), "StorCtl_Mod")
	assert.Equal(t, int16(10000), getHolding(40366), "OutWRte")
}

func TestApplyForceChargeNegative(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)
	before := snapshot()

	err := ApplyForceCharge(SunSpecFloat, modbus_ip, -1, modbus_port)

	assert.ErrorContains(t, err, "negative")
	assert.Equal(t, before, mockServer.HoldingRegisters)
}

func TestApplyForceChargeWritesNothingOnBadModel(t *testing.T) {
	t.Run("missing model 124", func(t *testing.T) {
		setup()
		defer teardown()
		seedSunSpec(floatChain[:len(floatChain)-1])
		before := snapshot()

		err := ApplyForceCharge(SunSpecFloat, modbus_ip, 50, modbus_port)

		assert.ErrorIs(t, err, ErrStorageModelNotFound)
		assert.Equal(t, before, mockServer.HoldingRegisters)
	})
	t.Run("unexpected scale factor", func(t *testing.T) {
		setup()
		defer teardown()
		seedSunSpec(floatChain)
		setHolding(floatStorageModelAddr+offInOutWRteSF, 0)
		before := snapshot()

		err := ApplyForceCharge(SunSpecFloat, modbus_ip, 50, modbus_port)

		assert.ErrorIs(t, err, ErrUnexpectedStorageModel)
		assert.Equal(t, before, mockServer.HoldingRegisters)
	})
}

func TestApplyForceChargeUnreachable(t *testing.T) {
	err := ApplyForceCharge(SunSpecFloat, modbus_ip, 50, "6599")

	assert.Error(t, err)
}

func TestApplyDefaultsFloatWritesDiscoveredRegisters(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)
	setHolding(40359, 2)

	err := ApplyDefaults(SunSpecFloat, modbus_ip, modbus_port)

	assert.NoError(t, err)
	assert.Equal(t, int16(0), getHolding(40359), "StorCtl_Mod")
	assert.Equal(t, int16(10000), getHolding(40366), "OutWRte")
	assert.Equal(t, int16(0), getHolding(OutWRte), "legacy OutWRte address")
}

func TestApplyDefaultsIntSFUsesLegacyAddresses(t *testing.T) {
	setup()
	defer teardown()

	err := ApplyDefaults(SunSpecIntSF, modbus_ip, modbus_port)

	assert.NoError(t, err)
	assert.Equal(t, int16(10000), getHolding(OutWRte))
	assert.Equal(t, int16(1), getHolding(ChaGriSet))
}

func TestApplyDefaultsFloatWithoutMarkerWritesNothing(t *testing.T) {
	setup()
	defer teardown()
	before := snapshot()

	err := ApplyDefaults(SunSpecFloat, modbus_ip, modbus_port)

	assert.ErrorIs(t, err, ErrSunSpecMarkerNotFound)
	assert.Equal(t, before, mockServer.HoldingRegisters)
}

func TestReadStorageControlMode(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)
	setHolding(40359, 2)
	setHolding(StorCtl_Mod, 0)

	value, err := ReadStorageControlMode(SunSpecIntSF, modbus_ip, modbus_port)
	assert.NoError(t, err)
	assert.Equal(t, int16(0), value, "int_sf reads the legacy address")

	value, err = ReadStorageControlMode(SunSpecFloat, modbus_ip, modbus_port)
	assert.NoError(t, err)
	assert.Equal(t, int16(2), value, "float reads the discovered address")
}

func TestReadStorageControlModeErrors(t *testing.T) {
	_, err := ReadStorageControlMode(SunSpecFloat, modbus_ip, "6599")
	assert.Error(t, err, "unreachable inverter")

	setup()
	defer teardown()
	seedSunSpec(floatChain[:len(floatChain)-1])
	_, err = ReadStorageControlMode(SunSpecFloat, modbus_ip, modbus_port)
	assert.ErrorIs(t, err, ErrStorageModelNotFound)
}

// The Fronius handler must pass its SunSpec type down to the battery writes.
func TestHandlerUsesConfiguredSunSpecType(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)
	f := New()
	f.SunSpecType = SunSpecFloat

	// Same inputs as TestSetFroniusChargeBatteryMode cases that force-charge 31 %.
	pct, _, _, _, err := f.Handler(1000, 11000, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, modbus_port)

	require.NoError(t, err)
	assert.Equal(t, int16(31), pct)
	assert.Equal(t, int16(2), getHolding(40359), "discovered StorCtl_Mod")
	assert.Equal(t, int16(-3100), getHolding(40366), "discovered OutWRte")
	for _, addr := range legacyAddrs {
		assert.Equal(t, int16(0), getHolding(addr), "legacy address %d", addr)
	}
}

func TestDetectSunSpecType(t *testing.T) {
	setup()
	defer teardown()

	seedSunSpec(intSFChain)
	got, err := DetectSunSpecType(modbus_ip, modbus_port)
	assert.NoError(t, err)
	assert.Equal(t, SunSpecIntSF, got)

	seedSunSpec(floatChain)
	got, err = DetectSunSpecType(modbus_ip, modbus_port)
	assert.NoError(t, err)
	assert.Equal(t, SunSpecFloat, got)
}

func TestDetectSunSpecTypeErrors(t *testing.T) {
	_, err := DetectSunSpecType(modbus_ip, "6599")
	assert.Error(t, err, "unreachable inverter")

	setup()
	defer teardown()

	_, err = DetectSunSpecType(modbus_ip, modbus_port)
	assert.ErrorIs(t, err, ErrSunSpecMarkerNotFound)

	seedSunSpec([]sunspecModel{{1, 65}, {120, 26}, {124, 24}})
	_, err = DetectSunSpecType(modbus_ip, modbus_port)
	assert.ErrorIs(t, err, ErrInverterModelNotFound)
}

func TestVerifySunSpecType(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)

	cases := []struct {
		name       string
		configured SunSpecType
		port       string
		level      zapcore.Level
		message    string
	}{
		{"mismatch", SunSpecIntSF, modbus_port, zapcore.WarnLevel,
			"sunspec_type=int_sf but the inverter reports SunSpec model type float: battery control will not work as expected, set sunspec_type to float or auto"},
		{"match", SunSpecFloat, modbus_port, zapcore.InfoLevel,
			"sunspec_type=float matches the inverter SunSpec model type"},
		{"auto", SunSpecAuto, modbus_port, zapcore.InfoLevel,
			"sunspec_type=auto: inverter reports SunSpec model type float"},
		{"detection failure", SunSpecIntSF, "6599", zapcore.WarnLevel,
			"could not verify the inverter SunSpec model type (sunspec_type=int_sf)"},
	}

	// Read-only: none of the outcomes may write to the inverter.
	before := snapshot()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := observeLogs(t)

			VerifySunSpecType(tc.configured, modbus_ip, tc.port)

			matched := logs.FilterLevelExact(tc.level).FilterMessageSnippet(tc.message).All()
			assert.Len(t, matched, 1, "expected %s log %q, got %v", tc.level, tc.message, logs.All())
			if tc.level != zapcore.WarnLevel {
				assert.Empty(t, logs.FilterLevelExact(zapcore.WarnLevel).All(), "no warning on %s", tc.name)
			}
		})
	}
	assert.Equal(t, before, mockServer.HoldingRegisters)
}
