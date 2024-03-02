package main

import (
	"ha-fronius-bm/pkg/cmd"
)

var (
	version = "dev"
	commit  = "HEAD"
	date    = "today"
)

func main() {
	cmd.SetVersionInfo(version, commit, date)
	cmd.Execute()

}
