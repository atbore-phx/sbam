package main

import (
	"os"
	"sbam/pkg/cmd"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMain(t *testing.T) {
	err := cmd.SetVersionInfo("1.0", "abc123", "2022-01-01")
	assert.NoError(t, err)

	old := os.Args
	defer func() { os.Args = old }()

	os.Args = []string{"cmd", "--version"}

	err = cmd.Execute()
	assert.NoError(t, err)
}
