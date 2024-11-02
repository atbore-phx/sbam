package fronius_test

import (
	"sbam/pkg/fronius"
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

	err = fronius.OpenModbusClient("dummy://invalid://", modbus_ip, modbus_port)

	assert.Error(t, err)
}

func TestWriteFroniusModbusRegisters(t *testing.T) {
	modbusStorageCfg := map[uint16]int16{
		40349: 2,
		40350: 4000,
	}

	setup()
	fronius.OpenModbusClient("tcp", modbus_ip, modbus_port)
	err := fronius.WriteFroniusModbusRegisters(modbusStorageCfg)
	fronius.ClosemodbusClient()
	teardown()

	assert.NoError(t, err)
}

func TestReadFroniusModbusRegisters(t *testing.T) {
	modbusStorageCfg := map[uint16]int16{
		40349: 1,
		40350: 2000,
	}

	setup()
	fronius.OpenModbusClient("tcp", modbus_ip, modbus_port)
	values, err := fronius.ReadFroniusModbusRegisters(modbusStorageCfg)
	fronius.ClosemodbusClient()
	teardown()

	assert.NoError(t, err)
	assert.NotNil(t, values)
}

func TestReadFroniusModbusRegister(t *testing.T) {
	address := uint16(40349)

	setup()
	fronius.OpenModbusClient("tcp", modbus_ip, modbus_port)
	value, err := fronius.ReadFroniusModbusRegister(address)
	fronius.ClosemodbusClient()
	teardown()

	assert.NoError(t, err)
	assert.NotNil(t, value)
}

func TestSetdefaults(t *testing.T) {
	setup()
	err := fronius.Setdefaults(modbus_ip, modbus_port)
	teardown()

	assert.NoError(t, err)
}

func TestSetdefaultsError(t *testing.T) {
	err := fronius.Setdefaults(modbus_ip, modbus_port)
	assert.Error(t, err)
}

func TestForceCharge(t *testing.T) {

	test_power_prc := []int16{
		50,
		0,
	}

	for _, power_prc := range test_power_prc {
		setup()
		err := fronius.ForceCharge(modbus_ip, power_prc, modbus_port)
		teardown()
		assert.NoError(t, err)
	}
}

func TestForceChargeError(t *testing.T) {

	test_power_prc := []int16{
		50,
	}

	for _, power_prc := range test_power_prc {
		err := fronius.ForceCharge(modbus_ip, power_prc, modbus_port)
		assert.Error(t, err)
	}
}

func TestForceCharge2(t *testing.T) {

	test_power_prc := []int16{
		-50,
	}

	for _, power_prc := range test_power_prc {
		setup()
		err := fronius.ForceCharge(modbus_ip, power_prc, modbus_port)
		teardown()
		assert.Error(t, err)
	}
}

func TestHandler(t *testing.T) {
	assert := assert.New(t)
	fronius := fronius.New()

	pwForecast := 1000.0
	pwBatt2charge := 1000.0
	pwBattMax := 10000.0
	pwConsumption := 9000.0
	maxCharge := 3500.0
	pw_batt_reserve := 0.0
	startHr := "09:00"
	endHr := "17:00"

	setup()
	_, err := fronius.Handler(pwForecast, pwBatt2charge, pwBattMax, pwConsumption, maxCharge, pw_batt_reserve, startHr, endHr, modbus_ip, modbus_port)
	teardown()

	assert.NoError(err, "Handler returned an error")
}

func TestHandlerError(t *testing.T) {
	assert := assert.New(t)
	fronius := fronius.New()

	pwForecast := 1000.0
	pwBatt2charge := 1000.0
	pwBattMax := 10000.0
	pwConsumption := 9000.0
	maxCharge := 3500.0
	pw_batt_reserve := 0.0
	startHr := "09:00"
	endHr := "17:00"

	_, err := fronius.Handler(pwForecast, pwBatt2charge, pwBattMax, pwConsumption, maxCharge, pw_batt_reserve, startHr, endHr, modbus_ip, modbus_port)

	assert.Error(err, "Handler returned an error")
}

func TestOpenCloseModbusClient(t *testing.T) {
	assert := assert.New(t)
	setup()
	err = fronius.OpenModbusClient("tcp", modbus_ip, modbus_port)
	err = fronius.ClosemodbusClient()
	teardown()
	assert.NoError(err, "OpenModbusClient returned an error")

}

func TestOpenClientError(t *testing.T) {
	assert := assert.New(t)
	setup()
	err = fronius.OpenModbusClient("tcp", "123", modbus_port)
	teardown()
	assert.Error(err, "OpenModbusClient returned an error")

}

func TestSetChargePower(t *testing.T) {
	assert := assert.New(t)

	result := fronius.SetChargePower(100.0, 50.0, 50.0)
	assert.Equal(int16(50), result, "SetChargePower returned wrong value")

	result = fronius.SetChargePower(100.0, 80.0, 50.0)
	assert.Equal(int16(50), result, "SetChargePower returned wrong value")

}

func TestBatteryChargeMode1(t *testing.T) {
	assert := assert.New(t)
	setup()
	result, err := fronius.SetFroniusChargeBatteryMode(1000, 0, 11000, 9000, 3500, 0, "00:00", "05:00", modbus_ip, modbus_port)
	assert.Equal(int16(0), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeMode2(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, err := fronius.SetFroniusChargeBatteryMode(1000, 11000, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, modbus_port)
	assert.Equal(int16(31), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeMode3(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, err := fronius.SetFroniusChargeBatteryMode(10000, 5000, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, modbus_port)
	assert.Equal(int16(0), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeMode4(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, err := fronius.SetFroniusChargeBatteryMode(10000, 0, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, modbus_port)
	assert.Equal(int16(0), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeMode5(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, err := fronius.SetFroniusChargeBatteryMode(1000, 11000, 11000, 9000, 3500, 2500, "00:00", "23:59", modbus_ip, modbus_port)
	assert.Equal(int16(31), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeMode6(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, err := fronius.SetFroniusChargeBatteryMode(8000, 2000, 11000, 8000, 3500, 0, "00:00", "23:59", modbus_ip, modbus_port)
	assert.Equal(int16(0), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeMode7(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, err := fronius.SetFroniusChargeBatteryMode(10000, 7000, 11000, 0, 3500, 5000, "00:00", "23:59", modbus_ip, modbus_port)
	assert.Equal(int16(9), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeMode8(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, err := fronius.SetFroniusChargeBatteryMode(5000, 7000, 11000, 10000, 3500, 3000, "00:00", "23:59", modbus_ip, modbus_port)
	assert.Equal(int16(9), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.NoError(err)

	teardown()
}

func TestBatteryChargeError(t *testing.T) {
	assert := assert.New(t)
	setup()

	result, err := fronius.SetFroniusChargeBatteryMode(1000, 11000, -11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, modbus_port)
	assert.Equal(int16(-72), result, "SetFroniusChargeBatteryMode returned wrong value")
	assert.Error(err)

	teardown()
}
