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
		werr := modbusClient.WriteRegister(r-1, uint16(v))
		if werr != nil {
			u.Log.Errorf("Error writing register %d value %d: %v", r, v, werr)
			return fmt.Errorf("write register %d failed: %w", r, werr)
		}
	}
	return nil
}

func ReadFroniusModbusRegisters(modbusStorageCfg map[uint16]int16) ([]int16, error) {
	values := []int16{}
	for r, v := range modbusStorageCfg {
		value, rerr := modbusClient.ReadRegister(r-1, modbus.HOLDING_REGISTER)
		if rerr != nil {
			u.Log.Errorf("Error reading register %d: %v", r, rerr)
			return nil, fmt.Errorf("read register %d failed: %w", r, rerr)
		}
		u.Log.Debugf("Reading register: %d ; default value: %v ; read value: %v", r, v, value)

		values = append(values, int16(value))
	}
	return values, nil
}

func ReadFroniusModbusRegister(address uint16) (int16, error) {
	value, err := modbusClient.ReadRegister(address-1, modbus.HOLDING_REGISTER)
	u.Log.Debugf("Reading register: %d ; value: %v", address, value)
	return int16(value), u.HandleError(err, "Something goes wrong reading the register")
}

// ReadModbusRegister opens a Modbus TCP connection, reads a single holding
// register, closes the connection, and returns the value. It is a standalone
// helper for callers that only need to read one register without writing.
func ReadModbusRegister(modbusIP string, register uint16, port ...string) (int16, error) {
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	modbusErr = OpenModbusClient("tcp", modbusIP, p)
	if modbusErr != nil {
		return 0, modbusErr
	}
	defer func() {
		if cerr := ClosemodbusClient(); cerr != nil {
			u.Log.Warnf("error closing modbus client: %v", cerr)
		}
	}()
	return ReadFroniusModbusRegister(register)
}

func Setdefaults(modbus_ip string, port ...string) error {
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	u.Log.Info("Setting Fronius Storage Defaults start...")
	regList := copyMap(mdsc)
	modbusErr = Connectmodbus(modbus_ip, regList, p)
	if modbusErr != nil {
		u.Log.Errorf("Something goes wrong %s", modbusErr)
		return modbusErr
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

		modbusErr = Connectmodbus(modbus_ip, regList, p)
		if modbusErr != nil {
			u.Log.Errorf("Something goes wrong %s", modbusErr)
			return modbusErr
		}

	} else if power_prc == 0 {
		u.Log.Info("percent of charging is <1%, skipping Force Charge and set defaults.")
		modbusErr = Setdefaults(modbus_ip, p)
		if modbusErr != nil {
			u.Log.Errorln("Error Setting Defaults: %s ", modbusErr)
			return modbusErr
		}
	} else {
		modbusErr = errors.New("percent of charging is negative")
		u.Log.Errorf("someting goes wrong when force charging, %s", modbusErr)
		return modbusErr
	}
	u.Log.Info("Setting Fronius Storage Force Charge done.")
	return nil
}

func Connectmodbus(url string, regList map[uint16]int16, port ...string) error {
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	modbusErr = OpenModbusClient("tcp", url, p)
	if modbusErr != nil {
		u.Log.Errorf("Something goes wrong %s", modbusErr)
		return modbusErr
	}

	// Ensure client is closed when we exit this function
	defer func() {
		if cerr := ClosemodbusClient(); cerr != nil {
			u.Log.Warnf("error closing modbus client: %v", cerr)
		}
	}()

	_, rerr := ReadFroniusModbusRegisters(regList)
	if rerr != nil {
		u.Log.Errorf("Something goes wrong reading ReadFroniusModbusRegisters: %v", rerr)
		return rerr
	}

	werr := WriteFroniusModbusRegisters(regList)
	if werr != nil {
		u.Log.Errorf("Something goes wrong writing FroniusModbusRegisters: %v", werr)
		return werr
	}

	return nil
}
