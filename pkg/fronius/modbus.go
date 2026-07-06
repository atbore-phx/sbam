package fronius

import (
	"time"

	u "sbam/src/utils"

	"github.com/simonvetter/modbus"
)

var modbusClient *modbus.ModbusClient
var modbusErr error

func OpenModbusClient(proto string, url string, port ...string) error {
	p := "502"
	if len(port) > 0 {
		p = port[0]
	}
	mb_url := proto + "://" + url + ":" + p
	modbusClient, modbusErr = modbus.NewClient(&modbus.ClientConfiguration{
		URL:     mb_url,
		Timeout: 1 * time.Second,
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
