package scraper

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/openshift/jira-agent-dashboard/internal/db"
)

type otlpLine struct {
	Payload struct {
		ResourceLogs []struct {
			ScopeLogs []struct {
				LogRecords []otlpLogRecord `json:"logRecords"`
			} `json:"scopeLogs"`
		} `json:"resourceLogs"`
	} `json:"payload"`
}

type otlpLogRecord struct {
	TimeUnixNano string `json:"timeUnixNano"`
	Body         struct {
		StringValue string `json:"stringValue"`
	} `json:"body"`
	Attributes []otlpAttribute `json:"attributes"`
}

type otlpAttribute struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

func ParseOtelEvents(data []byte) ([]db.OtelEvent, error) {
	var events []db.OtelEvent
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var l otlpLine
		if err := json.Unmarshal(line, &l); err != nil {
			continue
		}

		for _, rl := range l.Payload.ResourceLogs {
			for _, sl := range rl.ScopeLogs {
				for _, rec := range sl.LogRecords {
					e := recordToEvent(rec)
					if e != nil {
						events = append(events, *e)
					}
				}
			}
		}
	}
	return events, scanner.Err()
}

func recordToEvent(rec otlpLogRecord) *db.OtelEvent {
	eventName := rec.Body.StringValue
	switch eventName {
	case "claude_code.api_request":
		return parseAPIRequest(rec)
	case "claude_code.tool_result":
		return parseToolResult(rec)
	case "claude_code.subagent_completed":
		return parseSubagentCompleted(rec)
	default:
		return nil
	}
}

func parseAPIRequest(rec otlpLogRecord) *db.OtelEvent {
	e := &db.OtelEvent{EventType: "api_request"}
	e.SessionID = attrString(rec.Attributes, "session.id")
	e.TimestampMs = nanoToMs(rec.TimeUnixNano)
	e.Model = attrString(rec.Attributes, "model")
	e.InputTokens = attrInt64(rec.Attributes, "input_tokens")
	e.OutputTokens = attrInt64(rec.Attributes, "output_tokens")
	e.CacheReadTokens = attrInt64(rec.Attributes, "cache_read_tokens")
	e.CacheCreateTokens = attrInt64(rec.Attributes, "cache_creation_tokens")
	e.CostUSD = attrFloat64(rec.Attributes, "cost_usd")
	e.DurationMs = attrInt64(rec.Attributes, "duration_ms")
	return e
}

func parseToolResult(rec otlpLogRecord) *db.OtelEvent {
	e := &db.OtelEvent{EventType: "tool_result"}
	e.SessionID = attrString(rec.Attributes, "session.id")
	e.TimestampMs = nanoToMs(rec.TimeUnixNano)
	e.ToolName = attrString(rec.Attributes, "tool_name")
	e.DurationMs = attrInt64(rec.Attributes, "duration_ms")
	e.ToolInputSize = attrInt64(rec.Attributes, "tool_input_size_bytes")
	e.ToolResultSize = attrInt64(rec.Attributes, "tool_result_size_bytes")
	if attrString(rec.Attributes, "success") == "true" {
		e.ToolSuccess = 1
	}
	return e
}

func parseSubagentCompleted(rec otlpLogRecord) *db.OtelEvent {
	e := &db.OtelEvent{EventType: "subagent_completed"}
	e.SessionID = attrString(rec.Attributes, "session.id")
	e.TimestampMs = nanoToMs(rec.TimeUnixNano)
	e.AgentType = attrString(rec.Attributes, "agent_type")
	e.DurationMs = attrInt64(rec.Attributes, "duration_ms")
	e.TotalTokens = attrInt64(rec.Attributes, "total_tokens")
	e.TotalToolUses = attrInt64(rec.Attributes, "total_tool_uses")
	e.Model = attrString(rec.Attributes, "model")
	return e
}

type otlpValue struct {
	StringValue string          `json:"stringValue"`
	IntValue    json.RawMessage `json:"intValue"`
	DoubleValue *float64        `json:"doubleValue"`
}

func attrString(attrs []otlpAttribute, key string) string {
	for _, a := range attrs {
		if a.Key == key {
			var v otlpValue
			if json.Unmarshal(a.Value, &v) != nil {
				continue
			}
			if v.StringValue != "" {
				return v.StringValue
			}
			if len(v.IntValue) > 0 {
				return string(v.IntValue)
			}
		}
	}
	return ""
}

func attrInt64(attrs []otlpAttribute, key string) int64 {
	s := attrString(attrs, key)
	if s == "" {
		return 0
	}
	s = strings.Trim(s, "\"")
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func attrFloat64(attrs []otlpAttribute, key string) float64 {
	for _, a := range attrs {
		if a.Key == key {
			var v otlpValue
			if json.Unmarshal(a.Value, &v) != nil {
				continue
			}
			if v.DoubleValue != nil {
				return *v.DoubleValue
			}
		}
	}
	s := attrString(attrs, key)
	if s == "" {
		return 0
	}
	s = strings.Trim(s, "\"")
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func nanoToMs(nanoStr string) int64 {
	ns, _ := strconv.ParseInt(nanoStr, 10, 64)
	if ns > 0 {
		return ns / 1_000_000
	}
	return 0
}
