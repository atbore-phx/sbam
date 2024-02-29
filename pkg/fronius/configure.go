package fronius

import (
	"fmt"
	"time"

	"github.com/simonvetter/modbus"
)

// defaults
var modbusStorageCfg = map[uint16]int16{
	40349: 0,     // StorCtl_Mod, no limits
	40356: 10000, // OutWRte, 100% w 2 sf
	40357: 10000, // InWRte, 100% w 2 sf
	40351: 0,     // MinRsvPct, 0% w 2 sf
	40361: 1,     // ChaGriSet, Grid enabled
}

func Setdefaults(modbus_ip string, modbus_port string) error {
	url := "tcp://" + modbus_ip + ":" + modbus_port
	var modbusClient *modbus.ModbusClient
	var err error

	// for a TCP endpoint
	// (see examples/tls_client.go for TLS usage and options)
	modbusClient, err = modbus.NewClient(&modbus.ClientConfiguration{
		URL:     url,
		Timeout: 1 * time.Second,
		Logger:  nil,
	})
	if err != nil {
		panic(err)
	}
	err = modbusClient.Open()
	if err != nil {
		panic(err)
	}
	err = modbusClient.SetUnitId(1)
	if err != nil {
		panic(err)
	}

	for r, v := range modbusStorageCfg {
		err = modbusClient.WriteRegister(r-1, uint16(v))
		if err != nil {
			fmt.Printf("Something goes wrong writing the register: %d, value: %d\n", r, v)
			panic(err)
		}
		value, err := modbusClient.ReadRegister(r-1, modbus.HOLDING_REGISTER)
		fmt.Printf("register: %d ; value: %v\n", r, value)
		if err != nil {
			fmt.Printf("Something goes wrong reading the register: %d, value: %d\n", r, v)
			panic(err)
		}
	}
	modbusClient.Close()
	return nil
}
