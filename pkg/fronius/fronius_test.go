package fronius

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tbrandon/mbserver"
)

var mockServer *mbserver.Server
var err error
var modbus_ip = "127.0.0.1"
var modbus_port = "6502"

func setup() {

	mockServer = mbserver.NewServer()
	err = mockServer.ListenTCP(modbus_ip + ":" + modbus_port)
	if err != nil {
		panic(err)
	}

}

func teardown() {
	mockServer.Close()
}

func TestModbusConfigError(t *testing.T) {

	err = OpenModbusClient("dummy://invalid://", modbus_ip, modbus_port)

	assert.Error(t, err)
}

func TestWriteFroniusModbusRegisters(t *testing.T) {
	modbusStorageCfg := map[uint16]int16{
		40349: 2,
		40350: 4000,
	}

	setup()
	OpenModbusClient("tcp", modbus_ip, modbus_port)
	err := WriteFroniusModbusRegisters(modbusStorageCfg)
	ClosemodbusClient()
	teardown()

	assert.NoError(t, err)
}

func TestReadFroniusModbusRegisters(t *testing.T) {
	modbusStorageCfg := map[uint16]int16{
		40349: 1,
		40350: 2000,
	}

	setup()
	OpenModbusClient("tcp", modbus_ip, modbus_port)
	values, err := ReadFroniusModbusRegisters(modbusStorageCfg)
	ClosemodbusClient()
	teardown()

	assert.NoError(t, err)
	assert.NotNil(t, values)
}

func TestReadFroniusModbusRegister(t *testing.T) {
	address := uint16(40349)

	setup()
	OpenModbusClient("tcp", modbus_ip, modbus_port)
	value, err := ReadFroniusModbusRegister(address)
	ClosemodbusClient()
	teardown()

	assert.NoError(t, err)
	assert.NotNil(t, value)
}

func TestSetdefaults(t *testing.T) {
	setup()
	err := Setdefaults(modbus_ip, modbus_port)
	teardown()

	assert.NoError(t, err)
}

func TestSetdefaultsError(t *testing.T) {
	err := Setdefaults(modbus_ip, modbus_port)
	assert.Error(t, err)
}

func TestForceCharge(t *testing.T) {

	test_power_prc := []int16{
		50,
		0,
	}

	for _, power_prc := range test_power_prc {
		setup()
		err := ForceCharge(modbus_ip, power_prc, modbus_port)
		teardown()
		assert.NoError(t, err)
	}
}

func TestForceChargeError(t *testing.T) {

	test_power_prc := []int16{
		50,
	}

	for _, power_prc := range test_power_prc {
		err := ForceCharge(modbus_ip, power_prc, modbus_port)
		assert.Error(t, err)
	}
}

func TestForceCharge2(t *testing.T) {

	test_power_prc := []int16{
		-50,
	}

	for _, power_prc := range test_power_prc {
		setup()
		err := ForceCharge(modbus_ip, power_prc, modbus_port)
		teardown()
		assert.Error(t, err)
	}
}

func TestHandler(t *testing.T) {
	assert := assert.New(t)
	fronius := New()

	pwForecast := 1000.0
	pwBatt2charge := 1000.0
	pwBattMax := 10000.0
	pwConsumption := 9000.0
	maxCharge := 3500.0
	pw_batt_reserve := 0.0
	startHr := "09:00"
	endHr := "17:00"

	setup()
	_, _, _, _, err := fronius.Handler(pwForecast, pwBatt2charge, pwBattMax, pwConsumption, maxCharge, pw_batt_reserve, startHr, endHr, modbus_ip, true, 0, 0, true, modbus_port)
	teardown()

	assert.NoError(err, "Handler returned an error")
}

func TestHandlerError(t *testing.T) {
	assert := assert.New(t)
	fronius := New()

	pwForecast := 1000.0
	pwBatt2charge := 1000.0
	pwBattMax := 10000.0
	pwConsumption := 9000.0
	maxCharge := 3500.0
	pw_batt_reserve := 0.0
	startHr := "09:00"
	endHr := "17:00"

	_, _, _, _, err := fronius.Handler(pwForecast, pwBatt2charge, pwBattMax, pwConsumption, maxCharge, pw_batt_reserve, startHr, endHr, modbus_ip, true, 0, 0, true, modbus_port)

	assert.Error(err, "Handler returned an error")
}

func TestOpenCloseModbusClient(t *testing.T) {
	assert := assert.New(t)
	setup()
	err = OpenModbusClient("tcp", modbus_ip, modbus_port)
	err = ClosemodbusClient()
	teardown()
	assert.NoError(err, "OpenModbusClient returned an error")

}

func TestOpenClientError(t *testing.T) {
	assert := assert.New(t)
	setup()
	err = OpenModbusClient("tcp", "123", modbus_port)
	teardown()
	assert.Error(err, "OpenModbusClient returned an error")

}

func TestSetChargePower(t *testing.T) {
	assert := assert.New(t)

	result := SetChargePower(100.0, 50.0, 50.0)
	assert.Equal(int16(50), result, "SetChargePower returned wrong value")

	result = SetChargePower(100.0, 80.0, 50.0)
	assert.Equal(int16(50), result, "SetChargePower returned wrong value")

}

func TestBatteryChargeMode1(t *testing.T) {
	assert := assert.New(t)
	setup()
	result, _, _, _, err := SetFroniusChargeBatteryMode(1000, 0, 11000, 9000, 3500, 0, "00:00", "05:00", modbus_ip, true, 0, 0, true, modbus_port)
	assert.Equal(int16(0), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeMode2(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, _, _, _, err := SetFroniusChargeBatteryMode(1000, 11000, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, modbus_port)
	assert.Equal(int16(31), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeMode3(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, _, _, _, err := SetFroniusChargeBatteryMode(10000, 5000, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, modbus_port)
	assert.Equal(int16(0), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeMode4(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, _, _, _, err := SetFroniusChargeBatteryMode(10000, 0, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, modbus_port)
	assert.Equal(int16(0), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeMode5(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, _, _, _, err := SetFroniusChargeBatteryMode(1000, 11000, 11000, 9000, 3500, 2500, "00:00", "23:59", modbus_ip, true, 0, 0, true, modbus_port)
	assert.Equal(int16(31), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeMode6(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, _, _, _, err := SetFroniusChargeBatteryMode(8000, 2000, 11000, 8000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, modbus_port)
	assert.Equal(int16(0), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeMode7(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, _, _, _, err := SetFroniusChargeBatteryMode(10000, 7000, 11000, 0, 3500, 5000, "00:00", "23:59", modbus_ip, true, 0, 0, true, modbus_port)
	assert.Equal(int16(9), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeMode8(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, _, _, _, err := SetFroniusChargeBatteryMode(5000, 7000, 11000, 10000, 3500, 3000, "00:00", "23:59", modbus_ip, true, 0, 0, true, modbus_port)
	assert.Equal(int16(9), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeError(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, _, _, _, err := SetFroniusChargeBatteryMode(1000, 11000, -11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, modbus_port)
	assert.Equal(int16(-272), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.Error(err)

	teardown()
}

func TestBatteryChargeModeClassifierErrorSkipsDefaultsWhenNotCharging(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, _, _, _, err := SetFroniusChargeBatteryMode(
		100,
		2000,
		5000,
		5000,
		3500,
		1000,
		"00:00",
		"23:59",
		modbus_ip,
		false,
		100,
		0,
		false,
		modbus_port,
	)

	assert.Equal(int16(0), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	// Classifier returns idle (forecast disabled); ForceCharge(0) writes defaults.
	err = OpenModbusClient("tcp", modbus_ip, modbus_port)
	assert.NoError(err, "OpenModbusClient returned an error")
	outWRte, readErr := ReadFroniusModbusRegister(OutWRte)
	assert.NoError(readErr, "ReadFroniusModbusRegister returned an error")
	assert.Equal(int16(10000), outWRte, "OutWRte should be 10000 (defaults were restored)")
	err = ClosemodbusClient()
	assert.NoError(err, "ClosemodbusClient returned an error")

	teardown()
}

func TestBatteryChargeModeClassifierErrorResetsDefaultsWhenCharging(t *testing.T) {
	assert := assert.New(t)
	setup()

	// Pre-set StorCtl_Mod to 2 (inverter is force-charging).
	OpenModbusClient("tcp", modbus_ip, modbus_port)
	err = WriteFroniusModbusRegisters(map[uint16]int16{
		StorCtl_Mod: 2,
	})
	ClosemodbusClient()
	assert.NoError(err)

	result, _, _, _, err := SetFroniusChargeBatteryMode(
		100,
		2000,
		5000,
		5000,
		3500,
		1000,
		"00:00",
		"23:59",
		modbus_ip,
		false,
		100,
		0,
		false,
		modbus_port,
	)

	assert.Equal(int16(0), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	// Defaults should have been written; verify OutWRte is now 10000.
	err = OpenModbusClient("tcp", modbus_ip, modbus_port)
	assert.NoError(err, "OpenModbusClient returned an error")
	outWRte, readErr := ReadFroniusModbusRegister(OutWRte)
	assert.NoError(readErr, "ReadFroniusModbusRegister returned an error")
	assert.Equal(int16(10000), outWRte, "OutWRte should be 10000 (defaults were written)")
	err = ClosemodbusClient()
	assert.NoError(err, "ClosemodbusClient returned an error")

	teardown()
}

func TestBatteryChargeModeClassifierErrorResetFails(t *testing.T) {
	assert := assert.New(t)

	result, _, _, _, err := SetFroniusChargeBatteryMode(
		100,
		2000,
		5000,
		5000,
		3500,
		1000,
		"00:00",
		"23:59",
		modbus_ip,
		false,
		100,
		0,
		false,
		modbus_port,
	)

	assert.Equal(int16(0), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.Error(err)
}

func TestReadModbusRegister(t *testing.T) {
	assert := assert.New(t)
	setup()
	defer teardown()

	value, err := ReadModbusRegister(modbus_ip, StorCtl_Mod, modbus_port)
	// Reads from a mock server; call should succeed.
	assert.NoError(err)
	// value may be a default (0) from the holding register — fine.
	_ = value
}

func TestReadModbusRegisterConnectionError(t *testing.T) {
	_, err := ReadModbusRegister("255.255.255.255", OutWRte, "6502")
	assert.Error(t, err)
}

func TestWriteFroniusModbusRegistersError(t *testing.T) {
	assert := assert.New(t)
	setup()
	OpenModbusClient("tcp", modbus_ip, modbus_port)
	ClosemodbusClient()
	teardown()

	// Client is closed and server is down — write should fail.
	err := WriteFroniusModbusRegisters(map[uint16]int16{40349: 2})
	assert.Error(err)
}

func TestReadFroniusModbusRegistersError(t *testing.T) {
	assert := assert.New(t)
	setup()
	OpenModbusClient("tcp", modbus_ip, modbus_port)
	ClosemodbusClient()
	teardown()

	// Client is closed and server is down — read should fail.
	_, err := ReadFroniusModbusRegisters(map[uint16]int16{40349: 1})
	assert.Error(err)
}

func TestSetFroniusChargeBatteryModeCustomPort(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, _, _, _, err := SetFroniusChargeBatteryMode(
		1000,
		11000,
		11000,
		9000,
		3500,
		0,
		"00:00",
		"23:59",
		modbus_ip,
		true,
		0,
		0,
		true,
		modbus_port,
	)
	teardown()

	assert.Equal(int16(31), result)
	assert.NoError(err)
}

func TestSetFroniusChargeBatteryModeForceChargeError(t *testing.T) {
	assert := assert.New(t)

	// Use a bogus IP so Modbus connect fails during ForceCharge.
	result, _, _, _, err := SetFroniusChargeBatteryMode(
		1000,
		1000,
		10000,
		9000,
		3500,
		0,
		"00:00",
		"23:59",
		"192.0.2.1",
		true,
		0,
		0,
		true,
		"6502",
	)

	// Should get an error from ForceCharge failing to connect.
	assert.Error(err)
	_ = result
}
