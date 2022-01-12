package log

import (
	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var log = zap.New(zap.UseDevMode(true), func(o *zap.Options) {
	o.TimeEncoder = zapcore.RFC3339TimeEncoder
})

func Error(err error, msg string, keysAndValues ...interface{}) {
	log.Error(err, msg, keysAndValues...)
}

func Info(msg string, keysAndValues ...interface{}) {
	log.Info(msg, keysAndValues...)
}
