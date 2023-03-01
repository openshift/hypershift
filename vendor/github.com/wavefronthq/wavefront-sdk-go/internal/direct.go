package internal

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

var (
	client    = &http.Client{Timeout: time.Second * 10}
	errReport = errors.New("error: invalid Format or points")
)

// The implementation of a Reporter that reports points directly to a Wavefront server.
type directReporter struct {
	serverURL string
	token     string
}

// NewDirectReporter create a metrics Reporter
func NewDirectReporter(server string, token string) Reporter {
	return &directReporter{serverURL: server, token: token}
}

func (reporter directReporter) Report(format string, pointLines string) (*http.Response, error) {
	if format == "" || pointLines == "" {
		return nil, formatError
	}

	// compress
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte(pointLines))
	if err != nil {
		zw.Close()
		return nil, err
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}

	apiURL := reporter.serverURL + reportEndpoint
	req, err := http.NewRequest("POST", apiURL, &buf)
	if err != nil {
		return &http.Response{}, err
	}

	req.Header.Set(contentType, octetStream)
	req.Header.Set(contentEncoding, gzipFormat)
	req.Header.Set(authzHeader, bearer+reporter.token)

	q := req.URL.Query()
	q.Add(formatKey, format)
	req.URL.RawQuery = q.Encode()

	return execute(req)
}

func (reporter directReporter) ReportEvent(event string) (*http.Response, error) {
	if event == "" {
		return nil, errReport
	}

	apiURL := reporter.serverURL + eventEndpoint
	req, err := http.NewRequest("POST", apiURL, strings.NewReader(event))
	if err != nil {
		return &http.Response{}, err
	}

	req.Header.Set(contentType, applicationJSON)
	req.Header.Set(contentEncoding, gzipFormat)
	req.Header.Set(authzHeader, bearer+reporter.token)

	return execute(req)
}

func execute(req *http.Request) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		return resp, err
	}
	io.Copy(ioutil.Discard, resp.Body)
	defer resp.Body.Close()
	return resp, nil
}
