package storage_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"ha-fronius-bm/pkg/storage"
)

var mockServer *httptest.Server

func setup() {

	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
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
}`)
	}))

}

func teardown() {
	mockServer.Close()
}

func TestGetStorage(t *testing.T) {
	setup()
	ip := strings.TrimPrefix(mockServer.URL, "http://")
	batteries, err := storage.GetStorage(ip)
	if err != nil {
		t.Errorf("Error getting storage data: %s", err)
	}

	assert.Equal(t, 2, len(batteries.Body.Data))
	assert.Equal(t, 11059.0, batteries.Body.Data["0"].Controller.DesignedCapacity)
	assert.Equal(t, 13809.0, batteries.Body.Data["1"].Controller.DesignedCapacity)

	teardown()
}

func TestGetCapacityStorage2Charge(t *testing.T) {
	setup()

	ip := strings.TrimPrefix(mockServer.URL, "http://")
	batteries, err := storage.GetStorage(ip)
	if err != nil {
		t.Errorf("Error getting storage data: %s", err)
	}

	capacity, err := storage.GetCapacityStorage2Charge(batteries)
	if err != nil {
		t.Errorf("Error getting storage capacity: %s", err)
	}

	assert.Equal(t, 6133.32, capacity)

	teardown()
}

func TestHandler(t *testing.T) {
	setup()

	st := storage.New()
	ip := strings.TrimPrefix(mockServer.URL, "http://")
	charge, err := st.Handler(ip)
	if err != nil {
		t.Errorf("Error getting storage charge: %s", err)
	}

	assert.Equal(t, 6133.32, charge)

	teardown()
}

func TestHandlerError(t *testing.T) {
	setup()

	storage := storage.New()

	mockServer.Close() // Simulate an error by closing the mock server

	charge, err := storage.Handler(mockServer.URL)
	assert.Equal(t, float64(0), charge)
	assert.Error(t, err)

	teardown()
}

func TestHandlerError2(t *testing.T) {
	setup()

	st := storage.New()

	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == storage.Req_url {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		} else {
			http.Error(w, "Not Found", http.StatusNotFound)
		}
	}))

	charge, err := st.Handler(mockServer.URL)
	assert.Equal(t, float64(0), charge)
	assert.Error(t, err)

	teardown()
}

func TestGetCapacityStorage2ChargeError(t *testing.T) {
	setup()

	controller := storage.Controller{
		Enable: 0,
	}

	battery := storage.Battery{
		Controller: controller,
		Modules:    []interface{}{},
	}

	batteries := storage.Batteries{
		Body: struct {
			Data map[string]storage.Battery `json:"Data"`
		}{
			Data: map[string]storage.Battery{
				"0": battery,
			},
		},
	}

	capacity, err := storage.GetCapacityStorage2Charge(batteries)
	assert.Equal(t, float64(0), capacity)
	assert.Error(t, err)

	teardown()
}
