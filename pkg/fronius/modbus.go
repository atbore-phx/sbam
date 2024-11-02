package fronius

import (
	u "sbam/src/utils"
	"time"

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

	return err

}

func ClosemodbusClient() error {
	modbusClient.Close()

	return nil
}
