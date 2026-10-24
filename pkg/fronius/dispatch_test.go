package fronius

import (
	"encoding/binary"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tbrandon/mbserver"
)

const dispatchPort = "6503"

// fakeInverter wraps an mbserver with an ordered write recorder, write-fault
// injection by address and read-back corruption by address. All register
// access goes through mu so the race detector sees the synchronisation.
type fakeInverter struct {
	srv *mbserver.Server

	mu        sync.Mutex
	writes    []RegisterWrite   // 1-based addresses, in arrival order
	failWrite map[uint16]bool   // 1-based address -> write returns an exception
	corrupt   map[uint16]bool   // 1-based address -> stored value is altered
	regs      map[uint16]uint16 // mirror of written/preset values, 1-based
}

func newFakeInverter(t *testing.T, wChaMaxRaw uint16, wChaMaxSF int16, rateSF int16) *fakeInverter {
	t.Helper()
	f := &fakeInverter{
		srv:       mbserver.NewServer(),
		failWrite: map[uint16]bool{},
		corrupt:   map[uint16]bool{},
		regs:      map[uint16]uint16{},
	}
	f.set(WChaMax, wChaMaxRaw)
	f.set(WChaMax_SF, uint16(wChaMaxSF))
	f.set(InOutWRte_SF, uint16(rateSF))

	f.srv.RegisterFunctionHandler(6, f.writeSingle)
	f.srv.RegisterFunctionHandler(3, f.readHolding)
	require.NoError(t, f.srv.ListenTCP(modbus_ip+":"+dispatchPort))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeInverter) set(addr uint16, v uint16) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.srv.HoldingRegisters[addr-1] = v
	f.regs[addr] = v
}

func (f *fakeInverter) get(addr uint16) int16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int16(f.regs[addr])
}

func (f *fakeInverter) written(addr uint16) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, w := range f.writes {
		if w.Addr == addr {
			return true
		}
	}
	return false
}

func (f *fakeInverter) recorded() []RegisterWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]RegisterWrite(nil), f.writes...)
}

func (f *fakeInverter) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = nil
}

func (f *fakeInverter) writeSingle(s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	data := frame.GetData()
	addr := binary.BigEndian.Uint16(data[0:2]) + 1
	value := binary.BigEndian.Uint16(data[2:4])

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite[addr] {
		return []byte{}, &mbserver.SlaveDeviceFailure
	}
	f.writes = append(f.writes, RegisterWrite{Addr: addr, Value: int16(value)})
	if f.corrupt[addr] {
		value++
	}
	s.HoldingRegisters[addr-1] = value
	f.regs[addr] = value
	return data[0:4], &mbserver.Success
}

func (f *fakeInverter) readHolding(s *mbserver.Server, frame mbserver.Framer) ([]byte, *mbserver.Exception) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return mbserver.ReadHoldingRegisters(s, frame)
}

func regsOf(writes []RegisterWrite) map[uint16]int16 {
	m := map[uint16]int16{}
	for _, w := range writes {
		m[w.Addr] = w.Value
	}
	return m
}

func addrOrder(writes []RegisterWrite) []uint16 {
	out := make([]uint16, 0, len(writes))
	for _, w := range writes {
		out = append(out, w.Addr)
	}
	return out
}

var validLimits = InverterLimits{WChaMaxW: 5000, RateSF: -2}

// T-0100 / TC-003d: register-level mapping for all five actions against the
// ADR-0003 table, with WChaMax = 5000 W and SF = -2.
func TestApplyDispatch_RegisterTable_T0100(t *testing.T) {
	f := newFakeInverter(t, 5000, 0, -2)

	cases := []struct {
		name    string
		cmd     DispatchCommand
		mode    DispatchMode
		out     int16
		in      int16
		storCtl int16
		revert  bool
	}{
		{"self_consume", DispatchCommand{Mode: DispatchSelfConsume}, DispatchSelfConsume, 10000, 10000, 0, false},
		{"charge 2500W", DispatchCommand{Mode: DispatchCharge, RateW: 2500}, DispatchCharge, -5000, 10000, 2, true},
		{"hold", DispatchCommand{Mode: DispatchHold}, DispatchHold, 0, 10000, 2, true},
		{"standby", DispatchCommand{Mode: DispatchStandby}, DispatchStandby, 0, 0, 3, true},
		{"discharge 3000W", DispatchCommand{Mode: DispatchDischarge, RateW: 3000}, DispatchDischarge, 10000, -6000, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.reset()
			f.set(InOutWRte_RvrtTms, 0)
			applied, err := ApplyDispatch(modbus_ip, tc.cmd, dispatchPort)
			require.NoError(t, err)
			assert.Equal(t, tc.mode, applied.Mode)
			assert.Equal(t, validLimits, applied.Limits)

			assert.Equal(t, tc.storCtl, f.get(StorCtl_Mod))
			assert.Equal(t, tc.out, f.get(OutWRte))
			assert.Equal(t, tc.in, f.get(InWRte))
			assert.Equal(t, int16(0), f.get(MinRsvPct))
			assert.Equal(t, int16(1), f.get(ChaGriSet))
			// T-0101 / TC-001a: 40359 = 3900 on every forced action, untouched for self_consume.
			assert.Equal(t, tc.revert, f.written(InOutWRte_RvrtTms))
			if tc.revert {
				assert.Equal(t, int16(RevertSeconds), f.get(InOutWRte_RvrtTms))
			}
			assert.False(t, f.written(WChaMax), "WChaMax must never be written")
		})
	}
}

// T-0101 / TC-001a: discharge at 3000 W writes 40359 in [3600, 7200) and StorCtl_Mod=1.
func TestApplyDispatch_DischargeRevertTimeout_T0101(t *testing.T) {
	f := newFakeInverter(t, 5000, 0, -2)
	_, err := ApplyDispatch(modbus_ip, DispatchCommand{Mode: DispatchDischarge, RateW: 3000}, dispatchPort)
	require.NoError(t, err)
	r := f.get(InOutWRte_RvrtTms)
	assert.GreaterOrEqual(t, int(r), 3600)
	assert.Less(t, int(r), 7200)
	assert.Equal(t, int16(1), f.get(StorCtl_Mod))
}

// T-0101: a custom revert timeout is honoured; out-of-range values are rejected.
func TestBuildDispatchRegisters_RevertTimeout_T0101(t *testing.T) {
	w, err := BuildDispatchRegisters(DispatchCommand{Mode: DispatchHold, RevertS: 2100}, validLimits)
	require.NoError(t, err)
	assert.Equal(t, int16(2100), regsOf(w)[InOutWRte_RvrtTms])

	w, err = BuildDispatchRegisters(DispatchCommand{Mode: DispatchHold, RevertS: MaxRevertSeconds}, validLimits)
	require.NoError(t, err)
	assert.Equal(t, uint16(MaxRevertSeconds), uint16(regsOf(w)[InOutWRte_RvrtTms]))

	for _, r := range []int{-1, MaxRevertSeconds + 1} {
		_, err = BuildDispatchRegisters(DispatchCommand{Mode: DispatchHold, RevertS: r}, validLimits)
		assert.ErrorIs(t, err, ErrInvalidDispatchCommand)
	}

	w, err = BuildDispatchRegisters(DispatchCommand{Mode: DispatchSelfConsume, RevertS: 2100}, validLimits)
	require.NoError(t, err)
	_, has := regsOf(w)[InOutWRte_RvrtTms]
	assert.False(t, has, "self_consume never writes 40359")
}

// T-0102 / S-005: forced actions write StorCtl_Mod last (rates first, then
// ChaGriSet/MinRsvPct, then 40359); defaults write StorCtl_Mod first.
func TestApplyDispatch_WriteOrder_T0102(t *testing.T) {
	f := newFakeInverter(t, 5000, 0, -2)
	forcedOrder := []uint16{OutWRte, InWRte, ChaGriSet, MinRsvPct, InOutWRte_RvrtTms, StorCtl_Mod}
	defaultsOrder := []uint16{StorCtl_Mod, OutWRte, InWRte, MinRsvPct, ChaGriSet}

	for _, m := range []DispatchMode{DispatchCharge, DispatchHold, DispatchStandby, DispatchDischarge} {
		f.reset()
		_, err := ApplyDispatch(modbus_ip, DispatchCommand{Mode: m, RateW: 1000}, dispatchPort)
		require.NoError(t, err)
		assert.Equal(t, forcedOrder, addrOrder(f.recorded()), "mode %s", m)
	}

	f.reset()
	_, err := ApplyDispatch(modbus_ip, DispatchCommand{Mode: DispatchSelfConsume}, dispatchPort)
	require.NoError(t, err)
	assert.Equal(t, defaultsOrder, addrOrder(f.recorded()))

	f.reset()
	require.NoError(t, SetDefaultsOrdered(modbus_ip, dispatchPort))
	assert.Equal(t, defaultsOrder, addrOrder(f.recorded()))
}

// T-0103 / TC-003c: rates are clamped in float to min(limit, WChaMax) and the
// sign lands only on the documented register for every action.
func TestBuildDispatchRegisters_ClampAndSign_T0103(t *testing.T) {
	// Plan 9000 W, limit 4500 W, WChaMax 5000 W -> 90 % -> InWRte = -9000.
	w, err := BuildDispatchRegisters(DispatchCommand{Mode: DispatchDischarge, RateW: 9000, MaxRateW: 4500}, validLimits)
	require.NoError(t, err)
	assert.Equal(t, int16(-9000), regsOf(w)[InWRte])

	cases := []struct {
		name string
		cmd  DispatchCommand
		out  int16
		in   int16
	}{
		{"charge negative OutWRte", DispatchCommand{Mode: DispatchCharge, RateW: 1000}, -2000, 10000},
		{"discharge negative InWRte", DispatchCommand{Mode: DispatchDischarge, RateW: 1000}, 10000, -2000},
		{"charge above WChaMax clamps to -10000", DispatchCommand{Mode: DispatchCharge, RateW: 1e9}, -10000, 10000},
		{"discharge above WChaMax clamps to -10000", DispatchCommand{Mode: DispatchDischarge, RateW: 40000}, 10000, -10000},
		{"limit above WChaMax ignored", DispatchCommand{Mode: DispatchCharge, RateW: 9000, MaxRateW: 9000}, -10000, 10000},
		{"hold non-negative", DispatchCommand{Mode: DispatchHold, RateW: 4000}, 0, 10000},
		{"standby non-negative", DispatchCommand{Mode: DispatchStandby, RateW: 4000}, 0, 0},
		{"self_consume non-negative", DispatchCommand{Mode: DispatchSelfConsume, RateW: 4000}, 10000, 10000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := BuildDispatchRegisters(tc.cmd, validLimits)
			require.NoError(t, err)
			r := regsOf(w)
			assert.Equal(t, tc.out, r[OutWRte])
			assert.Equal(t, tc.in, r[InWRte])
			assert.GreaterOrEqual(t, r[OutWRte], int16(-10000))
			assert.GreaterOrEqual(t, r[InWRte], int16(-10000))
		})
	}
}

// T-0103 edge: a charge/discharge rate rounding to 0 % becomes hold/standby.
func TestBuildDispatch_ZeroRateDowngrade_T0103(t *testing.T) {
	mode, w, err := buildDispatch(DispatchCommand{Mode: DispatchCharge, RateW: 0.2}, validLimits)
	require.NoError(t, err)
	assert.Equal(t, DispatchHold, mode)
	assert.Equal(t, int16(0), regsOf(w)[OutWRte])
	assert.Equal(t, int16(2), regsOf(w)[StorCtl_Mod])

	mode, w, err = buildDispatch(DispatchCommand{Mode: DispatchDischarge, RateW: 0}, validLimits)
	require.NoError(t, err)
	assert.Equal(t, DispatchStandby, mode)
	assert.Equal(t, int16(3), regsOf(w)[StorCtl_Mod])
}

// T-0104 / TC-003a/b: invalid limits or rates return an error and no writes.
func TestBuildDispatchRegisters_Rejects_T0104(t *testing.T) {
	cases := []struct {
		name string
		cmd  DispatchCommand
		lim  InverterLimits
		want error
	}{
		{"WChaMax 0", DispatchCommand{Mode: DispatchDischarge, RateW: 3000}, InverterLimits{0, -2}, ErrInverterLimits},
		{"WChaMax below 100", DispatchCommand{Mode: DispatchCharge, RateW: 50}, InverterLimits{99, -2}, ErrInverterLimits},
		{"WChaMax above 30000", DispatchCommand{Mode: DispatchCharge, RateW: 50}, InverterLimits{30001, -2}, ErrInverterLimits},
		{"WChaMax negative", DispatchCommand{Mode: DispatchHold}, InverterLimits{-5000, -2}, ErrInverterLimits},
		{"WChaMax NaN", DispatchCommand{Mode: DispatchHold}, InverterLimits{math.NaN(), -2}, ErrInverterLimits},
		{"WChaMax Inf", DispatchCommand{Mode: DispatchHold}, InverterLimits{math.Inf(1), -2}, ErrInverterLimits},
		{"SF -1", DispatchCommand{Mode: DispatchCharge, RateW: 1000}, InverterLimits{5000, -1}, ErrInverterLimits},
		{"SF 0", DispatchCommand{Mode: DispatchSelfConsume}, InverterLimits{5000, 0}, ErrInverterLimits},
		{"rate NaN", DispatchCommand{Mode: DispatchCharge, RateW: math.NaN()}, validLimits, ErrInvalidDispatchCommand},
		{"rate +Inf", DispatchCommand{Mode: DispatchDischarge, RateW: math.Inf(1)}, validLimits, ErrInvalidDispatchCommand},
		{"rate -Inf", DispatchCommand{Mode: DispatchDischarge, RateW: math.Inf(-1)}, validLimits, ErrInvalidDispatchCommand},
		{"rate negative", DispatchCommand{Mode: DispatchCharge, RateW: -1000}, validLimits, ErrInvalidDispatchCommand},
		{"max rate NaN", DispatchCommand{Mode: DispatchCharge, RateW: 1000, MaxRateW: math.NaN()}, validLimits, ErrInvalidDispatchCommand},
		{"unknown mode", DispatchCommand{Mode: "boost", RateW: 1000}, validLimits, ErrInvalidDispatchCommand},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := BuildDispatchRegisters(tc.cmd, tc.lim)
			assert.ErrorIs(t, err, tc.want)
			assert.Nil(t, w)
		})
	}
}

// T-0104: implausible limits read from the inverter mean nothing is written.
func TestApplyDispatch_BadInverterLimitsWritesNothing_T0104(t *testing.T) {
	cases := []struct {
		name   string
		raw    uint16
		wSF    int16
		rateSF int16
	}{
		{"WChaMax 0", 0, 0, -2},
		{"SF -1", 5000, 0, -1},
		{"WChaMax scaled too large", 5000, 1, -2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeInverter(t, tc.raw, tc.wSF, tc.rateSF)
			_, err := ApplyDispatch(modbus_ip, DispatchCommand{Mode: DispatchDischarge, RateW: 3000}, dispatchPort)
			assert.ErrorIs(t, err, ErrInverterLimits)
			assert.Empty(t, f.recorded())
		})
	}
}

// ReadInverterLimits applies WChaMax_SF and returns the rate SF as read.
func TestReadInverterLimits(t *testing.T) {
	newFakeInverter(t, 1050, 1, -2)
	lim, err := ReadInverterLimits(modbus_ip, dispatchPort)
	require.NoError(t, err)
	assert.InDelta(t, 10500.0, lim.WChaMaxW, 1e-9)
	assert.Equal(t, int16(-2), lim.RateSF)
}

func TestReadInverterLimitsConnectionError(t *testing.T) {
	_, err := ReadInverterLimits(modbus_ip, dispatchPort)
	assert.Error(t, err)
}

// T-0105 / TC-005a: a failing InWRte write during discharge never sets
// StorCtl_Mod=1, returns an error and attempts defaults.
func TestApplyDispatch_WriteFailure_T0105(t *testing.T) {
	f := newFakeInverter(t, 5000, 0, -2)
	f.set(StorCtl_Mod, 0)
	f.mu.Lock()
	f.failWrite[InWRte] = true
	f.mu.Unlock()

	applied, err := ApplyDispatch(modbus_ip, DispatchCommand{Mode: DispatchDischarge, RateW: 3000}, dispatchPort)
	assert.ErrorIs(t, err, ErrModbusWrite)
	assert.NotEqual(t, int16(1), f.get(StorCtl_Mod))
	// The defaults restore was attempted: StorCtl_Mod=0 was written after the failure.
	rec := f.recorded()
	require.NotEmpty(t, rec)
	assert.Equal(t, uint16(OutWRte), rec[0].Addr)
	assert.Equal(t, RegisterWrite{StorCtl_Mod, 0}, rec[1])
	// InWRte keeps failing, so the restore cannot complete.
	assert.False(t, applied.DefaultsRestored)
	for _, w := range rec {
		assert.False(t, w.Addr == StorCtl_Mod && w.Value == 1, "StorCtl_Mod=1 must not be written")
	}
}

// T-0105: when the failure is transient the defaults restore succeeds.
func TestApplyDispatch_WriteFailureDefaultsRestored_T0105(t *testing.T) {
	f := newFakeInverter(t, 5000, 0, -2)
	f.mu.Lock()
	f.failWrite[InOutWRte_RvrtTms] = true
	f.mu.Unlock()

	applied, err := ApplyDispatch(modbus_ip, DispatchCommand{Mode: DispatchCharge, RateW: 3000}, dispatchPort)
	assert.ErrorIs(t, err, ErrModbusWrite)
	assert.True(t, applied.DefaultsRestored)
	assert.Equal(t, int16(0), f.get(StorCtl_Mod))
	assert.Equal(t, int16(10000), f.get(OutWRte))
}

// T-0106 / TC-005b: a read-back that differs from the written value is an error.
func TestApplyDispatch_ReadBackMismatch_T0106(t *testing.T) {
	f := newFakeInverter(t, 5000, 0, -2)
	f.mu.Lock()
	f.corrupt[OutWRte] = true
	f.mu.Unlock()

	_, err := ApplyDispatch(modbus_ip, DispatchCommand{Mode: DispatchCharge, RateW: 2500}, dispatchPort)
	assert.ErrorIs(t, err, ErrModbusWrite)
	assert.ErrorIs(t, err, ErrReadBackMismatch)
}

// T-0106: SetDefaultsOrdered also verifies by read-back.
func TestSetDefaultsOrdered_ReadBackMismatch_T0106(t *testing.T) {
	f := newFakeInverter(t, 5000, 0, -2)
	f.mu.Lock()
	f.corrupt[ChaGriSet] = true
	f.mu.Unlock()
	err := SetDefaultsOrdered(modbus_ip, dispatchPort)
	assert.ErrorIs(t, err, ErrReadBackMismatch)
}

func TestApplyDispatch_ConnectionError(t *testing.T) {
	_, err := ApplyDispatch(modbus_ip, DispatchCommand{Mode: DispatchHold}, dispatchPort)
	assert.Error(t, err)
}

// S-005: the ordered writer refuses registers outside the allowlist.
func TestWriteFroniusModbusRegistersOrdered_Allowlist(t *testing.T) {
	f := newFakeInverter(t, 5000, 0, -2)
	require.NoError(t, OpenModbusClient("tcp", modbus_ip, dispatchPort))
	err := WriteFroniusModbusRegistersOrdered([]RegisterWrite{{StorCtl_Mod, 0}, {WChaMax, 1000}})
	assert.NoError(t, ClosemodbusClient())
	assert.ErrorIs(t, err, ErrRegisterNotAllowed)
	assert.Empty(t, f.recorded(), "nothing is written when any register is disallowed")
}

// T-0108 / ADR-0005: revert timeout for an optimize-mode force_charge.
func TestForceChargeRevertSeconds_T0108(t *testing.T) {
	at := time.Date(2026, 10, 2, 10, 10, 0, 0, time.Local)
	assert.Equal(t, 2100, ForceChargeRevertSeconds(1800, at))
	assert.Equal(t, 3300, ForceChargeRevertSeconds(0, at))
	assert.Equal(t, 3300, ForceChargeRevertSeconds(-5, at))
	assert.Equal(t, 600, ForceChargeRevertSeconds(0, time.Date(2026, 10, 2, 23, 55, 0, 0, time.Local)))
	// ADR-0005 caps at 86400; the uint16 register caps lower, at 65535.
	assert.Equal(t, MaxRevertSeconds, ForceChargeRevertSeconds(86400, at))
	assert.Equal(t, MaxRevertSeconds, ForceChargeRevertSeconds(math.MaxInt32, at))
}

// T-0108: ForceChargeWithRevert writes the ForceCharge registers plus 40359.
func TestForceChargeWithRevert_T0108(t *testing.T) {
	f := newFakeInverter(t, 5000, 0, -2)
	require.NoError(t, ForceChargeWithRevert(modbus_ip, 50, 2100, dispatchPort))
	assert.Equal(t, int16(2), f.get(StorCtl_Mod))
	assert.Equal(t, int16(-5000), f.get(OutWRte))
	assert.Equal(t, int16(10000), f.get(InWRte))
	assert.Equal(t, int16(0), f.get(MinRsvPct))
	assert.Equal(t, int16(1), f.get(ChaGriSet))
	assert.Equal(t, int16(2100), f.get(InOutWRte_RvrtTms))
	rec := f.recorded()
	assert.Equal(t, uint16(StorCtl_Mod), rec[len(rec)-1].Addr, "StorCtl_Mod written last")

	// 0 % restores defaults.
	f.reset()
	require.NoError(t, ForceChargeWithRevert(modbus_ip, 0, 2100, dispatchPort))
	assert.Equal(t, int16(0), f.get(StorCtl_Mod))
	assert.False(t, f.written(InOutWRte_RvrtTms))
}

func TestForceChargeWithRevert_Rejects_T0108(t *testing.T) {
	f := newFakeInverter(t, 5000, 0, -2)
	for _, tc := range []struct {
		pct    int16
		revert int
	}{{101, 2100}, {400, 2100}, {-1, 2100}, {50, 0}, {50, MaxRevertSeconds + 1}} {
		err := ForceChargeWithRevert(modbus_ip, tc.pct, tc.revert, dispatchPort)
		assert.ErrorIs(t, err, ErrInvalidDispatchCommand, "pct %d revert %d", tc.pct, tc.revert)
	}
	assert.Empty(t, f.recorded())
}

func TestForceChargeWithRevert_WriteFailureRestoresDefaults(t *testing.T) {
	f := newFakeInverter(t, 5000, 0, -2)
	f.mu.Lock()
	f.failWrite[InOutWRte_RvrtTms] = true
	f.mu.Unlock()
	err := ForceChargeWithRevert(modbus_ip, 50, 2100, dispatchPort)
	assert.ErrorIs(t, err, ErrModbusWrite)
	assert.Equal(t, int16(0), f.get(StorCtl_Mod))
	assert.Equal(t, int16(10000), f.get(OutWRte))
}
