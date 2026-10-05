package fronius

import (
	"errors"
	"fmt"

	u "sbam/src/utils"

	"github.com/simonvetter/modbus"
)

// SunSpec map layout. Fronius inverters expose a chain of SunSpec models
// starting with the "SunS" marker at 40001. Each model starts with an ID
// register followed by a length register (number of registers that follow).
// The start address of the storage model (124) depends on the inverter's
// "SunSpec Model Type" setting: with "int + SF" the inverter model (101/102/103)
// has 50 registers, with "float" (111/112/113) it has 60, so every model after
// it, storage included, moves by 10 registers.
const (
	sunspecMarkerAddr     = 40001
	sunspecFirstModelAddr = 40003
	sunspecMarkerHi       = 0x5375 // "Su"
	sunspecMarkerLo       = 0x6e53 // "nS"
	sunspecEndModelID     = 0xFFFF
	sunspecStorageModelID = 124
	sunspecMaxModels      = 64

	// Start of model 124 (its ID register) with "int + SF", the layout the
	// legacy hardcoded addresses were written for.
	legacyStorageModelAddr = 40344
)

// Offsets of the model 124 fields from the model's ID register.
const (
	offWChaMax    = 2
	offStorCtlMod = 5
	offMinRsvPct  = 7
	offOutWRte    = 12
	offInWRte     = 13
	offChaGriSet  = 17
)

// ErrStorageModelNotFound is returned when the inverter exposes a SunSpec
// map without a storage model (124). Nothing is written in that case.
var ErrStorageModelNotFound = errors.New("SunSpec storage model 124 not found")

// StorageRegisters holds the 1-based addresses of the model 124 registers
// sbam reads and writes.
type StorageRegisters struct {
	ModelAddr  uint16
	WChaMax    uint16
	StorCtlMod uint16
	MinRsvPct  uint16
	OutWRte    uint16
	InWRte     uint16
	ChaGriSet  uint16
}

func storageRegistersAt(modelAddr uint16) StorageRegisters {
	return StorageRegisters{
		ModelAddr:  modelAddr,
		WChaMax:    modelAddr + offWChaMax,
		StorCtlMod: modelAddr + offStorCtlMod,
		MinRsvPct:  modelAddr + offMinRsvPct,
		OutWRte:    modelAddr + offOutWRte,
		InWRte:     modelAddr + offInWRte,
		ChaGriSet:  modelAddr + offChaGriSet,
	}
}

// defaults returns the register values that restore normal battery operation.
func (r StorageRegisters) defaults() map[uint16]int16 {
	return map[uint16]int16{
		r.StorCtlMod: 0,     // no limits
		r.OutWRte:    10000, // 100% w 2 sf
		r.InWRte:     10000, // 100% w 2 sf
		r.MinRsvPct:  0,     // 0% w 2 sf
		r.ChaGriSet:  1,     //  Grid enabled
	}
}

func readHolding(address uint16, quantity uint16) ([]uint16, error) {
	return modbusClient.ReadRegisters(address-1, quantity, modbus.HOLDING_REGISTER)
}

// DiscoverStorageRegisters walks the SunSpec model chain on the open Modbus
// connection and returns the addresses of the storage model (124), so sbam
// works with both "int + SF" and "float" SunSpec model types.
//
// When the device does not expose the "SunS" marker, the legacy "int + SF"
// addresses are returned to preserve the previous behaviour. When the marker
// is present but model 124 is not, ErrStorageModelNotFound is returned and
// callers must not write.
func DiscoverStorageRegisters() (StorageRegisters, error) {
	marker, err := readHolding(sunspecMarkerAddr, 2)
	if err != nil {
		return StorageRegisters{}, fmt.Errorf("read SunSpec marker failed: %w", err)
	}
	if marker[0] != sunspecMarkerHi || marker[1] != sunspecMarkerLo {
		u.Log.Warnf("SunSpec marker not found at %d, using legacy int+SF storage addresses (model 124 at %d)", sunspecMarkerAddr, legacyStorageModelAddr)
		return storageRegistersAt(legacyStorageModelAddr), nil
	}

	addr := uint32(sunspecFirstModelAddr)
	for i := 0; i < sunspecMaxModels; i++ {
		if addr+1 > 0xFFFF {
			break
		}
		header, err := readHolding(uint16(addr), 2)
		if err != nil {
			return StorageRegisters{}, fmt.Errorf("read SunSpec model header at %d failed: %w", addr, err)
		}
		id, length := header[0], header[1]
		if id == sunspecEndModelID {
			break
		}
		if id == sunspecStorageModelID {
			u.Log.Debugf("SunSpec storage model 124 at %d", addr)
			return storageRegistersAt(uint16(addr)), nil
		}
		addr += 2 + uint32(length)
	}
	return StorageRegisters{}, ErrStorageModelNotFound
}
