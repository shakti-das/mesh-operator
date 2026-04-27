package common

import (
	"go.uber.org/zap/zapcore"
)

type MopArgs struct {
	AllNamespaces bool
	DryRun        bool
	LogLevel      zapcore.Level
}
