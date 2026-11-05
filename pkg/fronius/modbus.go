package fronius

import (
	"context"
	"time"

	u "sbam/src/utils"

	"github.com/simonvetter/modbus"
)

var modbusClient *modbus.ModbusClient
var modbusErr error

// modbusTimeout bounds each Modbus operation, including the TCP connect.
const modbusTimeout = 1 * time.Second

func OpenModbusClient(proto string, url string, port ...string) error {
	return openModbusClient(context.Background(), proto, url, port...)
}

// openModbusClient opens the client unless ctx is done. The Modbus library
// takes no context, so a deadline closer than modbusTimeout shortens the
// timeout of every operation on this connection, and helpers check ctx between
// operations: after a cancel, a call returns within one modbusTimeout.
func openModbusClient(ctx context.Context, proto string, url string, port ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timeout := modbusTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		timeout = min(timeout, remaining)
	}

	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	mb_url := proto + "://" + url + ":" + p
	modbusClient, modbusErr = modbus.NewClient(&modbus.ClientConfiguration{
		URL:     mb_url,
		Timeout: timeout,
	})
	if u.HandleError(modbusErr, "Someting goes wrong configuring Modbus Client") != nil {
		return modbusErr
	}

	modbusErr = modbusClient.Open()
	if u.HandleError(modbusErr, "Someting goes wrong opening Modbus Client") != nil {
		return modbusErr
	}

	modbusErr = modbusClient.SetUnitId(1)
	if modbusErr != nil {
		u.Log.Errorf("Something goes wrong setting Modbus Client SlaveID: %v", modbusErr)
		return modbusErr
	}

	return nil

}

func ClosemodbusClient() error {
	modbusErr = modbusClient.Close()

	return u.HandleError(modbusErr, "Someting goes wrong closing Modbus Client")
}
