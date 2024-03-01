package fronius

import (
	"fmt"

	"github.com/simonvetter/modbus"
)

const (
	StorCtl_Mod = 40349
	OutWRte     = 40356
	InWRte      = 40357
	MinRsvPct   = 40351
	ChaGriSet   = 40361
	WChaMax     = 40346
)

// defaults to r/w
var mdsc = map[uint16]int16{
	StorCtl_Mod: 0,     // no limits
	OutWRte:     10000, // 100% w 2 sf
	InWRte:      10000, // 100% w 2 sf
	MinRsvPct:   0,     // 0% w 2 sf
	ChaGriSet:   1,     //  Grid enabled
}

func WriteFroniusModbusRegisters(modbusStorageCfg map[uint16]int16) error {

	for r, v := range modbusStorageCfg {
		err = modbusClient.WriteRegister(r-1, uint16(v))
		if err != nil {
			fmt.Printf("Something goes wrong writing the register: %d, value: %d\n", r, v)
			panic(err)
		}
	}
	return nil
}

func ReadFroniusModbusRegisters(modbusStorageCfg map[uint16]int16) ([]int16, error) {
	values := []int16{}
	for r, v := range modbusStorageCfg {
		value, err := modbusClient.ReadRegister(r-1, modbus.HOLDING_REGISTER)
		fmt.Printf("register: %d ; value: %v\n", r, value)
		if err != nil {
			fmt.Printf("Something goes wrong reading the register: %d, value: %d\n", r, v)
			panic(err)
		}
		values = append(values, int16(value))
	}
	return values, nil
}

func ReadFroniusModbusRegister(address uint16) (int16, error) {
	value, err := modbusClient.ReadRegister(address-1, modbus.HOLDING_REGISTER)
	fmt.Printf("register: %d ; value: %v\n", address, value)
	if err != nil {
		fmt.Printf("Something goes wrong reading the register: %d, value: %d\n", address, value)
		panic(err)
	}
	return int16(value), nil
}

func Setdefaults(modbus_ip string, modbus_port string) error {
	url := "tcp://" + modbus_ip + ":" + modbus_port
	regList := mdsc

	OpenModbusClient(url)

	WriteFroniusModbusRegisters(regList)
	ReadFroniusModbusRegisters(regList)

	ClosemodbusClient()

	return nil
}

func ForceCharge(modbus_ip string, modbus_port string, power_prc int16) error {
	url := "tcp://" + modbus_ip + ":" + modbus_port
	regList := mdsc

	regList[StorCtl_Mod] = 2 // Limit Decharging
	regList[OutWRte] = -100 * power_prc

	OpenModbusClient(url)

	WriteFroniusModbusRegisters(regList)
	ReadFroniusModbusRegisters(regList)

	ClosemodbusClient()

	return nil
}
