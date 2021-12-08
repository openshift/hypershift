package main

import (
	"crypto/tls"
	"flag"
	"net/http"
	"net/url"
	"time"

	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type options struct {
	target string
}

func main() {
	opts := options{}
	flag.StringVar(&opts.target, "target", "", "A http url to probe. The program will continue until it gets a http 2XX back.")
	flag.Parse()

	log := zap.New(zap.UseDevMode(true), zap.JSONEncoder(), func(o *zap.Options) {
		o.TimeEncoder = zapcore.RFC3339TimeEncoder
	})

	url, err := url.Parse(opts.target)
	if err != nil {
		logrus.WithError(err).Fatalf("failed to parse %q as url", opts.target)
	}

	check(log, url, time.Second, time.Second)
}

func check(log logr.Logger, target *url.URL, requestTimeout time.Duration, sleepTime time.Duration) {
	log = log.WithValues("sleepTime", sleepTime.String())
	client := &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	for ; ; time.Sleep(sleepTime) {
		response, err := client.Get(target.String())
		if err != nil {
			log.Error(err, "Request failed, retrying...")
			continue
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode > 299 {
			log.WithValues("statuscode", response.StatusCode).Info("Request didn't return a 2XX status code, retrying...")
			continue
		}

		log.Info("Success", "statuscode", response.StatusCode)
		return
	}
}
