package storage

import "fmt"

func New() *Storage {
	return &Storage{}
}

func (storage *Storage) Handler(fronius_ip string) (float64, error) {
	charge := 0.0

	batteries, err := GetStorage(fronius_ip)
	if err != nil {
		fmt.Println("Error getting Storage Charge Data:", err)
		return charge, err
	}

	charge, err = GetCapacityStorage2Charge(batteries)
	if err != nil {
		fmt.Println("Error getting Full Storage Capacity to Charge:", err)
		return charge, err
	}
	return charge, nil

}
