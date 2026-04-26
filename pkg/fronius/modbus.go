package fronius

import (
	"time"

	u "sbam/src/utils"

	"github.com/simonvetter/modbus"
)

var modbusClient *modbus.ModbusClient
var err error

func OpenModbusClient(proto string, url string, port ...string) error {
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	mb_url := proto + "://" + url + ":" + p
	modbusClient, err = modbus.NewClient(&modbus.ClientConfiguration{
		URL:     mb_url,
		Timeout: 1 * time.Second,
	})
	if u.HandleError(err, "Someting goes wrong configuring Modbus Client") != nil {
		return err
	}

	err = modbusClient.Open()
	if u.HandleError(err, "Someting goes wrong opening Modbus Client") != nil {
		return err
	}

	err = modbusClient.SetUnitId(1)
	u.HandleErrorPanic(err, "Someting goes wrong setting Modbus Client SlaveID")

	return nil

}

func ClosemodbusClient() error {
	err = modbusClient.Close()

	return u.HandleError(err, "Someting goes wrong closing Modbus Client")
}
