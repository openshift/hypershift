package log

import (
	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var Log = zap.New(func(o *zap.Options) {
	o.TimeEncoder = zapcore.RFC3339TimeEncoder
})
