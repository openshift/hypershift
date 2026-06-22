package util

import (
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/go-logr/logr"
	"go.uber.org/zap/zapcore"
)

// NewLogger creates a logr.Logger backed by zap with RFC3339 timestamps.
// This is the standard logger constructor for CLI commands.
func NewLogger() logr.Logger {
	return zap.New(func(o *zap.Options) {
		o.TimeEncoder = zapcore.RFC3339TimeEncoder
	})
}
