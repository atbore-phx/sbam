package main

import (
	"fmt"
	"os"

	"ha-fronius-bm/pkg/forecast"
)

func main() {
	// init global vars
	url := os.Getenv("URL")
	apiKey := os.Getenv("API_KEY")

	pwr := forecast.New()
	solarPowerProduction, err := pwr.Handler(apiKey, url)
	if err != nil {
		panic(err)
	}

	fmt.Print(solarPowerProduction)

}
