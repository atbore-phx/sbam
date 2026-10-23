package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const powerFlowURL = "/solar_api/v1/GetPowerFlowRealtimeData.fcgi"

// PowerFlow is the site power snapshot reported by the Fronius Solar API.
// Signs follow Fronius: GridW > 0 imports from the grid, BatteryW > 0
// discharges the battery and BatteryW < 0 charges it.
type PowerFlow struct {
	GridW    float64
	BatteryW float64
}

// BaselineImportW is the grid import the site would have with the battery
// idle: what every other load draws from the grid, whatever it is.
func (p PowerFlow) BaselineImportW() float64 {
	return p.GridW + p.BatteryW
}

type powerFlowResponse struct {
	Body struct {
		Data struct {
			Site struct {
				PGrid *float64 `json:"P_Grid"`
				PAkku *float64 `json:"P_Akku"`
			} `json:"Site"`
		} `json:"Data"`
	} `json:"Body"`
	Head struct {
		Status struct {
			Code   int    `json:"Code"`
			Reason string `json:"Reason"`
		} `json:"Status"`
	} `json:"Head"`
}

// ErrPowerFlowIncomplete is returned when the Solar API answers without a
// grid power value (no smart meter, or the meter is not reporting).
var ErrPowerFlowIncomplete = errors.New("power flow has no grid power value")

var powerFlowClient = &http.Client{Timeout: 5 * time.Second}

// ReadPowerFlow reads the current grid and battery power from the Fronius
// Solar API. A missing battery value is read as 0 W.
func ReadPowerFlow(froniusIP string) (PowerFlow, error) {
	resp, err := powerFlowClient.Get("http://" + froniusIP + powerFlowURL)
	if err != nil {
		return PowerFlow{}, fmt.Errorf("read power flow: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return PowerFlow{}, fmt.Errorf("read power flow: HTTP %d", resp.StatusCode)
	}

	var r powerFlowResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return PowerFlow{}, fmt.Errorf("decode power flow: %w", err)
	}
	if r.Head.Status.Code != 0 {
		return PowerFlow{}, fmt.Errorf("power flow status %d: %s", r.Head.Status.Code, r.Head.Status.Reason)
	}
	if r.Body.Data.Site.PGrid == nil {
		return PowerFlow{}, ErrPowerFlowIncomplete
	}
	p := PowerFlow{GridW: *r.Body.Data.Site.PGrid}
	if r.Body.Data.Site.PAkku != nil {
		p.BatteryW = *r.Body.Data.Site.PAkku
	}
	return p, nil
}
