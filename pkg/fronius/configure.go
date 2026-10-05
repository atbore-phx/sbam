package fronius

import (
	"errors"
	"fmt"
	u "sbam/src/utils"

	"github.com/simonvetter/modbus"
)

// Storage register addresses for the "int + SF" SunSpec model type. The
// addresses actually used are discovered at runtime (see
// DiscoverStorageRegisters); these remain for reference and as the fallback
// for devices without a SunSpec marker.
const (
	StorCtl_Mod = legacyStorageModelAddr + offStorCtlMod
	OutWRte     = legacyStorageModelAddr + offOutWRte
	InWRte      = legacyStorageModelAddr + offInWRte
	MinRsvPct   = legacyStorageModelAddr + offMinRsvPct
	ChaGriSet   = legacyStorageModelAddr + offChaGriSet
	WChaMax     = legacyStorageModelAddr + offWChaMax
)

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
	modbusErr = connectStorage(modbus_ip, StorageRegisters.defaults, p)
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
		modbusErr = connectStorage(modbus_ip, func(r StorageRegisters) map[uint16]int16 {
			regList := r.defaults()
			regList[r.StorCtlMod] = 2 // Limit Decharging
			regList[r.OutWRte] = -100 * power_prc
			return regList
		}, p)
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

// ReadStorageControlMode opens a Modbus TCP connection, locates the storage
// model and returns the current StorCtl_Mod value.
func ReadStorageControlMode(modbusIP string, port ...string) (int16, error) {
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
	regs, derr := DiscoverStorageRegisters()
	if derr != nil {
		return 0, derr
	}
	return ReadFroniusModbusRegister(regs.StorCtlMod)
}

// connectStorage opens a Modbus TCP connection, locates the storage model
// and writes the registers built for the discovered addresses. Nothing is
// written when the storage model cannot be located.
func connectStorage(url string, build func(StorageRegisters) map[uint16]int16, port string) error {
	modbusErr = OpenModbusClient("tcp", url, port)
	if modbusErr != nil {
		u.Log.Errorf("Something goes wrong %s", modbusErr)
		return modbusErr
	}

	defer func() {
		if cerr := ClosemodbusClient(); cerr != nil {
			u.Log.Warnf("error closing modbus client: %v", cerr)
		}
	}()

	regs, derr := DiscoverStorageRegisters()
	if derr != nil {
		u.Log.Errorf("Something goes wrong locating the storage model, nothing written: %v", derr)
		return derr
	}
	regList := build(regs)

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
