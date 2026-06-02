package storage

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

var mockServer *httptest.Server

var resp string = `{
	"Body" : {
	   "Data" : {
		  "0" : {
			 "Controller" : {
				"DesignedCapacity" : 11059.0,
				"Enable" : 1,
				"StateOfCharge_Relative" : 82.0
			 }
			},
			 "1" : {
				"Controller" : {
				   "DesignedCapacity" : 13809.0,
				   "Enable" : 1,
				   "StateOfCharge_Relative" : 70.0
			 }
		  }
	   }
	}
}`

var respBD string = `{
	"Body" : {
	   "Data" : {
		  "0" : {
			 "Controller" : {
				"DesignedCapacity" : 11059.0,
				"Enable" : 0,
				"StateOfCharge_Relative" : 82.0
			 }
			},
			 "1" : {
				"Controller" : {
				   "DesignedCapacity" : 13809.0,
				   "Enable" : 0,
				   "StateOfCharge_Relative" : 70.0
			 }
		  }
	   }
	}
}`

var respJsonErr string = `{
	"Body" : {
	   "Data" : {
		  3
}`

func setup(response string) {

	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	}))

}

func teardown() {
	mockServer.Close()
}

func TestGetStorage(t *testing.T) {
	setup(resp)
	ip := strings.TrimPrefix(mockServer.URL, "http://")
	batteries, err := getStorage(ip)
	if err != nil {
		t.Errorf("Error getting storage data: %s", err)
	}

	assert.Equal(t, 2, len(batteries.Body.Data))
	assert.Equal(t, 11059.0, batteries.Body.Data["0"].Controller.DesignedCapacity)
	assert.Equal(t, 13809.0, batteries.Body.Data["1"].Controller.DesignedCapacity)

	teardown()
}

func TestGetStorageError1(t *testing.T) {
	setup(respJsonErr)
	ip := strings.TrimPrefix(mockServer.URL, "http://")
	_, err := getStorage(ip)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid character '3'")

	teardown()
}

func TestGetStorageError2(t *testing.T) {
	ip := "|"
	_, err := getStorage(ip)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "|")
}

func TestGetCapacityStorage2Charge(t *testing.T) {
	setup(resp)

	ip := strings.TrimPrefix(mockServer.URL, "http://")
	batteries, err := getStorage(ip)
	if err != nil {
		t.Errorf("Error getting storage data: %s", err)
	}

	capacity, _, _, err := getCapacityStorage2Charge(batteries)
	if err != nil {
		t.Errorf("Error getting storage capacity: %s", err)
	}

	assert.Equal(t, 6133.32, capacity)

	teardown()
}

func TestGetCapacityStorageMax(t *testing.T) {
	setup(resp)

	ip := strings.TrimPrefix(mockServer.URL, "http://")
	batteries, err := getStorage(ip)
	if err != nil {
		t.Errorf("Error getting storage data: %s", err)
	}

	_, capacity_max, _, err := getCapacityStorage2Charge(batteries)
	if err != nil {
		t.Errorf("Error getting storage capacity: %s", err)
	}

	assert.Equal(t, 24868.0, capacity_max)

	teardown()
}

func TestGetCapacityStorage2ChargeError(t *testing.T) {
	setup(resp)

	ctrl := controller{
		Enable: 0,
	}

	bat := battery{
		Controller: ctrl,
		Modules:    []interface{}{},
	}

	bats := batteries{
		Body: struct {
			Data map[string]battery `json:"Data"`
		}{
			Data: map[string]battery{
				"0": bat,
			},
		},
	}

	capacity, capacity_max, socPct, err := getCapacityStorage2Charge(bats)
	assert.Equal(t, float64(0), capacity)
	assert.Equal(t, float64(0), capacity_max)
	assert.Equal(t, float64(0), socPct)
	assert.Error(t, err)

	teardown()
}

func TestHandler(t *testing.T) {
	setup(resp)

	st := New()
	ip := strings.TrimPrefix(mockServer.URL, "http://")
	charge, charge_max, socPct, err := st.Handler(ip)
	if err != nil {
		t.Errorf("Error getting storage charge: %s", err)
	}
	assert.Equal(t, 6133.32, charge)
	assert.Equal(t, 24868.0, charge_max)
	assert.InDelta(t, 75.34, socPct, 0.01)
	assert.NoError(t, err)

	teardown()
}

func TestHandlerError(t *testing.T) {
	setup(resp)

	st := New()

	mockServer.Close() // Simulate an error by closing the mock server

	charge, charge_max, socPct, err := st.Handler(mockServer.URL)
	assert.Equal(t, float64(0), charge)
	assert.Equal(t, float64(0), charge_max)
	assert.Equal(t, float64(0), socPct)
	assert.Error(t, err)

	teardown()
}

func TestHandlerError2(t *testing.T) {
	setup(resp)

	st := New()

	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == ReqURL {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		} else {
			http.Error(w, "Not Found", http.StatusNotFound)
		}
	}))

	charge, charge_max, socPct, err := st.Handler(mockServer.URL)
	assert.Equal(t, float64(0), charge)
	assert.Equal(t, float64(0), charge_max)
	assert.Equal(t, float64(0), socPct)
	assert.Error(t, err)

	teardown()
}

func TestHandlerError3(t *testing.T) {
	setup(respBD)

	st := New()
	ip := strings.TrimPrefix(mockServer.URL, "http://")

	charge, charge_max, socPct, err := st.Handler(ip)
	assert.Equal(t, float64(0), charge)
	assert.Equal(t, float64(0), charge_max)
	assert.Equal(t, float64(0), socPct)
	assert.Error(t, err)

	teardown()
}
