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

func discover(t *testing.T) (StorageRegisters, error) {
	t.Helper()
	assert.NoError(t, OpenModbusClient("tcp", modbus_ip, modbus_port))
	defer ClosemodbusClient()
	return DiscoverStorageRegisters()
}

func TestDiscoverStorageRegistersIntSF(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(intSFChain)

	regs, err := discover(t)

	assert.NoError(t, err)
	assert.Equal(t, uint16(40344), regs.ModelAddr)
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

	regs, err := discover(t)

	assert.NoError(t, err)
	assert.Equal(t, uint16(40354), regs.ModelAddr)
	assert.Equal(t, uint16(40359), regs.StorCtlMod)
	assert.Equal(t, uint16(40361), regs.MinRsvPct)
	assert.Equal(t, uint16(40366), regs.OutWRte)
	assert.Equal(t, uint16(40367), regs.InWRte)
	assert.Equal(t, uint16(40371), regs.ChaGriSet)
}

func TestDiscoverStorageRegistersWithoutMarkerUsesLegacy(t *testing.T) {
	setup()
	defer teardown()

	regs, err := discover(t)

	assert.NoError(t, err)
	assert.Equal(t, uint16(legacyStorageModelAddr), regs.ModelAddr)
}

func TestDiscoverStorageRegistersMissingModel(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain[:len(floatChain)-1])

	_, err := discover(t)

	assert.ErrorIs(t, err, ErrStorageModelNotFound)
}

func TestForceChargeFloatWritesDiscoveredRegisters(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)

	err := ForceCharge(modbus_ip, 50, modbus_port)

	assert.NoError(t, err)
	assert.Equal(t, int16(2), getHolding(40359), "StorCtl_Mod")
	assert.Equal(t, int16(-5000), getHolding(40366), "OutWRte")
	assert.Equal(t, int16(10000), getHolding(40367), "InWRte")
	assert.Equal(t, int16(0), getHolding(40361), "MinRsvPct")
	assert.Equal(t, int16(1), getHolding(40371), "ChaGriSet")
	// The int+SF addresses fall inside the MPPT model in float mode and
	// must not be touched.
	for _, addr := range []uint16{40349, 40351, 40356, 40357} {
		assert.Equal(t, int16(0), getHolding(addr), "legacy address %d", addr)
	}
}

func TestSetdefaultsFloatWritesDiscoveredRegisters(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)
	setHolding(40359, 2)

	err := Setdefaults(modbus_ip, modbus_port)

	assert.NoError(t, err)
	assert.Equal(t, int16(0), getHolding(40359), "StorCtl_Mod")
	assert.Equal(t, int16(10000), getHolding(40366), "OutWRte")
	assert.Equal(t, int16(0), getHolding(40356), "legacy OutWRte address")
}

func TestForceChargeMissingModelWritesNothing(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain[:len(floatChain)-1])
	before := append([]uint16(nil), mockServer.HoldingRegisters...)

	err := ForceCharge(modbus_ip, 50, modbus_port)

	assert.ErrorIs(t, err, ErrStorageModelNotFound)
	assert.Equal(t, before, mockServer.HoldingRegisters)
}

func TestReadStorageControlModeFloat(t *testing.T) {
	setup()
	defer teardown()
	seedSunSpec(floatChain)
	setHolding(40359, 2)

	value, err := ReadStorageControlMode(modbus_ip, modbus_port)

	assert.NoError(t, err)
	assert.Equal(t, int16(2), value)
}
