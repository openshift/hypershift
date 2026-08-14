package log

import (
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"go.uber.org/zap/zapcore"
)

var Log = zap.New(func(o *zap.Options) {
	o.TimeEncoder = zapcore.RFC3339TimeEncoder
})
