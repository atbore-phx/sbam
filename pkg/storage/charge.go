package storage

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

const (
	Req_url = "/solar_api/v1/GetStorageRealtimeData.cgi"
)

func GetStorage(fronius_ip string) (Batteries, error) {
	url := "http://" + fronius_ip + Req_url
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return Batteries{}, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Batteries{}, err
	}
	defer resp.Body.Close()

	var batteries Batteries
	err = json.NewDecoder(resp.Body).Decode(&batteries)
	if err != nil {
		return Batteries{}, err
	}

	return batteries, nil
}

func GetCapacityStorage2Charge(batteries Batteries) (float64, error) {
	capacity := 0.0
	status := 0.0
	disabled := true

	for _, b := range batteries.Body.Data {
		if b.Controller.Enable == 1 {
			status += b.Controller.DesignedCapacity * b.Controller.StateOfChargeRelative / 100
			capacity += b.Controller.DesignedCapacity
			disabled = false
		}
	}

	if disabled {
		err := errors.New("Battery Cluster is disabled")
		return capacity - status, err
	}

	return capacity - status, nil
}
