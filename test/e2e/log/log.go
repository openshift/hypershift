package log

import (
	"github.com/bombsimon/logrusr"
	"github.com/sirupsen/logrus"
)

var Logger = logrusr.NewLogger(logrus.New())
