package senders

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wavefronthq/wavefront-sdk-go/event"
	"github.com/wavefronthq/wavefront-sdk-go/histogram"
	"github.com/wavefronthq/wavefront-sdk-go/internal"
	"github.com/wavefronthq/wavefront-sdk-go/version"
)

const (
	metricHandler int = iota
	histoHandler
	spanHandler
	eventHandler
	handlersCount
)

type proxySender struct {
	handlers         []internal.ConnectionHandler
	defaultSource    string
	internalRegistry *internal.MetricRegistry

	pointsValid     *internal.DeltaCounter
	pointsInvalid   *internal.DeltaCounter
	pointsDropped   *internal.DeltaCounter
	pointsDiscarded *internal.DeltaCounter

	histogramsValid     *internal.DeltaCounter
	histogramsInvalid   *internal.DeltaCounter
	histogramsDropped   *internal.DeltaCounter
	histogramsDiscarded *internal.DeltaCounter

	spansValid     *internal.DeltaCounter
	spansInvalid   *internal.DeltaCounter
	spansDropped   *internal.DeltaCounter
	spansDiscarded *internal.DeltaCounter

	spanLogsValid     *internal.DeltaCounter
	spanLogsInvalid   *internal.DeltaCounter
	spanLogsDropped   *internal.DeltaCounter
	spanLogsDiscarded *internal.DeltaCounter

	eventsValid     *internal.DeltaCounter
	eventsInvalid   *internal.DeltaCounter
	eventsDropped   *internal.DeltaCounter
	eventsDiscarded *internal.DeltaCounter
}

// Creates and returns a Wavefront Proxy Sender instance
// Deprecated: Use 'senders.NewSender(url)'
func NewProxySender(cfg *ProxyConfiguration) (Sender, error) {
	sender := &proxySender{
		defaultSource: internal.GetHostname("wavefront_proxy_sender"),
		handlers:      make([]internal.ConnectionHandler, handlersCount),
	}

	var setters []internal.RegistryOption
	setters = append(setters, internal.SetPrefix("~sdk.go.core.sender.proxy"))
	setters = append(setters, internal.SetTag("pid", strconv.Itoa(os.Getpid())))

	for key, value := range cfg.SDKMetricsTags {
		setters = append(setters, internal.SetTag(key, value))
	}

	sender.internalRegistry = internal.NewMetricRegistry(
		sender,
		setters...,
	)

	if sdkVersion, e := internal.GetSemVer(version.Version); e == nil {
		sender.internalRegistry.NewGaugeFloat64("version", func() float64 {
			return sdkVersion
		})
	}

	if cfg.FlushIntervalSeconds == 0 {
		cfg.FlushIntervalSeconds = defaultProxyFlushInterval
	}

	if cfg.MetricsPort != 0 {
		sender.handlers[metricHandler] = makeConnHandler(cfg.Host, cfg.MetricsPort, cfg.FlushIntervalSeconds, "points", sender.internalRegistry)
	}

	if cfg.DistributionPort != 0 {
		sender.handlers[histoHandler] = makeConnHandler(cfg.Host, cfg.DistributionPort, cfg.FlushIntervalSeconds, "histograms", sender.internalRegistry)
	}

	if cfg.TracingPort != 0 {
		sender.handlers[spanHandler] = makeConnHandler(cfg.Host, cfg.TracingPort, cfg.FlushIntervalSeconds, "spans", sender.internalRegistry)
	}

	if cfg.EventsPort != 0 {
		sender.handlers[eventHandler] = makeConnHandler(cfg.Host, cfg.EventsPort, cfg.FlushIntervalSeconds, "events", sender.internalRegistry)
	}

	sender.pointsValid = sender.internalRegistry.NewDeltaCounter("points.valid")
	sender.pointsInvalid = sender.internalRegistry.NewDeltaCounter("points.invalid")
	sender.pointsDropped = sender.internalRegistry.NewDeltaCounter("points.dropped")
	sender.pointsDiscarded = sender.internalRegistry.NewDeltaCounter("points.discarded")

	sender.histogramsValid = sender.internalRegistry.NewDeltaCounter("histograms.valid")
	sender.histogramsInvalid = sender.internalRegistry.NewDeltaCounter("histograms.invalid")
	sender.histogramsDropped = sender.internalRegistry.NewDeltaCounter("histograms.dropped")
	sender.histogramsDiscarded = sender.internalRegistry.NewDeltaCounter("histograms.discarded")

	sender.spansValid = sender.internalRegistry.NewDeltaCounter("spans.valid")
	sender.spansInvalid = sender.internalRegistry.NewDeltaCounter("spans.invalid")
	sender.spansDropped = sender.internalRegistry.NewDeltaCounter("spans.dropped")
	sender.spansDiscarded = sender.internalRegistry.NewDeltaCounter("spans.discarded")

	sender.spanLogsValid = sender.internalRegistry.NewDeltaCounter("span_logs.valid")
	sender.spanLogsInvalid = sender.internalRegistry.NewDeltaCounter("span_logs.invalid")
	sender.spanLogsDropped = sender.internalRegistry.NewDeltaCounter("span_logs.dropped")
	sender.spanLogsDiscarded = sender.internalRegistry.NewDeltaCounter("span_logs.discarded")

	sender.eventsValid = sender.internalRegistry.NewDeltaCounter("events.valid")
	sender.eventsInvalid = sender.internalRegistry.NewDeltaCounter("events.invalid")
	sender.eventsDropped = sender.internalRegistry.NewDeltaCounter("events.dropped")
	sender.eventsDiscarded = sender.internalRegistry.NewDeltaCounter("events.discarded")

	for _, h := range sender.handlers {
		if h != nil {
			sender.Start()
			return sender, nil
		}
	}

	return nil, errors.New("at least one proxy port should be enabled")
}

func makeConnHandler(host string, port, flushIntervalSeconds int, prefix string, internalRegistry *internal.MetricRegistry) internal.ConnectionHandler {
	addr := host + ":" + strconv.FormatInt(int64(port), 10)
	flushInterval := time.Second * time.Duration(flushIntervalSeconds)
	return internal.NewProxyConnectionHandler(addr, flushInterval, prefix, internalRegistry)
}

func (sender *proxySender) Start() {
	for _, h := range sender.handlers {
		if h != nil {
			h.Start()
		}
	}
	sender.internalRegistry.Start()
}

func (sender *proxySender) SendMetric(name string, value float64, ts int64, source string, tags map[string]string) error {
	handler := sender.handlers[metricHandler]
	if handler == nil {
		sender.pointsDiscarded.Inc()
		return errors.New("proxy metrics port not provided, cannot send metric data")
	}

	if !handler.Connected() {
		if err := handler.Connect(); err != nil {
			sender.pointsDiscarded.Inc()
			return err
		}
	}

	line, err := MetricLine(name, value, ts, source, tags, sender.defaultSource)
	if err != nil {
		sender.pointsInvalid.Inc()
		return err
	} else {
		sender.pointsValid.Inc()
	}
	err = handler.SendData(line)
	if err != nil {
		sender.pointsDropped.Inc()
	}
	return err
}

func (sender *proxySender) SendDeltaCounter(name string, value float64, source string, tags map[string]string) error {
	if name == "" {
		sender.pointsInvalid.Inc()
		return errors.New("empty metric name")
	}
	if !internal.HasDeltaPrefix(name) {
		name = internal.DeltaCounterName(name)
	}
	if value > 0 {
		return sender.SendMetric(name, value, 0, source, tags)
	}
	return nil
}

func (sender *proxySender) SendDistribution(name string, centroids []histogram.Centroid, hgs map[histogram.Granularity]bool, ts int64, source string, tags map[string]string) error {
	handler := sender.handlers[histoHandler]
	if handler == nil {
		sender.histogramsDiscarded.Inc()
		return errors.New("proxy distribution port not provided, cannot send distribution data")
	}

	if !handler.Connected() {
		if err := handler.Connect(); err != nil {
			sender.histogramsDiscarded.Inc()
			return err
		}
	}

	line, err := HistoLine(name, centroids, hgs, ts, source, tags, sender.defaultSource)
	if err != nil {
		sender.histogramsInvalid.Inc()
		return err
	} else {
		sender.histogramsValid.Inc()
	}
	err = handler.SendData(line)
	if err != nil {
		sender.histogramsDropped.Inc()
	}
	return err
}

func (sender *proxySender) SendSpan(name string, startMillis, durationMillis int64, source, traceId, spanId string, parents, followsFrom []string, tags []SpanTag, spanLogs []SpanLog) error {
	handler := sender.handlers[spanHandler]
	if handler == nil {
		sender.spansDiscarded.Inc()
		if spanLogs != nil {
			sender.spanLogsDiscarded.Inc()
		}
		return errors.New("proxy tracing port not provided, cannot send span data")
	}

	if !handler.Connected() {
		if err := handler.Connect(); err != nil {
			sender.spansDiscarded.Inc()
			if spanLogs != nil {
				sender.spanLogsDiscarded.Inc()
			}
			return err
		}
	}

	line, err := SpanLine(name, startMillis, durationMillis, source, traceId, spanId, parents, followsFrom, tags, spanLogs, sender.defaultSource)
	if err != nil {
		sender.spansInvalid.Inc()

		return err
	} else {
		sender.spansValid.Inc()
	}
	err = handler.SendData(line)
	if err != nil {
		sender.spansDropped.Inc()
		return err
	}

	if len(spanLogs) > 0 {
		logs, err := SpanLogJSON(traceId, spanId, spanLogs)
		if err != nil {
			sender.spanLogsInvalid.Inc()
			return err
		} else {
			sender.spanLogsValid.Inc()
		}
		err = handler.SendData(logs)
		if err != nil {
			sender.spanLogsDropped.Inc()
		}
		return err
	}
	return nil
}

func (sender *proxySender) SendEvent(name string, startMillis, endMillis int64, source string, tags map[string]string, setters ...event.Option) error {
	handler := sender.handlers[eventHandler]
	if handler == nil {
		sender.eventsDiscarded.Inc()
		return errors.New("proxy events port not provided, cannot send events data")
	}

	if !handler.Connected() {
		if err := handler.Connect(); err != nil {
			sender.eventsDiscarded.Inc()
			return err
		}
	}

	line, err := EventLine(name, startMillis, endMillis, source, tags, setters...)
	if err != nil {
		sender.eventsInvalid.Inc()
		return err
	} else {
		sender.eventsValid.Inc()
	}
	err = handler.SendData(line)
	if err != nil {
		sender.eventsDropped.Inc()
	}
	return err
}

func (sender *proxySender) Close() {
	for _, h := range sender.handlers {
		if h != nil {
			h.Close()
		}
	}
	sender.internalRegistry.Stop()
}

func (sender *proxySender) Flush() error {
	errStr := ""
	for _, h := range sender.handlers {
		if h != nil {
			err := h.Flush()
			if err != nil {
				errStr = errStr + err.Error() + "\n"
			}
		}
	}
	if errStr != "" {
		return errors.New(strings.Trim(errStr, "\n"))
	}
	return nil
}

func (sender *proxySender) GetFailureCount() int64 {
	var failures int64
	for _, h := range sender.handlers {
		if h != nil {
			failures += h.GetFailureCount()
		}
	}
	return failures
}
