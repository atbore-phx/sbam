package fronius

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tbrandon/mbserver"
)

const faultyPort = "6505"

// faultyInverter starts a mock inverter with the float SunSpec chain whose
// reads fail for the 1-based addresses in failRead and whose single-register
// writes fail when failWrite is set. Handlers are registered before the
// server listens, so no request can race with them.
func faultyInverter(t *testing.T, failRead map[uint16]bool, failWrite bool) *mbserver.Server {
	t.Helper()
	srv := mbserver.NewServer()
	srv.RegisterFunctionHandler(3, func(s *mbserver.Server, f mbserver.Framer) ([]byte, *mbserver.Exception) {
		data := f.GetData()
		start := binary.BigEndian.Uint16(data[0:2]) + 1
		n := binary.BigEndian.Uint16(data[2:4])
		for a := start; a < start+n; a++ {
			if failRead[a] {
				return []byte{}, &mbserver.IllegalDataAddress
			}
		}
		return mbserver.ReadHoldingRegisters(s, f)
	})
	if failWrite {
		srv.RegisterFunctionHandler(6, func(*mbserver.Server, mbserver.Framer) ([]byte, *mbserver.Exception) {
			return []byte{}, &mbserver.SlaveDeviceFailure
		})
	}
	set := func(a, v uint16) { srv.HoldingRegisters[a-1] = v }
	set(sunspecMarkerAddr, sunspecMarkerHi)
	set(sunspecMarkerAddr+1, sunspecMarkerLo)
	addr := uint16(sunspecFirstModelAddr)
	for _, m := range floatChain {
		set(addr, m.id)
		set(addr+1, m.length)
		if m.id == sunspecStorageModelID {
			set(addr+offInOutWRteSF, 0xFFFE)
		}
		addr += 2 + m.length
	}
	set(addr, sunspecEndModelID)
	require.NoError(t, srv.ListenTCP(modbus_ip+":"+faultyPort))
	t.Cleanup(srv.Close)
	return srv
}

// The float chain: inverter model header at 40070, storage model at 40354.
const (
	floatInverterHeader = 40070
	floatInOutWRteSF    = floatStorageModelAddr + offInOutWRteSF
	floatOutWRte        = floatStorageModelAddr + offOutWRte
)

func TestDiscoveryReadFailures(t *testing.T) {
	cases := map[string]struct {
		fail uint16
		want string
	}{
		"marker":       {sunspecMarkerAddr, "read SunSpec marker failed"},
		"model header": {floatInverterHeader, "read SunSpec model header"},
		"scale factor": {floatInOutWRteSF, "read InOutWRte_SF"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			faultyInverter(t, map[uint16]bool{tc.fail: true}, false)
			require.NoError(t, OpenModbusClient("tcp", modbus_ip, faultyPort))
			defer ClosemodbusClient()

			_, err := DiscoverStorageRegisters(SunSpecAuto)

			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestDetectSunSpecTypeReadFailures(t *testing.T) {
	for name, fail := range map[string]uint16{"marker": sunspecMarkerAddr, "model header": floatInverterHeader} {
		t.Run(name, func(t *testing.T) {
			faultyInverter(t, map[uint16]bool{fail: true}, false)

			_, err := DetectSunSpecType(modbus_ip, faultyPort)

			assert.ErrorContains(t, err, "failed")
		})
	}
}

func TestReadStorageControlModeDiscoveryFailure(t *testing.T) {
	faultyInverter(t, map[uint16]bool{sunspecMarkerAddr: true}, false)

	_, err := ReadStorageControlMode(SunSpecFloat, modbus_ip, faultyPort)

	assert.ErrorContains(t, err, "read SunSpec marker failed")
}

func TestStorageWriteFailures(t *testing.T) {
	t.Run("register read before write", func(t *testing.T) {
		faultyInverter(t, map[uint16]bool{floatOutWRte: true}, false)

		err := ApplyForceCharge(SunSpecFloat, modbus_ip, 50, faultyPort)

		assert.ErrorContains(t, err, "read register")
	})
	t.Run("write", func(t *testing.T) {
		faultyInverter(t, nil, true)

		err := ApplyForceCharge(SunSpecFloat, modbus_ip, 50, faultyPort)

		assert.ErrorContains(t, err, "write register")
	})
	t.Run("defaults through a zero charge", func(t *testing.T) {
		faultyInverter(t, nil, true)

		err := ApplyForceCharge(SunSpecAuto, modbus_ip, 0, faultyPort)

		assert.ErrorContains(t, err, "write register")
	})
	t.Run("hold", func(t *testing.T) {
		faultyInverter(t, nil, true)

		err := ApplyHold(SunSpecAuto, modbus_ip, faultyPort)

		assert.ErrorContains(t, err, "write register")
	})
}

// A NaN forecast makes classifyDecision fail: the classifier-error branch
// checks StorCtl_Mod and only resets the inverter when it is force-charging.
func TestClassifierErrorBranch(t *testing.T) {
	nan := math.NaN()

	t.Run("not force-charging: nothing written", func(t *testing.T) {
		srv := faultyInverter(t, nil, false)
		before := append([]uint16(nil), srv.HoldingRegisters...)

		_, _, _, _, err := setFroniusChargeBatteryMode(SunSpecFloat, true, nan, 5000, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, faultyPort)

		require.NoError(t, err)
		assert.Equal(t, before, srv.HoldingRegisters)
	})
	t.Run("force-charging: defaults restored", func(t *testing.T) {
		srv := faultyInverter(t, nil, false)
		srv.HoldingRegisters[40359-1] = 2

		_, _, _, _, err := setFroniusChargeBatteryMode(SunSpecFloat, true, nan, 5000, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, faultyPort)

		require.NoError(t, err)
		assert.Equal(t, uint16(0), srv.HoldingRegisters[40359-1])
		assert.Equal(t, uint16(10000), srv.HoldingRegisters[floatOutWRte-1])
	})
	t.Run("reset fails", func(t *testing.T) {
		srv := faultyInverter(t, nil, true)
		srv.HoldingRegisters[40359-1] = 2

		_, _, _, _, err := setFroniusChargeBatteryMode(SunSpecFloat, true, nan, 5000, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, faultyPort)

		assert.ErrorContains(t, err, "write register")
	})
}

func TestChargeModeHoldWriteFailure(t *testing.T) {
	faultyInverter(t, nil, true)

	_, decision, _, _, err := setFroniusChargeBatteryMode(SunSpecFloat, true, 20000, 5000, 11000, 9000, 3500, 0, "00:00", "23:59", modbus_ip, true, 0, 0, true, faultyPort)

	assert.Equal(t, DecisionIdle, decision)
	assert.ErrorContains(t, err, "write register")
}
