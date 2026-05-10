package utils

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		assert.NoError(t, HandleError(nil, "ignored"))
	})

	t.Run("returns same error", func(t *testing.T) {
		err := errors.New("boom")
		got := HandleError(err, "context")
		require.Error(t, got)
		assert.ErrorIs(t, got, err)
	})
}

func TestHandleErrorPanic(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		assert.NoError(t, HandleErrorPanic(nil, "ignored"))
	})

	t.Run("panics on error", func(t *testing.T) {
		err := errors.New("boom")
		assert.PanicsWithValue(t, err, func() {
			_ = HandleErrorPanic(err, "context")
		})
	})
}
