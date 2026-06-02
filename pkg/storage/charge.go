package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	u "sbam/src/utils"
	"time"
)

const (
	ReqURL = "/solar_api/v1/GetStorageRealtimeData.cgi"
)

func getStorage(fronius_ip string) (batteries, error) {
	url := "http://" + fronius_ip + ReqURL
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		u.Log.Errorf("Something goes wrong creating the http request: %s", err)
		return batteries{}, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		u.Log.Errorf("Something goes wrong opening http connection: %s", err)
		return batteries{}, err
	}
	defer resp.Body.Close()

	var b batteries
	err = json.NewDecoder(resp.Body).Decode(&b)
	if err != nil {
		u.Log.Errorf("Something goes wrong retriving json: %s", err)
		return batteries{}, err
	}

	return b, nil
}

func getCapacityStorage2Charge(b batteries) (float64, float64, float64, error) {
	capacity := 0.0
	status := 0.0
	disabled := true

	for _, battery := range b.Body.Data {
		if battery.Controller.Enable == 1 {
			status += battery.Controller.DesignedCapacity * battery.Controller.StateOfChargeRelative / 100
			capacity += battery.Controller.DesignedCapacity
			disabled = false
		}
	}

	// Compute SoC (%) as (current stored energy / capacity) * 100
	var socPct float64
	if capacity > 0 {
		socPct = (status * 100.0) / capacity
	} else {
		u.HandleError(fmt.Errorf("invalid battery max capacity: %f", capacity), "cannot compute SoC setting to 0%")
		socPct = 0.0
	}

	if disabled {
		err := errors.New("Battery Cluster is disabled")
		return capacity - status, capacity, socPct, err
	}
	tc := capacity - status
	u.Log.Infof("Battery Capacity to charge: %d Wh", int(tc))
	u.Log.Infof("Battery Capacity Max: %d Wh", int(capacity))
	u.Log.Infof("Battery SoC: %.2f%%", socPct)
	return tc, capacity, socPct, nil
}
