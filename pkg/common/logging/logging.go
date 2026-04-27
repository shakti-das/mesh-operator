package logging

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/util/runtime"

	"k8s.io/klog/v2"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger returns a zap logger with its config set for Scone-style structured logs.
func NewLogger(level zapcore.Level) (*zap.Logger, error) {
	config := newConfig(level)

	logger, err := config.Build()

	// Wire in an additional error handler to make sure that these errors produce structured output
	runtime.ErrorHandlers = append(runtime.ErrorHandlers, func(ctx context.Context, err error, msg string, keysAndValues ...interface{}) {
		logger.Error(msg, zap.Error(err))
	})

	return logger, err
}

func InitKlog(logger *zap.Logger, klogLevel int) {
	if klogLevel < 0 {
		// disabled
		return
	}
	// Set up an adapter that will consume klog logging events and forward those to the zap logger.
	// This is needed to make sure that logs produced by kube libraries show up in our service logs
	// in a structured format.
	set := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	klog.InitFlags(set)

	_ = set.Parse([]string{
		fmt.Sprintf("--v=%d", klogLevel),
		"--logtostderr=true",
	})
	klog.SetLogger(NewKlogAdapter(logger))
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
