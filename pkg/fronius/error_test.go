package fronius

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock for the logger
type MockLogger struct {
	mock.Mock
}

func (m *MockLogger) Errorf(format string, args ...interface{}) {
	m.Called(format, args)
}

var uu = struct {
	Log *MockLogger
}{
	Log: &MockLogger{},
}

func TestHandleErrorPanic(t *testing.T) {
	// Set up the mock logger
	uu.Log = &MockLogger{}

	t.Run("should panic when error is not nil", func(t *testing.T) {
		err := errors.New("test error")
		uu.Log.On("Errorf", "test message %s", err).Return()

		// Use a deferred function to recover from panic
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("Expected panic but did not occur")
			}
		}()

		handleErrorPanic(err, "test message")
		uu.Log.AssertExpectations(t)
	})

	t.Run("should return nil when error is nil", func(t *testing.T) {
		result := handleErrorPanic(nil, "test message")
		assert.Nil(t, result)
	})
}
