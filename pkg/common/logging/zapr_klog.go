package logging

import (
	"github.com/go-logr/logr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type zapKlogAdapter struct {
	l *zap.Logger
}

// NewKlogAdapter creates an adapter that's capable of translating klog logging calls into zap logging calls.
// It's very simplistic and not to be considered a universal implementation of such adapter.
func NewKlogAdapter(zapLogger *zap.Logger) logr.Logger {
	return logr.New(&zapKlogAdapter{l: zapLogger})
}

// verbosityToZapLevel approximates a klog verbosity level to a zap level.
// It's only used when logging non-error logs, so only warn/info/debug levels are considered.
func verbosityToZapLevel(level int) zapcore.Level {
	if level <= 2 {
		return zapcore.WarnLevel
	} else if level <= 3 {
		return zapcore.InfoLevel
	} else if level <= 8 {
		return zapcore.DebugLevel
	}

	// We don't want this to be logged.
	return zapcore.InvalidLevel
}

// Init receives optional information about the logr library for LogSink
// implementations that need it.
func (zl *zapKlogAdapter) Init(ri logr.RuntimeInfo) {
	zl.l = zl.l.WithOptions(zap.AddCallerSkip(ri.CallDepth))
}

// Enabled tests whether this LogSink is enabled at the specified V-level.
func (zl *zapKlogAdapter) Enabled(level int) bool {
	return zl.l.Core().Enabled(verbosityToZapLevel(level))
}

func (zl *zapKlogAdapter) Info(level int, msg string, _ ...any) {
	if checkedEntry := zl.l.Check(verbosityToZapLevel(level), msg); checkedEntry != nil {
		checkedEntry.Write(zap.Any("test", "log"))
	}
}

// Error logs an error, with the given message and key/value pairs as
// context.  See Logger.Error for more details.
func (zl *zapKlogAdapter) Error(err error, msg string, keysAndValues ...any) {
	checkedEntry := zl.l.Check(zap.ErrorLevel, msg)
	checkedEntry.Write(zap.NamedError("error", err))
}

// WithValues returns a new LogSink with additional key/value pairs.
func (zl *zapKlogAdapter) WithValues(keysAndValues ...any) logr.LogSink {
	return zl
}

// WithName returns a new LogSink with the specified name appended.
func (zl *zapKlogAdapter) WithName(name string) logr.LogSink {
	newLogger := *zl
	newLogger.l = zl.l.Named(name)
	return &newLogger
}
