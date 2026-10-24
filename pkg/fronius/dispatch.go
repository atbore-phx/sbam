package fronius

import (
	"errors"
	"fmt"
	"math"
	"time"

	u "sbam/src/utils"

	"github.com/simonvetter/modbus"
)

// Additional SunSpec model 124 registers (1-based) used by the optimizer
// dispatch path. See ADR-0003.
const (
	InOutWRte_RvrtTms = 40359 // revert timeout for InWRte/OutWRte, seconds (uint16)
	WChaMax_SF        = 40362 // scale factor for WChaMax
	InOutWRte_SF      = 40369 // scale factor for InWRte/OutWRte (Fronius: -2)
)

const (
	// RevertSeconds is the revert timeout written with every forced optimizer
	// action: one hourly cycle plus margin for the worst-case tick (ADR-0003/0004).
	RevertSeconds = 3900
	// MaxRevertSeconds is the largest value register 40359 (uint16) can hold.
	MaxRevertSeconds = math.MaxUint16
	// forceChargeRevertMarginS is added to a manual force_charge duration (ADR-0005).
	forceChargeRevertMarginS = 300
	// forceChargeRevertCapS is the ADR-0005 cap; the register limit applies on top.
	forceChargeRevertCapS = 86400

	// MinWChaMaxW and MaxWChaMaxW bound a plausible WChaMax reading (S-003).
	MinWChaMaxW = 100.0
	MaxWChaMaxW = 30000.0
	// ExpectedRateSF is the only InOutWRte_SF value sbam accepts.
	ExpectedRateSF int16 = -2

	ratePct100 = 10000 // 100 % at scale factor -2
)

// Sentinel errors so callers can map failures to fallback reasons.
var (
	// ErrInvalidDispatchCommand is returned for an unknown mode, a non-finite or
	// negative rate, or an out-of-range revert timeout.
	ErrInvalidDispatchCommand = errors.New("invalid dispatch command")
	// ErrInverterLimits is returned when WChaMax or InOutWRte_SF is implausible
	// (maps to optimizer_fallback_inverter_limits).
	ErrInverterLimits = errors.New("implausible inverter limits")
	// ErrModbusWrite is returned on a write failure or read-back mismatch
	// (maps to optimizer_fallback_modbus_write_failed). When ApplyDispatch
	// returns it, a defaults restore has already been attempted once.
	ErrModbusWrite = errors.New("modbus write failed")
	// ErrReadBackMismatch is wrapped together with ErrModbusWrite when a
	// register reads back a different value than was written.
	ErrReadBackMismatch = errors.New("modbus read-back mismatch")
	// ErrRegisterNotAllowed is returned when a write targets a register
	// outside the storage-control allowlist.
	ErrRegisterNotAllowed = errors.New("register not in write allowlist")
)

// DispatchMode is the inverter storage action for one slot.
type DispatchMode string

const (
	DispatchCharge      DispatchMode = "charge"
	DispatchHold        DispatchMode = "hold"
	DispatchStandby     DispatchMode = "standby"
	DispatchDischarge   DispatchMode = "discharge"
	DispatchSelfConsume DispatchMode = "self_consume"
)

// DispatchCommand is the action to apply.
type DispatchCommand struct {
	Mode DispatchMode
	// RateW is the requested charge/discharge rate in W (charge and discharge only).
	RateW float64
	// RevertS is the 40359 revert timeout for forced actions; 0 means RevertSeconds.
	RevertS int
	// MaxRateW is the configured rate limit (opt_max_charge_rate_w or
	// opt_max_discharge_rate_w) in W; 0 means only WChaMax limits the rate.
	MaxRateW float64
}

// RegisterWrite is one holding-register write (1-based SunSpec address).
type RegisterWrite struct {
	Addr  uint16
	Value int16
}

// InverterLimits are the values read from the inverter before every dispatch.
type InverterLimits struct {
	WChaMaxW float64
	RateSF   int16
}

// Applied describes what ApplyDispatch did.
type Applied struct {
	// Mode is the effective mode (a charge/discharge whose rate rounds to 0 %
	// becomes hold/standby).
	Mode   DispatchMode
	Writes []RegisterWrite
	Limits InverterLimits
	// DefaultsRestored is true when a write failed and the defaults fallback succeeded.
	DefaultsRestored bool
}

// allowedWriteRegisters is the storage-control write allowlist (spec Technical
// Constraints). WChaMax is read, never written.
var allowedWriteRegisters = map[uint16]bool{
	StorCtl_Mod:       true,
	OutWRte:           true,
	InWRte:            true,
	MinRsvPct:         true,
	ChaGriSet:         true,
	InOutWRte_RvrtTms: true,
}

// defaultsWrites returns the mdsc defaults with StorCtl_Mod=0 first (S-005).
func defaultsWrites() []RegisterWrite {
	return []RegisterWrite{
		{StorCtl_Mod, 0},
		{OutWRte, ratePct100},
		{InWRte, ratePct100},
		{MinRsvPct, 0},
		{ChaGriSet, 1},
	}
}

// forcedWrites returns a forced-action register set in the ADR-0003 order:
// rates, ChaGriSet/MinRsvPct, revert timeout, StorCtl_Mod last.
func forcedWrites(outWRte, inWRte int16, revertS int, storCtlMod int16) []RegisterWrite {
	return []RegisterWrite{
		{OutWRte, outWRte},
		{InWRte, inWRte},
		{ChaGriSet, 1},
		{MinRsvPct, 0},
		{InOutWRte_RvrtTms, int16(uint16(revertS))},
		{StorCtl_Mod, storCtlMod},
	}
}

func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// validateLimits rejects implausible inverter limits (S-003).
func validateLimits(lim InverterLimits) error {
	if !isFinite(lim.WChaMaxW) || lim.WChaMaxW < MinWChaMaxW || lim.WChaMaxW > MaxWChaMaxW {
		return fmt.Errorf("%w: WChaMax %v W outside [%v, %v]", ErrInverterLimits, lim.WChaMaxW, MinWChaMaxW, MaxWChaMaxW)
	}
	if lim.RateSF != ExpectedRateSF {
		return fmt.Errorf("%w: InOutWRte_SF %d, expected %d", ErrInverterLimits, lim.RateSF, ExpectedRateSF)
	}
	return nil
}

// resolveRevert returns the revert timeout for a forced action.
func resolveRevert(revertS int) (int, error) {
	if revertS == 0 {
		return RevertSeconds, nil
	}
	if revertS < 0 || revertS > MaxRevertSeconds {
		return 0, fmt.Errorf("%w: revert timeout %d s outside [1, %d]", ErrInvalidDispatchCommand, revertS, MaxRevertSeconds)
	}
	return revertS, nil
}

// rateToPct converts a rate in W to percent·100 of WChaMax. The rate is
// clamped in float to [0, min(maxRateW, WChaMax)] and the result to
// [0, 10000] before the int16 conversion (S-003).
func rateToPct(rateW, maxRateW, wChaMaxW float64) int16 {
	limit := wChaMaxW
	if maxRateW > 0 && maxRateW < limit {
		limit = maxRateW
	}
	rate := math.Min(math.Max(rateW, 0), limit)
	pf := math.Round(rate / wChaMaxW * ratePct100)
	pf = math.Min(math.Max(pf, 0), ratePct100)
	return int16(pf)
}

// buildDispatch is BuildDispatchRegisters plus the effective mode.
func buildDispatch(cmd DispatchCommand, lim InverterLimits) (DispatchMode, []RegisterWrite, error) {
	if !isFinite(cmd.RateW) || cmd.RateW < 0 {
		return "", nil, fmt.Errorf("%w: rate %v W", ErrInvalidDispatchCommand, cmd.RateW)
	}
	if !isFinite(cmd.MaxRateW) || cmd.MaxRateW < 0 {
		return "", nil, fmt.Errorf("%w: max rate %v W", ErrInvalidDispatchCommand, cmd.MaxRateW)
	}
	switch cmd.Mode {
	case DispatchCharge, DispatchHold, DispatchStandby, DispatchDischarge, DispatchSelfConsume:
	default:
		return "", nil, fmt.Errorf("%w: unknown mode %q", ErrInvalidDispatchCommand, cmd.Mode)
	}
	if err := validateLimits(lim); err != nil {
		return "", nil, err
	}
	if cmd.Mode == DispatchSelfConsume {
		return DispatchSelfConsume, defaultsWrites(), nil
	}
	revert, err := resolveRevert(cmd.RevertS)
	if err != nil {
		return "", nil, err
	}

	mode := cmd.Mode
	var p int16
	if mode == DispatchCharge || mode == DispatchDischarge {
		p = rateToPct(cmd.RateW, cmd.MaxRateW, lim.WChaMaxW)
		if p == 0 {
			downgraded := DispatchHold
			if mode == DispatchDischarge {
				downgraded = DispatchStandby
			}
			u.Log.Debugf("dispatch %s rate %.0f W rounds to 0 %%, emitting %s", mode, cmd.RateW, downgraded)
			mode = downgraded
		}
	}

	switch mode {
	case DispatchCharge:
		return mode, forcedWrites(-p, ratePct100, revert, 2), nil
	case DispatchHold:
		return mode, forcedWrites(0, ratePct100, revert, 2), nil
	case DispatchStandby:
		return mode, forcedWrites(0, 0, revert, 3), nil
	default: // DispatchDischarge
		return mode, forcedWrites(ratePct100, -p, revert, 1), nil
	}
}

// BuildDispatchRegisters is the single source of truth for action → registers
// (ADR-0003). It is pure: no I/O, no global state.
func BuildDispatchRegisters(cmd DispatchCommand, lim InverterLimits) ([]RegisterWrite, error) {
	_, writes, err := buildDispatch(cmd, lim)
	return writes, err
}

// readInverterLimits reads WChaMax, WChaMax_SF and InOutWRte_SF on the open client.
func readInverterLimits() (InverterLimits, error) {
	if modbusClient == nil {
		return InverterLimits{}, errors.New("modbus client not open")
	}
	raw, err := modbusClient.ReadRegister(WChaMax-1, modbus.HOLDING_REGISTER)
	if err != nil {
		return InverterLimits{}, fmt.Errorf("read WChaMax: %w", err)
	}
	wSF, err := modbusClient.ReadRegister(WChaMax_SF-1, modbus.HOLDING_REGISTER)
	if err != nil {
		return InverterLimits{}, fmt.Errorf("read WChaMax_SF: %w", err)
	}
	rateSF, err := modbusClient.ReadRegister(InOutWRte_SF-1, modbus.HOLDING_REGISTER)
	if err != nil {
		return InverterLimits{}, fmt.Errorf("read InOutWRte_SF: %w", err)
	}
	lim := InverterLimits{
		WChaMaxW: float64(raw) * math.Pow10(int(int16(wSF))),
		RateSF:   int16(rateSF),
	}
	u.Log.Debugf("inverter limits: WChaMax raw %d SF %d -> %.0f W, InOutWRte_SF %d", raw, int16(wSF), lim.WChaMaxW, lim.RateSF)
	return lim, nil
}

// ReadInverterLimits opens a Modbus connection, reads 40346, 40362 and 40369,
// and closes the connection. The values are returned unvalidated.
func ReadInverterLimits(ip string, port ...string) (InverterLimits, error) {
	if err := openModbus(ip, port...); err != nil {
		return InverterLimits{}, err
	}
	defer closeModbus()
	return readInverterLimits()
}

// WriteFroniusModbusRegistersOrdered writes the registers in slice order on
// the open client, then reads every written address back and compares (S-005).
func WriteFroniusModbusRegistersOrdered(writes []RegisterWrite) error {
	if modbusClient == nil {
		return fmt.Errorf("%w: modbus client not open", ErrModbusWrite)
	}
	for _, w := range writes {
		if !allowedWriteRegisters[w.Addr] {
			return fmt.Errorf("%w: %w: %d", ErrModbusWrite, ErrRegisterNotAllowed, w.Addr)
		}
	}
	expected := make(map[uint16]uint16, len(writes))
	for _, w := range writes {
		u.Log.Debugf("Writing register: %d ; value: %d", w.Addr, w.Value)
		if err := modbusClient.WriteRegister(w.Addr-1, uint16(w.Value)); err != nil {
			u.Log.Errorf("Error writing register %d value %d: %v", w.Addr, w.Value, err)
			return fmt.Errorf("%w: write register %d: %w", ErrModbusWrite, w.Addr, err)
		}
		expected[w.Addr] = uint16(w.Value)
	}
	for _, w := range writes {
		got, err := modbusClient.ReadRegister(w.Addr-1, modbus.HOLDING_REGISTER)
		if err != nil {
			u.Log.Errorf("Error reading back register %d: %v", w.Addr, err)
			return fmt.Errorf("%w: read back register %d: %w", ErrModbusWrite, w.Addr, err)
		}
		if got != expected[w.Addr] {
			u.Log.Errorf("Read-back mismatch on register %d: wrote %d, read %d", w.Addr, int16(expected[w.Addr]), int16(got))
			return fmt.Errorf("%w: %w: register %d wrote %d read %d", ErrModbusWrite, ErrReadBackMismatch, w.Addr, int16(expected[w.Addr]), int16(got))
		}
	}
	return nil
}

func openModbus(ip string, port ...string) error {
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	return OpenModbusClient("tcp", ip, p)
}

func closeModbus() {
	if cerr := ClosemodbusClient(); cerr != nil {
		u.Log.Warnf("error closing modbus client: %v", cerr)
	}
}

// writeOrdered opens a connection, performs an ordered write with read-back
// and closes the connection.
func writeOrdered(ip string, writes []RegisterWrite, port ...string) error {
	if err := openModbus(ip, port...); err != nil {
		return err
	}
	defer closeModbus()
	return WriteFroniusModbusRegistersOrdered(writes)
}

// SetDefaultsOrdered writes the storage defaults with StorCtl_Mod=0 first and
// verifies them by read-back.
func SetDefaultsOrdered(ip string, port ...string) error {
	return writeOrdered(ip, defaultsWrites(), port...)
}

// restoreDefaultsAfterFailure attempts SetDefaultsOrdered once (S-005).
func restoreDefaultsAfterFailure(ip string, cause error, port ...string) bool {
	u.Log.Errorf("storage write failed (%v), restoring defaults", cause)
	if derr := SetDefaultsOrdered(ip, port...); derr != nil {
		u.Log.Errorf("restoring storage defaults failed: %v", derr)
		return false
	}
	u.Log.Info("storage defaults restored after write failure")
	return true
}

// ApplyDispatch reads the inverter limits, builds the registers, writes them in
// order and verifies them by read-back. On ErrInverterLimits or
// ErrInvalidDispatchCommand nothing is written and the caller restores
// defaults. On ErrModbusWrite the defaults restore has already been attempted
// once (Applied.DefaultsRestored reports the outcome).
func ApplyDispatch(ip string, cmd DispatchCommand, port ...string) (Applied, error) {
	if err := openModbus(ip, port...); err != nil {
		return Applied{}, err
	}
	lim, err := readInverterLimits()
	if err != nil {
		closeModbus()
		return Applied{}, err
	}
	mode, writes, err := buildDispatch(cmd, lim)
	if err != nil {
		closeModbus()
		u.Log.Errorf("dispatch rejected: %v", err)
		return Applied{Limits: lim}, err
	}
	applied := Applied{Mode: mode, Writes: writes, Limits: lim}
	u.Log.Infof("Applying storage dispatch %s (rate %.0f W, WChaMax %.0f W)", mode, cmd.RateW, lim.WChaMaxW)
	werr := WriteFroniusModbusRegistersOrdered(writes)
	closeModbus()
	if werr != nil {
		applied.DefaultsRestored = restoreDefaultsAfterFailure(ip, werr, port...)
		return applied, werr
	}
	return applied, nil
}

// ForceChargeRevertSeconds returns the 40359 value for an optimize-mode manual
// force_charge (ADR-0005): duration + 300 s, or the seconds to the end of the
// current local hour + 300 s when no duration is given. The result is capped
// at 86400 s (ADR-0005) and at the uint16 register limit.
func ForceChargeRevertSeconds(durationS int, now time.Time) int {
	base := durationS
	if base <= 0 {
		endOfHour := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()+1, 0, 0, 0, now.Location())
		base = int(math.Ceil(endOfHour.Sub(now).Seconds()))
	}
	r := base + forceChargeRevertMarginS
	if r > forceChargeRevertCapS {
		r = forceChargeRevertCapS
	}
	if r > MaxRevertSeconds {
		r = MaxRevertSeconds
	}
	return r
}

// ForceChargeWithRevert is the optimize-mode force charge: the same registers
// as ForceCharge (StorCtl_Mod=2, OutWRte=-100·pct) plus the revert timeout
// 40359, written in order with read-back. powerPrc 0 restores defaults. On a
// write failure defaults are attempted once.
func ForceChargeWithRevert(ip string, powerPrc int16, revertS int, port ...string) error {
	if powerPrc < 0 || powerPrc > 100 {
		return fmt.Errorf("%w: force charge percent %d outside [0, 100]", ErrInvalidDispatchCommand, powerPrc)
	}
	if powerPrc == 0 {
		u.Log.Info("percent of charging is <1%, skipping Force Charge and set defaults.")
		return SetDefaultsOrdered(ip, port...)
	}
	if revertS <= 0 || revertS > MaxRevertSeconds {
		return fmt.Errorf("%w: revert timeout %d s outside [1, %d]", ErrInvalidDispatchCommand, revertS, MaxRevertSeconds)
	}
	out := int16(int32(-100) * int32(powerPrc))
	writes := forcedWrites(out, ratePct100, revertS, 2)
	u.Log.Infof("Setting Fronius Storage Force Charge at %d%% with revert timeout %d s", powerPrc, revertS)
	if err := writeOrdered(ip, writes, port...); err != nil {
		if errors.Is(err, ErrModbusWrite) {
			restoreDefaultsAfterFailure(ip, err, port...)
		}
		return err
	}
	return nil
}
