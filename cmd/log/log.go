package log

import "github.com/openshift/hypershift/cmd/util"

var log = util.Log

func Error(err error, msg string, keysAndValues ...interface{}) {
	log.Error(err, msg, keysAndValues...)
}
