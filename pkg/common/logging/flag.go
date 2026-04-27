package logging

import (
	"flag"

	"go.uber.org/zap/zapcore"
)

// NewFlag returns a golang core flag for setting the log level. We need this because the zapcore.Level type
// only implements the methods needed to convert to a core flag rather than a pflag (which is what cobra uses).
// The expectation is that the output of this will be used with pflag's AddGoFlag method.
func NewFlag(value *zapcore.Level) *flag.Flag {
	return &flag.Flag{
		Name:     "log-level",
		Usage:    "configure the logger verbosity level",
		Value:    value,
		DefValue: value.String(),
	}
}
