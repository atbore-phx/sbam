package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMain(t *testing.T) {

	os.Args = []string{"cmd", "--version"}
	err := run("1.0", "abc123", "2022-01-01")

	assert.NoError(t, err)
}
