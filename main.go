package main

import (
	"sbam/pkg/cmd"
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
