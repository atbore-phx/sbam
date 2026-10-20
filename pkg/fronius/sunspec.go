package fronius

import (
	"errors"
	"fmt"
	"strings"

	u "sbam/src/utils"

	"github.com/simonvetter/modbus"
)

// SunSpecType selects how sbam locates the Fronius storage registers. It
// mirrors the inverter's Modbus "SunSpec Model Type" setting.
type SunSpecType string

const (
	// SunSpecIntSF uses the legacy hardcoded "int + SF" addresses (default).
	SunSpecIntSF SunSpecType = "int_sf"
	// SunSpecFloat locates the storage model by walking the SunSpec model
	// chain. The "SunS" marker is required.
	SunSpecFloat SunSpecType = "float"
	// SunSpecAuto walks the SunSpec model chain like SunSpecFloat, so it works
	// with either inverter setting, and falls back to the legacy addresses when
	// the device has no "SunS" marker.
	SunSpecAuto SunSpecType = "auto"
)

// ParseSunSpecType validates a configured sunspec_type value. An empty value
// selects the default, SunSpecIntSF.
func ParseSunSpecType(value string) (SunSpecType, error) {
	switch t := SunSpecType(strings.ToLower(strings.TrimSpace(value))); t {
	case "":
		return SunSpecIntSF, nil
	case SunSpecIntSF, SunSpecFloat, SunSpecAuto:
		return t, nil
	default:
		return "", fmt.Errorf("invalid sunspec_type %q: must be one of int_sf, float, auto", value)
	}
}

// orDefault returns SunSpecIntSF for the zero value, so an unset type keeps
// the legacy behaviour.
func (t SunSpecType) orDefault() SunSpecType {
	if t == "" {
		return SunSpecIntSF
	}
	return t
}

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
	offStorCtlMod   = 5
	offMinRsvPct    = 7
	offOutWRte      = 12
	offInWRte       = 13
	offChaGriSet    = 17
	offInOutWRteSF  = 25
	storageModelLen = 24 // registers after the ID and length registers

	// sbam writes OutWRte and InWRte as percentages with two decimals
	// (10000 = 100 %), which assumes InOutWRte_SF = -2. MinRsvPct is only
	// ever written as 0, which does not depend on its scale factor.
	expectedInOutWRteSF = -2
)

var (
	// ErrStorageModelNotFound is returned when the inverter exposes a SunSpec
	// map without a storage model (124). Nothing is written in that case.
	ErrStorageModelNotFound = errors.New("SunSpec storage model 124 not found")
	// ErrSunSpecMarkerNotFound is returned when the "SunS" marker is missing
	// and the configured type requires it.
	ErrSunSpecMarkerNotFound = errors.New("SunSpec marker \"SunS\" not found")
	// ErrInverterModelNotFound is returned when no SunSpec inverter model
	// (101-103 or 111-113) is found while detecting the model type.
	ErrInverterModelNotFound = errors.New("SunSpec inverter model not found")
	// ErrUnexpectedStorageModel is returned when model 124 is shorter than
	// the SunSpec definition or its InOutWRte_SF is not the scale factor sbam
	// writes for. Nothing is written in that case.
	ErrUnexpectedStorageModel = errors.New("unexpected SunSpec storage model 124 layout")
)

// StorageRegisters holds the 1-based addresses of the model 124 registers
// sbam reads and writes.
type StorageRegisters struct {
	StorCtlMod uint16
	MinRsvPct  uint16
	OutWRte    uint16
	InWRte     uint16
	ChaGriSet  uint16
}

func storageRegistersAt(modelAddr uint16) StorageRegisters {
	return StorageRegisters{
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

// hasSunSpecMarker reports whether the "SunS" marker is present at 40001.
func hasSunSpecMarker() (bool, error) {
	marker, err := readHolding(sunspecMarkerAddr, 2)
	if err != nil {
		return false, fmt.Errorf("read SunSpec marker failed: %w", err)
	}
	return marker[0] == sunspecMarkerHi && marker[1] == sunspecMarkerLo, nil
}

// findModel walks the SunSpec model chain and returns the ID, start address
// and length of the first model accepted by match. found is false when the
// end of the chain is reached without a match.
func findModel(match func(id uint16) bool) (id uint16, addr uint16, length uint16, found bool, err error) {
	a := uint32(sunspecFirstModelAddr)
	for i := 0; i < sunspecMaxModels && a+1 <= 0xFFFF; i++ {
		header, rerr := readHolding(uint16(a), 2)
		if rerr != nil {
			return 0, 0, 0, false, fmt.Errorf("read SunSpec model header at %d failed: %w", a, rerr)
		}
		if header[0] == sunspecEndModelID {
			break
		}
		if match(header[0]) {
			return header[0], uint16(a), header[1], true, nil
		}
		a += 2 + uint32(header[1])
	}
	return 0, 0, 0, false, nil
}

// DiscoverStorageRegisters walks the SunSpec model chain on the open Modbus
// connection and returns the addresses of the storage model (124).
//
// When the device does not expose the "SunS" marker, SunSpecAuto returns the
// legacy "int + SF" addresses and any other type returns
// ErrSunSpecMarkerNotFound. When the marker is present but model 124 is not,
// ErrStorageModelNotFound is returned. Callers must not write on error.
func DiscoverStorageRegisters(t SunSpecType) (StorageRegisters, error) {
	ok, err := hasSunSpecMarker()
	if err != nil {
		return StorageRegisters{}, err
	}
	if !ok {
		if t == SunSpecAuto {
			u.Log.Warnf("SunSpec marker not found at %d, using legacy int+SF storage addresses (model 124 at %d)", sunspecMarkerAddr, legacyStorageModelAddr)
			return storageRegistersAt(legacyStorageModelAddr), nil
		}
		return StorageRegisters{}, ErrSunSpecMarkerNotFound
	}

	_, addr, length, found, err := findModel(func(id uint16) bool { return id == sunspecStorageModelID })
	if err != nil {
		return StorageRegisters{}, err
	}
	if !found {
		return StorageRegisters{}, ErrStorageModelNotFound
	}
	if err := checkStorageModel(addr, length); err != nil {
		return StorageRegisters{}, err
	}
	u.Log.Debugf("SunSpec storage model 124 at %d", addr)
	return storageRegistersAt(addr), nil
}

// checkStorageModel verifies that model 124 at addr has the full SunSpec
// length and the InOutWRte_SF that sbam's OutWRte/InWRte values assume.
func checkStorageModel(addr, length uint16) error {
	if length < storageModelLen {
		return fmt.Errorf("%w: model 124 at %d has length %d, want %d", ErrUnexpectedStorageModel, addr, length, storageModelLen)
	}
	v, err := readHolding(addr+offInOutWRteSF, 1)
	if err != nil {
		return fmt.Errorf("read InOutWRte_SF at %d failed: %w", addr+offInOutWRteSF, err)
	}
	if got := int16(v[0]); got != expectedInOutWRteSF {
		return fmt.Errorf("%w: InOutWRte_SF at %d is %d, want %d", ErrUnexpectedStorageModel, addr+offInOutWRteSF, got, expectedInOutWRteSF)
	}
	return nil
}

// sunspecTypeForInverterModel maps a SunSpec inverter model ID to the
// inverter's model type setting.
func sunspecTypeForInverterModel(id uint16) (SunSpecType, bool) {
	switch {
	case id >= 101 && id <= 103:
		return SunSpecIntSF, true
	case id >= 111 && id <= 113:
		return SunSpecFloat, true
	}
	return "", false
}

// DetectSunSpecType reads the SunSpec model chain (read-only) and reports
// whether the inverter is set to "int + SF" or "float".
func DetectSunSpecType(modbusIP string, port ...string) (SunSpecType, error) {
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	if err := OpenModbusClient("tcp", modbusIP, p); err != nil {
		return "", err
	}
	defer func() {
		if cerr := ClosemodbusClient(); cerr != nil {
			u.Log.Warnf("error closing modbus client: %v", cerr)
		}
	}()

	ok, err := hasSunSpecMarker()
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrSunSpecMarkerNotFound
	}
	id, _, _, found, err := findModel(func(id uint16) bool {
		_, isInverter := sunspecTypeForInverterModel(id)
		return isInverter
	})
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrInverterModelNotFound
	}
	t, _ := sunspecTypeForInverterModel(id)
	return t, nil
}

// VerifySunSpecType compares the configured type with the one the inverter
// reports and logs a warning on mismatch. It only reads registers and never
// changes behaviour; a failed check is logged and ignored.
func VerifySunSpecType(configured SunSpecType, modbusIP string, port ...string) {
	detected, err := DetectSunSpecType(modbusIP, port...)
	if err != nil {
		u.Log.Warnf("could not verify the inverter SunSpec model type (sunspec_type=%s): %v", configured, err)
		return
	}
	switch {
	case configured == SunSpecAuto:
		u.Log.Infof("sunspec_type=auto: inverter reports SunSpec model type %s", detected)
	case configured != detected:
		u.Log.Warnf("sunspec_type=%s but the inverter reports SunSpec model type %s: battery control will not work as expected, set sunspec_type to %s or auto", configured, detected, detected)
	default:
		u.Log.Infof("sunspec_type=%s matches the inverter SunSpec model type", configured)
	}
}
