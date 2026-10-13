package fronius

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

// Legacy int+SF addresses that fall inside the MPPT model in float mode.
var legacyAddrs = []uint16{StorCtl_Mod, MinRsvPct, OutWRte, InWRte}

func setHolding(address uint16, value uint16) {
	mockServer.HoldingRegisters[address-1] = value
}

func getHolding(address uint16) int16 {
	return int16(mockServer.HoldingRegisters[address-1])
}

// seedSunSpec writes the "SunS" marker and the model headers of chain into
// the mock server, terminated by the end model.
func seedSunSpec(chain []sunspecModel) {
	setHolding(sunspecMarkerAddr, sunspecMarkerHi)
	setHolding(sunspecMarkerAddr+1, sunspecMarkerLo)
	addr := uint16(sunspecFirstModelAddr)
	for _, m := range chain {
		setHolding(addr, m.id)
		setHolding(addr+1, m.length)
		addr += 2 + m.length
	}
	setHolding(addr, sunspecEndModelID)
	setHolding(addr+1, 0)
}

// useSunSpecType sets the active type for one test and restores the default.
func useSunSpecType(t *testing.T, st SunSpecType) {
	t.Helper()
	SetSunSpecType(st)
	t.Cleanup(func() { SetSunSpecType(SunSpecIntSF) })
}

func discover(t *testing.T, st SunSpecType) (StorageRegisters, error) {
	t.Helper()
	assert.NoError(t, OpenModbusClient("tcp", modbus_ip, modbus_port))
	defer ClosemodbusClient()
	return DiscoverStorageRegisters(st)
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

func TestSunSpecTypeDefaultsToIntSF(t *testing.T) {
	assert.Equal(t, SunSpecIntSF, ActiveSunSpecType())
}

func TestDiscoverStorageRegistersIntSFMatchesLegacyConstants(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(intSFChain)

	regs, err := discover(t, SunSpecAuto)

	assert.NoError(t, err)
	assert.Equal(t, uint16(legacyStorageModelAddr), regs.ModelAddr)
	assert.Equal(t, uint16(StorCtl_Mod), regs.StorCtlMod)
	assert.Equal(t, uint16(OutWRte), regs.OutWRte)
	assert.Equal(t, uint16(InWRte), regs.InWRte)
	assert.Equal(t, uint16(MinRsvPct), regs.MinRsvPct)
	assert.Equal(t, uint16(ChaGriSet), regs.ChaGriSet)
	assert.Equal(t, uint16(WChaMax), regs.WChaMax)
}

func TestDiscoverStorageRegistersFloat(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)

	regs, err := discover(t, SunSpecFloat)

	assert.NoError(t, err)
	assert.Equal(t, uint16(40354), regs.ModelAddr)
	assert.Equal(t, uint16(40356), regs.WChaMax)
	assert.Equal(t, uint16(40359), regs.StorCtlMod)
	assert.Equal(t, uint16(40361), regs.MinRsvPct)
	assert.Equal(t, uint16(40366), regs.OutWRte)
	assert.Equal(t, uint16(40367), regs.InWRte)
	assert.Equal(t, uint16(40371), regs.ChaGriSet)
}

func TestDiscoverStorageRegistersWithoutMarker(t *testing.T) {
	setup()
	defer teardown()

	regs, err := discover(t, SunSpecAuto)
	assert.NoError(t, err)
	assert.Equal(t, uint16(legacyStorageModelAddr), regs.ModelAddr, "auto falls back to legacy addresses")

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

func TestApplyForceChargeIntSFUsesLegacyAddresses(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)

	err := ApplyForceCharge(modbus_ip, 50, modbus_port)

	assert.NoError(t, err)
	assert.Equal(t, int16(2), getHolding(StorCtl_Mod), "int_sf keeps writing the legacy StorCtl_Mod")
	assert.Equal(t, int16(-5000), getHolding(OutWRte), "int_sf keeps writing the legacy OutWRte")
	assert.Equal(t, int16(0), getHolding(40359), "discovered StorCtl_Mod untouched")
}

func TestApplyForceChargeFloatWritesDiscoveredRegisters(t *testing.T) {
	for _, st := range []SunSpecType{SunSpecFloat, SunSpecAuto} {
		t.Run(string(st), func(t *testing.T) {
			setup()
			defer teardown()
			useSunSpecType(t, st)
			seedSunSpec(floatChain)

			err := ApplyForceCharge(modbus_ip, 50, modbus_port)

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

func TestApplyForceChargeZeroSetsDefaults(t *testing.T) {
	setup()
	defer teardown()
	useSunSpecType(t, SunSpecFloat)
	seedSunSpec(floatChain)
	setHolding(40359, 2)
	setHolding(40366, uint16(0xEC78)) // -5000

	err := ApplyForceCharge(modbus_ip, 0, modbus_port)

	assert.NoError(t, err)
	assert.Equal(t, int16(0), getHolding(40359), "StorCtl_Mod")
	assert.Equal(t, int16(10000), getHolding(40366), "OutWRte")
}

func TestApplyForceChargeNegative(t *testing.T) {
	setup()
	defer teardown()
	useSunSpecType(t, SunSpecFloat)
	seedSunSpec(floatChain)
	before := append([]uint16(nil), mockServer.HoldingRegisters...)

	err := ApplyForceCharge(modbus_ip, -1, modbus_port)

	assert.ErrorContains(t, err, "negative")
	assert.Equal(t, before, mockServer.HoldingRegisters)
}

func TestApplyForceChargeMissingModelWritesNothing(t *testing.T) {
	setup()
	defer teardown()
	useSunSpecType(t, SunSpecFloat)
	seedSunSpec(floatChain[:len(floatChain)-1])
	before := append([]uint16(nil), mockServer.HoldingRegisters...)

	err := ApplyForceCharge(modbus_ip, 50, modbus_port)

	assert.ErrorIs(t, err, ErrStorageModelNotFound)
	assert.Equal(t, before, mockServer.HoldingRegisters)
}

func TestApplyForceChargeUnreachable(t *testing.T) {
	useSunSpecType(t, SunSpecFloat)

	err := ApplyForceCharge(modbus_ip, 50, "6599")

	assert.Error(t, err)
}

func TestApplyDefaultsFloatWritesDiscoveredRegisters(t *testing.T) {
	setup()
	defer teardown()
	useSunSpecType(t, SunSpecFloat)
	seedSunSpec(floatChain)
	setHolding(40359, 2)

	err := ApplyDefaults(modbus_ip, modbus_port)

	assert.NoError(t, err)
	assert.Equal(t, int16(0), getHolding(40359), "StorCtl_Mod")
	assert.Equal(t, int16(10000), getHolding(40366), "OutWRte")
	assert.Equal(t, int16(0), getHolding(OutWRte), "legacy OutWRte address")
}

func TestApplyDefaultsIntSFUsesLegacyAddresses(t *testing.T) {
	setup()
	defer teardown()

	err := ApplyDefaults(modbus_ip, modbus_port)

	assert.NoError(t, err)
	assert.Equal(t, int16(10000), getHolding(OutWRte))
	assert.Equal(t, int16(1), getHolding(ChaGriSet))
}

func TestApplyDefaultsFloatWithoutMarkerWritesNothing(t *testing.T) {
	setup()
	defer teardown()
	useSunSpecType(t, SunSpecFloat)
	before := append([]uint16(nil), mockServer.HoldingRegisters...)

	err := ApplyDefaults(modbus_ip, modbus_port)

	assert.ErrorIs(t, err, ErrSunSpecMarkerNotFound)
	assert.Equal(t, before, mockServer.HoldingRegisters)
}

func TestReadStorageControlMode(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)
	setHolding(40359, 2)
	setHolding(StorCtl_Mod, 0)

	value, err := ReadStorageControlMode(modbus_ip, modbus_port)
	assert.NoError(t, err)
	assert.Equal(t, int16(0), value, "int_sf reads the legacy address")

	useSunSpecType(t, SunSpecFloat)
	value, err = ReadStorageControlMode(modbus_ip, modbus_port)
	assert.NoError(t, err)
	assert.Equal(t, int16(2), value, "float reads the discovered address")
}

func TestReadStorageControlModeErrors(t *testing.T) {
	useSunSpecType(t, SunSpecFloat)

	_, err := ReadStorageControlMode(modbus_ip, "6599")
	assert.Error(t, err, "unreachable inverter")

	setup()
	defer teardown()
	seedSunSpec(floatChain[:len(floatChain)-1])
	_, err = ReadStorageControlMode(modbus_ip, modbus_port)
	assert.ErrorIs(t, err, ErrStorageModelNotFound)
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

	// Read-only: none of the outcomes may write to the inverter.
	before := append([]uint16(nil), mockServer.HoldingRegisters...)
	for _, st := range []SunSpecType{SunSpecIntSF, SunSpecFloat, SunSpecAuto} {
		VerifySunSpecType(st, modbus_ip, modbus_port)
	}
	VerifySunSpecType(SunSpecIntSF, modbus_ip, "6599")
	assert.Equal(t, before, mockServer.HoldingRegisters)
}
