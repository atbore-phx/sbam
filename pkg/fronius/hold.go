package fronius

import u "sbam/src/utils"

// ApplyHold blocks battery discharge while keeping charging from PV allowed:
// StorCtl_Mod = 2 (limit discharge) with OutWRte = 0 %, the other storage
// registers at their defaults. A later ApplyDefaults releases the hold.
//
// With SunSpecIntSF (or the zero value) the legacy int+SF addresses are
// written; otherwise the storage model is located as for ApplyForceCharge.
func ApplyHold(t SunSpecType, modbusIP string, port ...string) error {
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	u.Log.Info("Holding Fronius Storage: battery discharge blocked during the charge window")

	var err error
	if t.orDefault() == SunSpecIntSF {
		regList := copyMap(mdsc)
		regList[StorCtl_Mod] = 2 // Limit Decharging
		regList[OutWRte] = 0     // to 0 %
		err = Connectmodbus(modbusIP, regList, p)
	} else {
		err = connectStorage(t, modbusIP, func(r StorageRegisters) map[uint16]int16 {
			regList := r.defaults()
			regList[r.StorCtlMod] = 2 // Limit Decharging
			regList[r.OutWRte] = 0    // to 0 %
			return regList
		}, p)
	}
	if err != nil {
		u.Log.Errorf("Something goes wrong %s", err)
		return err
	}
	u.Log.Info("Holding Fronius Storage done.")
	return nil
}
