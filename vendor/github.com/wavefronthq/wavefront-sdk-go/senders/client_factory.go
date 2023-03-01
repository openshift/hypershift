package senders

import (
	"fmt"
	"github.com/wavefronthq/wavefront-sdk-go/internal"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	defaultTracesPort  = 30001
	defaultMetricsPort = 2878
)

// Option Wavefront client configuration options
type Option func(*configuration)

// Configuration for the direct ingestion sender
type configuration struct {
	Server string // Wavefront URL of the form https://<INSTANCE>.wavefront.com
	Token  string // Wavefront API token with direct data ingestion permission

	// Optional configuration properties. Default values should suffice for most use cases.
	// override the defaults only if you wish to set higher values.

	MetricsPort int
	TracesPort  int

	// max batch of data sent per flush interval. defaults to 10,000. recommended not to exceed 40,000.
	BatchSize int

	// size of internal buffers beyond which received data is dropped.
	// helps with handling brief increases in data and buffering on errors.
	// separate buffers are maintained per data type (metrics, spans and distributions)
	// buffers are not pre-allocated to max size and vary based on actual usage.
	// defaults to 500,000. higher values could use more memory.
	MaxBufferSize int

	// interval (in seconds) at which to flush data to Wavefront. defaults to 1 Second.
	// together with batch size controls the max theoretical throughput of the sender.
	FlushIntervalSeconds int
	SDKMetricsTags       map[string]string
}

// NewSender creates Wavefront client
func NewSender(wfURL string, setters ...Option) (Sender, error) {
	cfg, err := CreateConfig(wfURL, setters...)
	if err != nil {
		return nil, fmt.Errorf("unable to create sender config: %s", err)
	}
	return newWavefrontClient(cfg)
}

func CreateConfig(wfURL string, setters ...Option) (*configuration, error) {
	cfg := &configuration{
		MetricsPort:          defaultMetricsPort,
		TracesPort:           defaultTracesPort,
		BatchSize:            defaultBatchSize,
		MaxBufferSize:        defaultBufferSize,
		FlushIntervalSeconds: defaultFlushInterval,
	}

	u, err := url.Parse(wfURL)
	if err != nil {
		return nil, err
	}

	if !strings.HasPrefix(strings.ToLower(u.Scheme), "http") {
		return nil, fmt.Errorf("invalid scheme '%s' in '%s', only 'http' is supported", u.Scheme, u)
	}

	if len(u.User.String()) > 0 {
		cfg.Token = u.User.String()
		u.User = nil
	}

	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil {
			return nil, fmt.Errorf("unable to convert port to integer: %s", err)
		}
		cfg.MetricsPort = port
		cfg.TracesPort = port
		u.Host = u.Hostname()
	}
	cfg.Server = u.String()

	for _, set := range setters {
		set(cfg)
	}
	return cfg, nil
}

// newWavefrontClient creates a Wavefront sender
func newWavefrontClient(cfg *configuration) (Sender, error) {
	metricsReporter := internal.NewReporter(fmt.Sprintf("%s:%d", cfg.Server, cfg.MetricsPort), cfg.Token)
	tracesReporter := internal.NewReporter(fmt.Sprintf("%s:%d", cfg.Server, cfg.TracesPort), cfg.Token)

	sender := &wavefrontSender{
		defaultSource: internal.GetHostname("wavefront_direct_sender"),
		proxy:         len(cfg.Token) == 0,
	}
	sender.initializeInternalMetrics(cfg)
	sender.pointHandler = newLineHandler(metricsReporter, cfg, internal.MetricFormat, "points", sender.internalRegistry)
	sender.histoHandler = newLineHandler(metricsReporter, cfg, internal.HistogramFormat, "histograms", sender.internalRegistry)
	sender.spanHandler = newLineHandler(tracesReporter, cfg, internal.TraceFormat, "spans", sender.internalRegistry)
	sender.spanLogHandler = newLineHandler(tracesReporter, cfg, internal.SpanLogsFormat, "span_logs", sender.internalRegistry)
	sender.eventHandler = newLineHandler(metricsReporter, cfg, internal.EventFormat, "events", sender.internalRegistry)

	sender.Start()
	return sender, nil
}

func (sender *wavefrontSender) initializeInternalMetrics(cfg *configuration) {

	var setters []internal.RegistryOption
	setters = append(setters, internal.SetPrefix("~sdk.go.core.sender.direct"))
	setters = append(setters, internal.SetTag("pid", strconv.Itoa(os.Getpid())))

	for key, value := range cfg.SDKMetricsTags {
		setters = append(setters, internal.SetTag(key, value))
	}

	sender.internalRegistry = internal.NewMetricRegistry(
		sender,
		setters...,
	)
	sender.pointsValid = sender.internalRegistry.NewDeltaCounter("points.valid")
	sender.pointsInvalid = sender.internalRegistry.NewDeltaCounter("points.invalid")
	sender.pointsDropped = sender.internalRegistry.NewDeltaCounter("points.dropped")

	sender.histogramsValid = sender.internalRegistry.NewDeltaCounter("histograms.valid")
	sender.histogramsInvalid = sender.internalRegistry.NewDeltaCounter("histograms.invalid")
	sender.histogramsDropped = sender.internalRegistry.NewDeltaCounter("histograms.dropped")

	sender.spansValid = sender.internalRegistry.NewDeltaCounter("spans.valid")
	sender.spansInvalid = sender.internalRegistry.NewDeltaCounter("spans.invalid")
	sender.spansDropped = sender.internalRegistry.NewDeltaCounter("spans.dropped")

	sender.spanLogsValid = sender.internalRegistry.NewDeltaCounter("span_logs.valid")
	sender.spanLogsInvalid = sender.internalRegistry.NewDeltaCounter("span_logs.invalid")
	sender.spanLogsDropped = sender.internalRegistry.NewDeltaCounter("span_logs.dropped")

	sender.eventsValid = sender.internalRegistry.NewDeltaCounter("events.valid")
	sender.eventsInvalid = sender.internalRegistry.NewDeltaCounter("events.invalid")
	sender.eventsDropped = sender.internalRegistry.NewDeltaCounter("events.dropped")
}

// BatchSize set max batch of data sent per flush interval. Defaults to 10,000. recommended not to exceed 40,000.
func BatchSize(n int) Option {
	return func(cfg *configuration) {
		cfg.BatchSize = n
	}
}

// MaxBufferSize set the size of internal buffers beyond which received data is dropped. Defaults to 50,000.
func MaxBufferSize(n int) Option {
	return func(cfg *configuration) {
		cfg.MaxBufferSize = n
	}
}

// FlushIntervalSeconds set the interval (in seconds) at which to flush data to Wavefront. Defaults to 1 Second.
func FlushIntervalSeconds(n int) Option {
	return func(cfg *configuration) {
		cfg.FlushIntervalSeconds = n
	}
}

// MetricsPort sets the port on which to report metrics. Default is 2878.
func MetricsPort(port int) Option {
	return func(cfg *configuration) {
		cfg.MetricsPort = port
	}
}

// TracesPort sets the port on which to report traces. Default is 30001.
func TracesPort(port int) Option {
	return func(cfg *configuration) {
		cfg.TracesPort = port
	}
}

// SDKMetricsTags sets internal SDK metrics.
func SDKMetricsTags(tags map[string]string) Option {
	return func(cfg *configuration) {
		if cfg.SDKMetricsTags != nil {
			for key, value := range tags {
				cfg.SDKMetricsTags[key] = value
			}
		} else {
			cfg.SDKMetricsTags = tags
		}

	}
}
