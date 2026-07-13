package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestNewLogger(t *testing.T) {
	t.Run("When creating a new logger it should produce JSON output with RFC3339 timestamps", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newLogger(zap.WriteTo(&buf))

		logger.Info("test message", "key", "value")

		var entry map[string]interface{}
		if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
			t.Fatalf("expected JSON log output, got error: %v\nraw output: %s", err, buf.String())
		}

		ts, ok := entry["ts"].(string)
		if !ok {
			t.Fatalf("expected string 'ts' field, got: %v", entry["ts"])
		}
		if _, err := time.Parse(time.RFC3339, ts); err != nil {
			t.Errorf("expected RFC3339 timestamp, got %q: %v", ts, err)
		}

		if msg, _ := entry["msg"].(string); msg != "test message" {
			t.Errorf("expected msg 'test message', got %q", msg)
		}

		if v, _ := entry["key"].(string); v != "value" {
			t.Errorf("expected key 'value', got %q", v)
		}
	})
}
