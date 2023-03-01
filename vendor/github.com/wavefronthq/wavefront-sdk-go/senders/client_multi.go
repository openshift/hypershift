package senders

import (
	"fmt"
	"strings"

	"github.com/wavefronthq/wavefront-sdk-go/event"
	"github.com/wavefronthq/wavefront-sdk-go/histogram"
)

// MultiSender Interface for sending metrics, distributions and spans to multiple Wavefront services at the same time
type MultiSender interface {
	Sender
}

type multiSender struct {
	senders []Sender
}

type multiError struct {
	errors []error
}

func (m *multiError) Error() string {
	switch len(m.errors) {
	case 0:
		return "no errors"
	case 1:
		return m.errors[0].Error()
	default:
		var errors []string
		for _, err := range m.errors {
			errors = append(errors, err.Error())
		}
		return fmt.Sprintf("%d errors: %s", len(m.errors), strings.Join(errors, ","))
	}
}

func (m *multiError) add(es ...error) {
	m.errors = append(m.errors, es...)
}

func (m *multiError) get() error {
	if len(m.errors) > 0 {
		return m
	}
	return nil
}

// NewMultiSender creates a new Wavefront MultiClient
func NewMultiSender(senders ...Sender) MultiSender {
	ms := &multiSender{}
	ms.senders = append(ms.senders, senders...)
	return ms
}

func (ms *multiSender) SendMetric(name string, value float64, ts int64, source string, tags map[string]string) error {
	var errors multiError
	for _, sender := range ms.senders {
		err := sender.SendMetric(name, value, ts, source, tags)
		if err != nil {
			errors.add(err)
		}
	}
	return errors.get()
}

func (ms *multiSender) SendDeltaCounter(name string, value float64, source string, tags map[string]string) error {
	var errors multiError
	for _, sender := range ms.senders {
		err := sender.SendDeltaCounter(name, value, source, tags)
		if err != nil {
			errors.add(err)
		}
	}
	return errors.get()
}

func (ms *multiSender) SendDistribution(name string, centroids []histogram.Centroid, hgs map[histogram.Granularity]bool, ts int64, source string, tags map[string]string) error {
	var errors multiError
	for _, sender := range ms.senders {
		err := sender.SendDistribution(name, centroids, hgs, ts, source, tags)
		if err != nil {
			errors.add(err)
		}
	}
	return errors.get()
}

func (ms *multiSender) SendSpan(name string, startMillis, durationMillis int64, source, traceId, spanId string, parents, followsFrom []string, tags []SpanTag, spanLogs []SpanLog) error {
	var errors multiError
	for _, sender := range ms.senders {
		err := sender.SendSpan(name, startMillis, durationMillis, source, traceId, spanId, parents, followsFrom, tags, spanLogs)
		if err != nil {
			errors.add(err)
		}
	}
	return errors.get()
}

func (ms *multiSender) SendEvent(name string, startMillis, endMillis int64, source string, tags map[string]string, setters ...event.Option) error {
	var errors multiError
	for _, sender := range ms.senders {
		err := sender.SendEvent(name, startMillis, endMillis, source, tags, setters...)
		if err != nil {
			errors.add(err)
		}
	}
	return errors.get()
}

func (ms *multiSender) Flush() error {
	var errors multiError
	for _, sender := range ms.senders {
		err := sender.Flush()
		if err != nil {
			errors.add(err)
		}
	}
	return errors.get()
}

func (ms *multiSender) GetFailureCount() int64 {
	var fc int64
	for _, sender := range ms.senders {
		fc += sender.GetFailureCount()
	}
	return fc
}

func (ms *multiSender) Start() {
	for _, sender := range ms.senders {
		sender.Start()
	}
}

func (ms *multiSender) Close() {
	for _, sender := range ms.senders {
		sender.Close()
	}
}
