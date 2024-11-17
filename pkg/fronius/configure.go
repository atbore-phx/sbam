package fronius

import (
	"errors"
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

func copyMap(src map[uint16]int16) map[uint16]int16 {
	dst := make(map[uint16]int16)
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func WriteFroniusModbusRegisters(modbusStorageCfg map[uint16]int16) error {

	for r, v := range modbusStorageCfg {
		u.Log.Debugf("Writing register: %d ; value: %v", r, uint16(v))
		err = modbusClient.WriteRegister(r-1, uint16(v))
		handleErrorPanic(err, "Error Writing register "+fmt.Sprintf("%d", r)+", value: "+fmt.Sprintf("%d", v))

	}
	return nil
}

func ReadFroniusModbusRegisters(modbusStorageCfg map[uint16]int16) ([]int16, error) {
	values := []int16{}
	for r, v := range modbusStorageCfg {
		value, err := modbusClient.ReadRegister(r-1, modbus.HOLDING_REGISTER)
		handleErrorPanic(err, "Error Reading register "+fmt.Sprintf("%d", r)+", value: "+fmt.Sprintf("%d", v))
		u.Log.Debugf("Reading register: %d ; value: %v; default:", r, value)

		values = append(values, int16(value))
	}
	return values, nil
}

func ReadFroniusModbusRegister(address uint16) (int16, error) {
	value, err := modbusClient.ReadRegister(address-1, modbus.HOLDING_REGISTER)
	u.Log.Debugf("Reading register: %d ; value: %v", address, value)
	return int16(value), handleError(err, "Something goes wrong reading the register")
}

func Setdefaults(modbus_ip string, port ...string) error {
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	u.Log.Info("Setting Fronius Storage Defaults start...")
	regList := copyMap(mdsc)
	err = Connectmodbus(modbus_ip, regList, p)
	if err != nil {
		u.Log.Errorf("Something goes wrong %s", err)
		return err
	}
	u.Log.Info("Setting Fronius Modbus Defaults done.")
	return nil
}

func ForceCharge(modbus_ip string, power_prc int16, port ...string) error {
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	u.Log.Infof("Setting Fronius Storage Force Charge at %d%%", power_prc)
	if power_prc > 0 {
		regList := copyMap(mdsc)

		regList[StorCtl_Mod] = 2 // Limit Decharging
		regList[OutWRte] = -100 * power_prc

		err = Connectmodbus(modbus_ip, regList, p)
		if err != nil {
			u.Log.Errorf("Something goes wrong %s", err)
			return err
		}

	} else if power_prc == 0 {
		Setdefaults(modbus_ip, p)
	} else {
		err = errors.New("percent of charging is negative")
		u.Log.Errorf("someting goes wrong when force charging, %s", err)
		return err
	}
	u.Log.Info("Setting Fronius Storage Force Charge done.")
	return nil
}

func Connectmodbus(url string, regList map[uint16]int16, port ...string) error {
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	err = OpenModbusClient("tcp", url, p)
	if err != nil {
		u.Log.Errorf("Something goes wrong %s", err)
		return err
	}

	_, err = ReadFroniusModbusRegisters(regList)
	handleErrorPanic(err, "Something goes wrong reading ReadFroniusModbusRegisters")

	err = WriteFroniusModbusRegisters(regList)
	handleErrorPanic(err, "Something goes wrong writing FroniusModbusRegisters")

	ClosemodbusClient()

	return nil
}
