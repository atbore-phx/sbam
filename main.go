package main

import (
	"os"
	"sbam/pkg/cmd"
	u "sbam/src/utils"
)

var (
	version = "dev"
	commit  = "HEAD"
	date    = "today"
)

func main() {
	if err := run(version, commit, date); err != nil {
		u.Log.Errorf("%s", err)
		os.Exit(1)
	}

}

func run(version string, commit string, date string) error {
	err := cmd.SetVersionInfo(version, commit, date)
	if err != nil {
		u.Log.Errorf("Error setting version: %s", err)
		return err
	}

	err = cmd.Execute()
	if err != nil {
		u.Log.Errorf("Error during the execution: %s", err)
		return err
	}

	return nil
}
