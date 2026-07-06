package storage

import (
	u "sbam/src/utils"
)

func New() *storage {
	return &storage{}
}

func (s *storage) Handler(fronius_ip string) (float64, float64, float64, error) {
	charge := 0.0
	charge_max := 0.0
	socPct := 0.0

	b, err := getStorage(fronius_ip)
	if err != nil {
		u.Log.Errorln("Error getting Storage Charge Data:", err)
		return charge, charge_max, socPct, err
	}

	charge, charge_max, socPct, err = getCapacityStorage2Charge(b)
	if err != nil {
		u.Log.Errorln("Error getting Full Storage Capacity to Charge:", err)
		return charge, charge_max, socPct, err
	}
	return charge, charge_max, socPct, nil

}
