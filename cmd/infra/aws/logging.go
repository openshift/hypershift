package aws

import (
	"github.com/bombsimon/logrusr"
	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
)

func setupLogger() logr.Logger {
	return logrusr.NewLogger(logrus.New())
}
