package senders

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wavefronthq/wavefront-sdk-go/event"
	"github.com/wavefronthq/wavefront-sdk-go/histogram"
	"github.com/wavefronthq/wavefront-sdk-go/internal"
)

var /* const */ quotation = regexp.MustCompile("\"")
var /* const */ lineBreak = regexp.MustCompile("\\n")

// Gets a metric line in the Wavefront metrics data format:
// <metricName> <metricValue> [<timestamp>] source=<source> [pointTags]
// Example: "new-york.power.usage 42422.0 1533531013 source=localhost datacenter=dc1"
func MetricLine(name string, value float64, ts int64, source string, tags map[string]string, defaultSource string) (string, error) {
	if name == "" {
		return "", errors.New("empty metric name")
	}

	if source == "" {
		source = defaultSource
	}

	sb := internal.GetBuffer()
	defer internal.PutBuffer(sb)

	sb.WriteString(strconv.Quote(sanitizeInternal(name)))
	sb.WriteString(" ")
	sb.WriteString(strconv.FormatFloat(value, 'f', -1, 64))

	if ts != 0 {
		sb.WriteString(" ")
		sb.WriteString(strconv.FormatInt(ts, 10))
	}

	sb.WriteString(" source=")
	sb.WriteString(sanitizeValue(source))

	for k, v := range tags {
		if v == "" {
			return "", errors.New("metric point tag value cannot be blank")
		}
		sb.WriteString(" ")
		sb.WriteString(strconv.Quote(sanitizeInternal(k)))
		sb.WriteString("=")
		sb.WriteString(sanitizeValue(v))
	}
	sb.WriteString("\n")
	return sb.String(), nil
}

// Gets a histogram line in the Wavefront histogram data format:
// {!M | !H | !D} [<timestamp>] #<count> <mean> [centroids] <histogramName> source=<source> [pointTags]
// Example: "!M 1533531013 #20 30.0 #10 5.1 request.latency source=appServer1 region=us-west"
func HistoLine(name string, centroids histogram.Centroids, hgs map[histogram.Granularity]bool, ts int64, source string, tags map[string]string, defaultSource string) (string, error) {
	if name == "" {
		return "", errors.New("empty distribution name")
	}

	if len(centroids) == 0 {
		return "", errors.New("distribution should have at least one centroid")
	}

	if len(hgs) == 0 {
		return "", errors.New("histogram granularities cannot be empty")
	}

	if source == "" {
		source = defaultSource
	}

	sb := internal.GetBuffer()
	defer internal.PutBuffer(sb)

	if ts != 0 {
		sb.WriteString(" ")
		sb.WriteString(strconv.FormatInt(ts, 10))
	}
	// Preprocess line. We know len(hgs) > 0 here.
	for _, centroid := range centroids.Compact() {
		sb.WriteString(" #")
		sb.WriteString(strconv.Itoa(centroid.Count))
		sb.WriteString(" ")
		sb.WriteString(strconv.FormatFloat(centroid.Value, 'f', -1, 64))
	}
	sb.WriteString(" ")
	sb.WriteString(strconv.Quote(sanitizeInternal(name)))
	sb.WriteString(" source=")
	sb.WriteString(sanitizeValue(source))

	for k, v := range tags {
		if v == "" {
			return "", errors.New("histogram tag value cannot be blank")
		}
		sb.WriteString(" ")
		sb.WriteString(strconv.Quote(sanitizeInternal(k)))
		sb.WriteString("=")
		sb.WriteString(sanitizeValue(v))
	}
	sbBytes := sb.Bytes()

	sbg := bytes.Buffer{}
	for hg, on := range hgs {
		if on {
			sbg.WriteString(hg.String())
			sbg.Write(sbBytes)
			sbg.WriteString("\n")
		}
	}
	return sbg.String(), nil
}

// Gets a span line in the Wavefront span data format:
// <tracingSpanName> source=<source> [pointTags] <start_millis> <duration_milli_seconds>
// Example:
// "getAllUsers source=localhost traceId=7b3bf470-9456-11e8-9eb6-529269fb1459 spanId=0313bafe-9457-11e8-9eb6-529269fb1459
//    parent=2f64e538-9457-11e8-9eb6-529269fb1459 application=Wavefront http.method=GET 1533531013 343500"
func SpanLine(name string, startMillis, durationMillis int64, source, traceId, spanId string, parents, followsFrom []string, tags []SpanTag, spanLogs []SpanLog, defaultSource string) (string, error) {
	if name == "" {
		return "", errors.New("empty span name")
	}

	if source == "" {
		source = defaultSource
	}

	if !isUUIDFormat(traceId) {
		return "", errors.New("traceId is not in UUID format")
	}
	if !isUUIDFormat(spanId) {
		return "", errors.New("spanId is not in UUID format")
	}

	sb := internal.GetBuffer()
	defer internal.PutBuffer(sb)

	sb.WriteString(sanitizeValue(name))
	sb.WriteString(" source=")
	sb.WriteString(sanitizeValue(source))
	sb.WriteString(" traceId=")
	sb.WriteString(traceId)
	sb.WriteString(" spanId=")
	sb.WriteString(spanId)

	for _, parent := range parents {
		sb.WriteString(" parent=")
		sb.WriteString(parent)
	}

	for _, item := range followsFrom {
		sb.WriteString(" followsFrom=")
		sb.WriteString(item)
	}

	if len(spanLogs) > 0 {
		sb.WriteString(" ")
		sb.WriteString(strconv.Quote(sanitizeInternal("_spanLogs")))
		sb.WriteString("=")
		sb.WriteString(strconv.Quote(sanitizeInternal("true")))
	}

	for _, tag := range tags {
		if tag.Key == "" || tag.Value == "" {
			return "", errors.New("span tag key/value cannot be blank")
		}
		sb.WriteString(" ")
		sb.WriteString(strconv.Quote(sanitizeInternal(tag.Key)))
		sb.WriteString("=")
		sb.WriteString(sanitizeValue(tag.Value))
	}
	sb.WriteString(" ")
	sb.WriteString(strconv.FormatInt(startMillis, 10))
	sb.WriteString(" ")
	sb.WriteString(strconv.FormatInt(durationMillis, 10))
	sb.WriteString("\n")

	return sb.String(), nil
}

func SpanLogJSON(traceId, spanId string, spanLogs []SpanLog) (string, error) {
	l := SpanLogs{
		TraceId: traceId,
		SpanId:  spanId,
		Logs:    spanLogs,
	}
	out, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	return string(out[:]) + "\n", nil
}

// EventLine encode the event to a wf proxy format
// set endMillis to 0 for a 'Instantaneous' event
func EventLine(name string, startMillis, endMillis int64, source string, tags map[string]string, setters ...event.Option) (string, error) {
	sb := internal.GetBuffer()
	defer internal.PutBuffer(sb)

	annotations := map[string]string{}
	l := map[string]interface{}{
		"annotations": annotations,
	}
	for _, set := range setters {
		set(l)
	}

	sb.WriteString("@Event")

	startMillis, endMillis = adjustStartEndTime(startMillis, endMillis)

	sb.WriteString(" ")
	sb.WriteString(strconv.FormatInt(startMillis, 10))
	sb.WriteString(" ")
	sb.WriteString(strconv.FormatInt(endMillis, 10))

	sb.WriteString(" ")
	sb.WriteString(strconv.Quote(name))

	for k, v := range annotations {
		sb.WriteString(" ")
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(strconv.Quote(v))
	}

	if len(source) > 0 {
		sb.WriteString(" host=")
		sb.WriteString(strconv.Quote(source))
	}

	for k, v := range tags {
		sb.WriteString(" tag=")
		sb.WriteString(strconv.Quote(fmt.Sprintf("%v: %v", k, v)))
	}

	sb.WriteString("\n")
	return sb.String(), nil
}

// EventLine encode the event to a wf API format
// set endMillis to 0 for a 'Instantaneous' event
func EventLineJSON(name string, startMillis, endMillis int64, source string, tags map[string]string, setters ...event.Option) (string, error) {
	annotations := map[string]string{}
	l := map[string]interface{}{
		"name":        name,
		"annotations": annotations,
	}

	for _, set := range setters {
		set(l)
	}

	startMillis, endMillis = adjustStartEndTime(startMillis, endMillis)

	l["startTime"] = startMillis
	l["endTime"] = endMillis

	if len(tags) > 0 {
		var tagList []string
		for k, v := range tags {
			tagList = append(tagList, fmt.Sprintf("%v: %v", k, v))
		}
		l["tags"] = tagList
	}

	if len(source) > 0 {
		l["hosts"] = []string{source}
	}

	jsonData, err := json.Marshal(l)
	if err != nil {
		return "", err
	}

	return string(jsonData), nil
}

func adjustStartEndTime(startMillis, endMillis int64) (int64, int64) {
	// secs to millis
	if startMillis < 999999999999 {
		startMillis = startMillis * 1000
	}

	if endMillis <= 999999999999 {
		endMillis = endMillis * 1000
	}

	if endMillis == 0 {
		endMillis = startMillis + 1
	}
	return startMillis, endMillis
}

func isUUIDFormat(str string) bool {
	l := len(str)
	if l != 36 {
		return false
	}
	for i := 0; i < l; i++ {
		c := str[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')) {
			return false
		}
	}
	return true
}

//Sanitize string of metric name, source and key of tags according to the rule of Wavefront proxy.
func sanitizeInternal(str string) string {
	sb := internal.GetBuffer()
	defer internal.PutBuffer(sb)

	// first character can be \u2206 (∆ - INCREMENT) or \u0394 (Δ - GREEK CAPITAL LETTER DELTA)
	// or ~ tilda character for internal metrics
	skipHead := 0
	if strings.HasPrefix(str, internal.DeltaPrefix) {
		sb.WriteString(internal.DeltaPrefix)
		skipHead = 3
	}
	if strings.HasPrefix(str, internal.AltDeltaPrefix) {
		sb.WriteString(internal.AltDeltaPrefix)
		skipHead = 2
	}
	// Second character can be ~ tilda character if first character
	// is \u2206 (∆ - INCREMENT) or \u0394 (Δ - GREEK CAPITAL LETTER)
	if (strings.HasPrefix(str, internal.DeltaPrefix) || strings.HasPrefix(str, internal.AltDeltaPrefix)) &&
		str[skipHead] == 126 {
		sb.WriteString(string(str[skipHead]))
		skipHead += 1
	}
	if str[0] == 126 {
		sb.WriteString(string(str[0]))
		skipHead = 1
	}

	for i := 0; i < len(str); i++ {
		if skipHead > 0 {
			i += skipHead
			skipHead = 0
		}
		cur := str[i]
		strCur := string(cur)
		isLegal := true

		if !(44 <= cur && cur <= 57) && !(65 <= cur && cur <= 90) && !(97 <= cur && cur <= 122) && cur != 95 {
			isLegal = false
		}
		if isLegal {
			sb.WriteString(strCur)
		} else {
			sb.WriteString("-")
		}
	}
	return sb.String()
}

//Sanitize string of tags value, etc.
func sanitizeValue(str string) string {
	res := strings.TrimSpace(str)
	if strings.Contains(str, "\"") || strings.Contains(str, "'") {
		res = quotation.ReplaceAllString(res, "\\\"")
	}
	return "\"" + lineBreak.ReplaceAllString(res, "\\n") + "\""
}
