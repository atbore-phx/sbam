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

func TestWriteFroniusModbusRegisters(t *testing.T) {
	modbusStorageCfg := map[uint16]int16{
		40349: 2,
		40350: 4000,
	}

	setup()
	fronius.OpenModbusClient(modbus_ip, modbus_port)
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
	fronius.OpenModbusClient(modbus_ip, modbus_port)
	values, err := fronius.ReadFroniusModbusRegisters(modbusStorageCfg)
	fronius.ClosemodbusClient()
	teardown()

	assert.NoError(t, err)
	assert.NotNil(t, values)
}

func TestReadFroniusModbusRegister(t *testing.T) {
	address := uint16(40349)

	setup()
	fronius.OpenModbusClient(modbus_ip, modbus_port)
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

func TestForceCharge(t *testing.T) {
	power_prc := int16(50)

	setup()
	err := fronius.ForceCharge(modbus_ip, power_prc, modbus_port)
	teardown()

	assert.NoError(t, err)
}

func TestHandler(t *testing.T) {
	assert := assert.New(t)
	fronius := fronius.New()

	pwForecast := 1.0
	pwBatt2charge := 1.0
	pwConsumption := 1.0
	maxCharge := 5
	startHr := "09:00"
	endHr := "17:00"

	setup()
	_, err := fronius.Handler(pwForecast, pwBatt2charge, pwConsumption, maxCharge, startHr, endHr, modbus_ip, modbus_port)
	teardown()

	assert.NoError(err, "Handler returned an error")
}

func TestOpenCloseModbusClient(t *testing.T) {
	assert := assert.New(t)
	setup()
	err = fronius.OpenModbusClient(modbus_ip, modbus_port)
	err = fronius.ClosemodbusClient()
	teardown()
	assert.NoError(err, "OpenModbusClient returned an error")

}
