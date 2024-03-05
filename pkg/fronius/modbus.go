package fronius

import (
	u "sbam/src/utils"
	"time"

	"github.com/simonvetter/modbus"
)

var modbusClient *modbus.ModbusClient
var err error

func OpenModbusClient(url string, port ...string) error {
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	url = "tcp://" + url + ":" + p
	modbusClient, err = modbus.NewClient(&modbus.ClientConfiguration{
		URL:     url,
		Timeout: 1 * time.Second,
	})
	if err != nil {
		u.Log.Error("Someting goes wrong configuring Modbus Client")
		return err
	}
	err = modbusClient.Open()
	if err != nil {
		u.Log.Error("Someting goes wrong opening Modbus Client")
		return err
	}
	err = modbusClient.SetUnitId(1)
	if err != nil {
		u.Log.Error("Someting goes wrong setting Modbus Client SlaveID")
		return err
	}

	return nil

}

func ClosemodbusClient() error {
	err = modbusClient.Close()
	if err != nil {
		u.Log.Error("Someting goes wrong closing Modbus Client")
		return err
	}

	return nil
}
