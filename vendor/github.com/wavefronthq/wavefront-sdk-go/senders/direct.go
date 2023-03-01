package senders

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/wavefronthq/wavefront-sdk-go/event"
	"github.com/wavefronthq/wavefront-sdk-go/histogram"
	"github.com/wavefronthq/wavefront-sdk-go/internal"
	"github.com/wavefronthq/wavefront-sdk-go/version"
)

type directSender struct {
	reporter         internal.Reporter
	defaultSource    string
	pointHandler     *internal.LineHandler
	histoHandler     *internal.LineHandler
	spanHandler      *internal.LineHandler
	spanLogHandler   *internal.LineHandler
	eventHandler     *internal.LineHandler
	internalRegistry *internal.MetricRegistry

	pointsValid   *internal.DeltaCounter
	pointsInvalid *internal.DeltaCounter
	pointsDropped *internal.DeltaCounter

	histogramsValid   *internal.DeltaCounter
	histogramsInvalid *internal.DeltaCounter
	histogramsDropped *internal.DeltaCounter

	spansValid   *internal.DeltaCounter
	spansInvalid *internal.DeltaCounter
	spansDropped *internal.DeltaCounter

	spanLogsValid   *internal.DeltaCounter
	spanLogsInvalid *internal.DeltaCounter
	spanLogsDropped *internal.DeltaCounter

	eventsValid     *internal.DeltaCounter
	eventsInvalid   *internal.DeltaCounter
	eventsDropped   *internal.DeltaCounter
	eventsDiscarded *internal.DeltaCounter
}

// NewDirectSender creates and returns a Wavefront Direct Ingestion Sender instance
// Deprecated: Use 'senders.NewSender(url)'
func NewDirectSender(cfg *DirectConfiguration) (Sender, error) {
	if cfg.Server == "" || cfg.Token == "" {
		return nil, fmt.Errorf("server and token cannot be empty")
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = defaultBatchSize
	}
	if cfg.MaxBufferSize == 0 {
		cfg.MaxBufferSize = defaultBufferSize
	}
	if cfg.FlushIntervalSeconds == 0 {
		cfg.FlushIntervalSeconds = defaultFlushInterval
	}

	reporter := internal.NewDirectReporter(cfg.Server, cfg.Token)

	sender := &directSender{
		defaultSource: internal.GetHostname("wavefront_direct_sender"),
	}
	sender.internalRegistry = internal.NewMetricRegistry(
		sender,
		internal.SetPrefix("~sdk.go.core.sender.direct"),
		internal.SetTag("pid", strconv.Itoa(os.Getpid())),
	)

	if sdkVersion, e := internal.GetSemVer(version.Version); e == nil {
		sender.internalRegistry.NewGaugeFloat64("version", func() float64 {
			return sdkVersion
		})
	}

	sender.pointHandler = makeLineHandler(reporter, cfg, internal.MetricFormat, "points", sender.internalRegistry)
	sender.histoHandler = makeLineHandler(reporter, cfg, internal.HistogramFormat, "histograms", sender.internalRegistry)
	sender.spanHandler = makeLineHandler(reporter, cfg, internal.TraceFormat, "spans", sender.internalRegistry)
	sender.spanLogHandler = makeLineHandler(reporter, cfg, internal.SpanLogsFormat, "span_logs", sender.internalRegistry)
	sender.eventHandler = makeLineHandler(reporter, cfg, internal.EventFormat, "events", sender.internalRegistry)

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

	sender.Start()
	return sender, nil
}

func makeLineHandler(reporter internal.Reporter, cfg *DirectConfiguration, format, prefix string,
	registry *internal.MetricRegistry) *internal.LineHandler {
	flushInterval := time.Second * time.Duration(cfg.FlushIntervalSeconds)

	opts := []internal.LineHandlerOption{internal.SetHandlerPrefix(prefix), internal.SetRegistry(registry)}
	batchSize := cfg.BatchSize
	if format == internal.EventFormat {
		batchSize = 1
		opts = append(opts, internal.SetLockOnThrottledError(true))
	}

	return internal.NewLineHandler(reporter, format, flushInterval, batchSize, cfg.MaxBufferSize, opts...)
}

func (sender *directSender) Start() {
	sender.pointHandler.Start()
	sender.histoHandler.Start()
	sender.spanHandler.Start()
	sender.spanLogHandler.Start()
	sender.internalRegistry.Start()
	sender.eventHandler.Start()
}

func (sender *directSender) SendMetric(name string, value float64, ts int64, source string, tags map[string]string) error {
	line, err := MetricLine(name, value, ts, source, tags, sender.defaultSource)
	if err != nil {
		sender.pointsInvalid.Inc()
		return err
	} else {
		sender.pointsValid.Inc()
	}
	err = sender.pointHandler.HandleLine(line)
	if err != nil {
		sender.pointsDropped.Inc()
	}
	return err
}

func (sender *directSender) SendDeltaCounter(name string, value float64, source string, tags map[string]string) error {
	if name == "" {
		sender.pointsInvalid.Inc()
		return fmt.Errorf("empty metric name")
	}
	if !internal.HasDeltaPrefix(name) {
		name = internal.DeltaCounterName(name)
	}
	if value > 0 {
		return sender.SendMetric(name, value, 0, source, tags)
	}
	return nil
}

func (sender *directSender) SendDistribution(name string, centroids []histogram.Centroid,
	hgs map[histogram.Granularity]bool, ts int64, source string, tags map[string]string) error {
	line, err := HistoLine(name, centroids, hgs, ts, source, tags, sender.defaultSource)
	if err != nil {
		sender.histogramsInvalid.Inc()
		return err
	} else {
		sender.histogramsValid.Inc()
	}
	err = sender.histoHandler.HandleLine(line)
	if err != nil {
		sender.histogramsDropped.Inc()
	}
	return err
}

func (sender *directSender) SendSpan(name string, startMillis, durationMillis int64, source, traceId, spanId string,
	parents, followsFrom []string, tags []SpanTag, spanLogs []SpanLog) error {
	line, err := SpanLine(name, startMillis, durationMillis, source, traceId, spanId, parents, followsFrom, tags, spanLogs, sender.defaultSource)
	if err != nil {
		sender.spansInvalid.Inc()
		return err
	} else {
		sender.spansValid.Inc()
	}
	err = sender.spanHandler.HandleLine(line)
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
		err = sender.spanLogHandler.HandleLine(logs)
		if err != nil {
			sender.spanLogsDropped.Inc()
			return err
		}
	}
	return nil
}

func (sender *directSender) SendEvent(name string, startMillis, endMillis int64, source string, tags map[string]string, setters ...event.Option) error {
	line, err := EventLineJSON(name, startMillis, endMillis, source, tags, setters...)
	if err != nil {
		sender.eventsInvalid.Inc()
		return err
	} else {
		sender.eventsValid.Inc()
	}
	err = sender.eventHandler.HandleLine(line)
	if err != nil {
		sender.eventsDropped.Inc()
	}
	return err
}

func (sender *directSender) Close() {
	sender.pointHandler.Stop()
	sender.histoHandler.Stop()
	sender.spanHandler.Stop()
	sender.spanLogHandler.Stop()
	sender.internalRegistry.Stop()
	sender.eventHandler.Stop()
}

func (sender *directSender) Flush() error {
	errStr := ""
	err := sender.pointHandler.Flush()
	if err != nil {
		errStr = errStr + err.Error() + "\n"
	}
	err = sender.histoHandler.Flush()
	if err != nil {
		errStr = errStr + err.Error() + "\n"
	}
	err = sender.spanHandler.Flush()
	if err != nil {
		errStr = errStr + err.Error()
	}
	err = sender.spanLogHandler.Flush()
	if err != nil {
		errStr = errStr + err.Error()
	}
	err = sender.eventHandler.Flush()
	if err != nil {
		errStr = errStr + err.Error()
	}
	if errStr != "" {
		return fmt.Errorf(errStr)
	}
	return nil
}

func (sender *directSender) GetFailureCount() int64 {
	return sender.pointHandler.GetFailureCount() +
		sender.histoHandler.GetFailureCount() +
		sender.spanHandler.GetFailureCount() +
		sender.spanLogHandler.GetFailureCount() +
		sender.eventHandler.GetFailureCount()
}
