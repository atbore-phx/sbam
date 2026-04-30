package fronius

import (
	"errors"
	"testing"

	u "sbam/src/utils"

	"github.com/stretchr/testify/assert"
)

func TestHandleErrorPanic(t *testing.T) {
	t.Run("should panic when error is not nil", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("Expected panic but did not occur")
			}
		}()

		u.HandleErrorPanic(errors.New("test error"), "test message")
	})

	t.Run("should return nil when error is nil", func(t *testing.T) {
		result := u.HandleErrorPanic(nil, "test message")
		assert.Nil(t, result)
	})
}
