package logging

import (
	"strconv"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger returns a zap logger with its config set for Scone-style structured logs.
func NewLogger(level zapcore.Level) (*zap.Logger, error) {
	config := newConfig(level)

	return config.Build()
}

// newConfig returns a zap logger config for Scone-style structured logs.
func newConfig(level zapcore.Level) zap.Config {
	config := zap.NewProductionConfig()

	// Set key names to match what's used by Scone.
	config.EncoderConfig.MessageKey = "message"
	config.EncoderConfig.TimeKey = "timestamp"

	config.EncoderConfig.EncodeTime = sconeTimeEncoder

	config.Level = zap.NewAtomicLevelAt(level)

	return config
}

func sconeTimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	nanos := t.UnixNano()
	millis := nanos / int64(time.Millisecond)
	enc.AppendString(strconv.FormatInt(millis, 10))
}
