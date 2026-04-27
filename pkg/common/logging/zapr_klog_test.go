package logging

import (
	"fmt"
	"testing"

	"go.uber.org/zap/zaptest"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zapcore"
)

func TestToZapLevel(t *testing.T) {
	assert.Equal(t, zapcore.WarnLevel, verbosityToZapLevel(0))
	assert.Equal(t, zapcore.WarnLevel, verbosityToZapLevel(1))
	assert.Equal(t, zapcore.InfoLevel, verbosityToZapLevel(3))
	assert.Equal(t, zapcore.DebugLevel, verbosityToZapLevel(5))
	assert.Equal(t, zapcore.DebugLevel, verbosityToZapLevel(7))
	assert.Equal(t, zapcore.InvalidLevel, verbosityToZapLevel(10))
}

func TestZapKlogAdapter_Enabled(t *testing.T) {
	logger := zaptest.NewLogger(t)
	adapter := zapKlogAdapter{l: logger}

	// Assumption is that Error logging is enabled by default
	assert.True(t, adapter.Enabled(1))
}

func TestZapKlogAdapter_Info(t *testing.T) {
	logger := zaptest.NewLogger(t)
	adapter := zapKlogAdapter{l: logger}

	adapter.Info(1, "test message")
}

func TestNewKlogAdapter_Error(t *testing.T) {
	logger := zaptest.NewLogger(t)
	adapter := zapKlogAdapter{l: logger}

	adapter.Error(fmt.Errorf("test-error"), "test message")
}

func TestNewKlogAdapter_WithValues(t *testing.T) {
	logger := zaptest.NewLogger(t)
	adapter := zapKlogAdapter{l: logger}

	otherAdapter := adapter.WithValues("some-key", "some-value")

	assert.Same(t, &adapter, otherAdapter)
}

func TestNewKlogAdapter_WithName(t *testing.T) {
	logger := zaptest.NewLogger(t)
	adapter := zapKlogAdapter{l: logger}

	otherAdapter := adapter.WithName("other-name")

	assert.NotEqual(t, adapter, otherAdapter)
}
