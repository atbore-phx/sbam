package fronius

import (
	"context"
	"errors"

	u "sbam/src/utils"
)

// ApplyDefaults restores the battery defaults for SunSpec type t. With
// SunSpecIntSF (or the zero value) it calls the legacy Setdefaults unchanged.
func ApplyDefaults(ctx context.Context, t SunSpecType, modbusIP string, port ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.orDefault() == SunSpecIntSF {
		return Setdefaults(modbusIP, port...)
	}
	return setdefaultsSunSpec(ctx, t, modbusIP, port...)
}

// ApplyForceCharge forces a battery charge for SunSpec type t. With
// SunSpecIntSF (or the zero value) it calls the legacy ForceCharge unchanged.
func ApplyForceCharge(ctx context.Context, t SunSpecType, modbusIP string, powerPrc int16, port ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.orDefault() == SunSpecIntSF {
		return ForceCharge(modbusIP, powerPrc, port...)
	}
	return forceChargeSunSpec(ctx, t, modbusIP, powerPrc, port...)
}

// ReadStorageControlMode returns the current StorCtl_Mod value for SunSpec
// type t. With SunSpecIntSF (or the zero value) it reads the legacy address.
func ReadStorageControlMode(ctx context.Context, t SunSpecType, modbusIP string, port ...string) (int16, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if t.orDefault() == SunSpecIntSF {
		return ReadModbusRegister(modbusIP, StorCtl_Mod, port...)
	}
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	if err := openModbusClient(ctx, "tcp", modbusIP, p); err != nil {
		return 0, err
	}
	defer func() {
		if cerr := ClosemodbusClient(); cerr != nil {
			u.Log.Warnf("error closing modbus client: %v", cerr)
		}
	}()
	regs, err := DiscoverStorageRegisters(ctx, t)
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return ReadFroniusModbusRegister(regs.StorCtlMod)
}

func setdefaultsSunSpec(ctx context.Context, t SunSpecType, modbusIP string, port ...string) error {
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	u.Log.Info("Setting Fronius Storage Defaults start...")
	if err := connectStorage(ctx, t, modbusIP, StorageRegisters.defaults, p); err != nil {
		u.Log.Errorf("Something goes wrong %s", err)
		return err
	}
	u.Log.Info("Setting Fronius Modbus Defaults done.")
	return nil
}

func forceChargeSunSpec(ctx context.Context, t SunSpecType, modbusIP string, powerPrc int16, port ...string) error {
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	u.Log.Infof("Setting Fronius Storage Force Charge at %d%%", powerPrc)
	if powerPrc > 100 {
		// OutWRte is a percentage of WChaMax; above 100 % the value would be
		// out of range (and overflow int16 above 327 %).
		u.Log.Warnf("percent of charging %d%% is above 100%%, capping at 100%%", powerPrc)
		powerPrc = 100
	}
	switch {
	case powerPrc > 0:
		err := connectStorage(ctx, t, modbusIP, func(r StorageRegisters) map[uint16]int16 {
			regList := r.defaults()
			regList[r.StorCtlMod] = 2 // Limit Decharging
			regList[r.OutWRte] = -100 * powerPrc
			return regList
		}, p)
		if err != nil {
			u.Log.Errorf("Something goes wrong %s", err)
			return err
		}
	case powerPrc == 0:
		u.Log.Info("percent of charging is <1%, skipping Force Charge and set defaults.")
		if err := setdefaultsSunSpec(ctx, t, modbusIP, p); err != nil {
			u.Log.Errorf("Error Setting Defaults: %s", err)
			return err
		}
	default:
		err := errors.New("percent of charging is negative")
		u.Log.Errorf("someting goes wrong when force charging, %s", err)
		return err
	}
	u.Log.Info("Setting Fronius Storage Force Charge done.")
	return nil
}

// connectStorage opens a Modbus TCP connection, locates the storage model
// and writes the registers built for the discovered addresses. Nothing is
// written when the storage model cannot be located or validated, or when ctx
// is done before the write starts. A write that has started is not interrupted,
// because a partly applied register set is worse than a late one; each register
// is bounded by the client timeout.
func connectStorage(ctx context.Context, t SunSpecType, url string, build func(StorageRegisters) map[uint16]int16, port string) error {
	if err := openModbusClient(ctx, "tcp", url, port); err != nil {
		return err
	}
	defer func() {
		if cerr := ClosemodbusClient(); cerr != nil {
			u.Log.Warnf("error closing modbus client: %v", cerr)
		}
	}()

	regs, err := DiscoverStorageRegisters(ctx, t)
	if err != nil {
		u.Log.Errorf("Something goes wrong locating the storage model, nothing written: %v", err)
		return err
	}
	regList := build(regs)

	if _, err := ReadFroniusModbusRegisters(regList); err != nil {
		u.Log.Errorf("Something goes wrong reading ReadFroniusModbusRegisters: %v", err)
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := WriteFroniusModbusRegisters(regList); err != nil {
		u.Log.Errorf("Something goes wrong writing FroniusModbusRegisters: %v", err)
		return err
	}
	return nil
}
