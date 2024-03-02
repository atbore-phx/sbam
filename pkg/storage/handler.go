package storage

import (
	u "ha-fronius-bm/src/utils"
)

func New() *Storage {
	return &Storage{}
}

func (storage *Storage) Handler(fronius_ip string) (float64, error) {
	charge := 0.0

	batteries, err := GetStorage(fronius_ip)
	if err != nil {
		u.Log.Errorln("Error getting Storage Charge Data:", err)
		return charge, err
	}

	charge, err = GetCapacityStorage2Charge(batteries)
	if err != nil {
		u.Log.Errorln("Error getting Full Storage Capacity to Charge:", err)
		return charge, err
	}
	return charge, nil

}
