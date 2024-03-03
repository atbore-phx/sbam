package fronius

import (
	"fmt"

	u "sbam/src/utils"

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
		u.Log.Infof("Writing register: %d ; value: %v", r, uint16(v))
		err = modbusClient.WriteRegister(r-1, uint16(v))
		if err != nil {
			u.Log.Errorf("Something goes wrong writing the register: %d, value: %d", r, v)
			panic(err)
		}
	}
	return nil
}

func ReadFroniusModbusRegisters(modbusStorageCfg map[uint16]int16) ([]int16, error) {
	values := []int16{}
	for r, v := range modbusStorageCfg {
		value, err := modbusClient.ReadRegister(r-1, modbus.HOLDING_REGISTER)
		u.Log.Infof("Reading register: %d ; value: %v", r, value)
		if err != nil {
			u.Log.Errorf("Something goes wrong reading the register: %d, value: %d", r, v)
			panic(err)
		}
		values = append(values, int16(value))
	}
	return values, nil
}

func ReadFroniusModbusRegister(address uint16) (int16, error) {
	value, err := modbusClient.ReadRegister(address-1, modbus.HOLDING_REGISTER)
	u.Log.Infof("Reading register: %d ; value: %v", address, value)
	if err != nil {
		u.Log.Errorf("Something goes wrong reading the register: %d, value: %d", address, value)
		panic(err)
	}
	return int16(value), nil
}

func Setdefaults(modbus_ip string) error {
	u.Log.Info("Setting Fronius Storage Defaults start...")
	regList := mdsc
	OpenModbusClient(modbus_ip)

	ReadFroniusModbusRegisters(regList)
	WriteFroniusModbusRegisters(regList)

	ClosemodbusClient()
	u.Log.Info("Setting Fronius Modbus Defaults done.")
	return nil
}

func ForceCharge(modbus_ip string, power_prc int16) error {
	u.Log.Infof("Setting Fronius Storage Force Charge at %d%%", power_prc)
	if power_prc > 0 {
		regList := mdsc

		regList[StorCtl_Mod] = 2 // Limit Decharging
		regList[OutWRte] = -100 * power_prc

		OpenModbusClient(modbus_ip)

		ReadFroniusModbusRegisters(regList)
		WriteFroniusModbusRegisters(regList)

		ClosemodbusClient()
	} else if power_prc == 0 {
		Setdefaults(modbus_ip)
	} else {
		panic(fmt.Errorf("someting goes wrong when force charging, percent of charging is negative: %d", power_prc))
	}
	u.Log.Info("Setting Fronius Storage Force Charge done.")
	return nil
}
