package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseClassification(t *testing.T) {
	tests := []struct {
		name          string
		responseJSON  string
		wantSeverity  string
		wantTopic     string
		wantErr       bool
	}{
		{
			name:         "When classification response is valid it should parse severity and topic",
			responseJSON: `{"severity": "required_change", "topic": "logic_bug"}`,
			wantSeverity: "required_change",
			wantTopic:    "logic_bug",
			wantErr:      false,
		},
		{
			name:         "When severity is nitpick it should parse correctly",
			responseJSON: `{"severity": "nitpick", "topic": "style"}`,
			wantSeverity: "nitpick",
			wantTopic:    "style",
			wantErr:      false,
		},
		{
			name:         "When severity is suggestion it should parse correctly",
			responseJSON: `{"severity": "suggestion", "topic": "api_design"}`,
			wantSeverity: "suggestion",
			wantTopic:    "api_design",
			wantErr:      false,
		},
		{
			name:         "When severity is question it should parse correctly",
			responseJSON: `{"severity": "question", "topic": "documentation"}`,
			wantSeverity: "question",
			wantTopic:    "documentation",
			wantErr:      false,
		},
		{
			name:         "When topic is test_gap it should parse correctly",
			responseJSON: `{"severity": "required_change", "topic": "test_gap"}`,
			wantSeverity: "required_change",
			wantTopic:    "test_gap",
			wantErr:      false,
		},
		{
			name:         "When severity is invalid it should default to suggestion",
			responseJSON: `{"severity": "invalid_severity", "topic": "style"}`,
			wantSeverity: "suggestion",
			wantTopic:    "style",
			wantErr:      false,
		},
		{
			name:         "When topic is invalid it should default to style",
			responseJSON: `{"severity": "nitpick", "topic": "invalid_topic"}`,
			wantSeverity: "nitpick",
			wantTopic:    "style",
			wantErr:      false,
		},
		{
			name:         "When both are invalid it should default both",
			responseJSON: `{"severity": "bad", "topic": "bad"}`,
			wantSeverity: "suggestion",
			wantTopic:    "style",
			wantErr:      false,
		},
		{
			name:         "When JSON is empty it should return error",
			responseJSON: ``,
			wantErr:      true,
		},
		{
			name:         "When JSON is malformed it should return error",
			responseJSON: `{invalid json}`,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var classification Classification
			err := json.Unmarshal([]byte(tt.responseJSON), &classification)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			// Apply validation and defaults
			if !validSeverities[classification.Severity] {
				classification.Severity = "suggestion"
			}
			if !validTopics[classification.Topic] {
				classification.Topic = "style"
			}

			if classification.Severity != tt.wantSeverity {
				t.Errorf("Severity = %v, want %v", classification.Severity, tt.wantSeverity)
			}
			if classification.Topic != tt.wantTopic {
				t.Errorf("Topic = %v, want %v", classification.Topic, tt.wantTopic)
			}
		})
	}
}

func TestClassifyComment(t *testing.T) {
	tests := []struct {
		name              string
		commentBody       string
		apiResponse       map[string]interface{}
		wantSeverity      string
		wantTopic         string
		wantErr           bool
	}{
		{
			name:        "When API returns valid classification it should parse correctly",
			commentBody: "This function has a potential nil pointer dereference",
			apiResponse: map[string]interface{}{
				"id":      "msg_123",
				"type":    "message",
				"role":    "assistant",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": `{"severity": "required_change", "topic": "logic_bug"}`,
					},
				},
				"model": "claude-haiku-4-5-20251001",
			},
			wantSeverity: "required_change",
			wantTopic:    "logic_bug",
			wantErr:      false,
		},
		{
			name:        "When API returns style suggestion it should parse correctly",
			commentBody: "Consider using camelCase for variable names",
			apiResponse: map[string]interface{}{
				"id":      "msg_124",
				"type":    "message",
				"role":    "assistant",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": `{"severity": "nitpick", "topic": "style"}`,
					},
				},
				"model": "claude-haiku-4-5-20251001",
			},
			wantSeverity: "nitpick",
			wantTopic:    "style",
			wantErr:      false,
		},
		{
			name:        "When API returns test gap it should parse correctly",
			commentBody: "This edge case is not covered by tests",
			apiResponse: map[string]interface{}{
				"id":      "msg_125",
				"type":    "message",
				"role":    "assistant",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": `{"severity": "suggestion", "topic": "test_gap"}`,
					},
				},
				"model": "claude-haiku-4-5-20251001",
			},
			wantSeverity: "suggestion",
			wantTopic:    "test_gap",
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock HTTP server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request headers
				if r.Header.Get("x-api-key") != "test-api-key" {
					t.Errorf("Expected x-api-key header to be 'test-api-key', got %v", r.Header.Get("x-api-key"))
				}
				if r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Errorf("Expected anthropic-version header to be '2023-06-01', got %v", r.Header.Get("anthropic-version"))
				}
				if r.Header.Get("content-type") != "application/json" {
					t.Errorf("Expected content-type header to be 'application/json', got %v", r.Header.Get("content-type"))
				}

				// Verify request method and path
				if r.Method != "POST" {
					t.Errorf("Expected POST request, got %v", r.Method)
				}

				// Return mock response
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(tt.apiResponse)
			}))
			defer server.Close()

			// Create classifier with mock server URL
			classifier := NewClassifier(server.URL, "test-api-key")

			// Call Classify
			classification, err := classifier.Classify(context.Background(), tt.commentBody)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if classification.Severity != tt.wantSeverity {
				t.Errorf("Severity = %v, want %v", classification.Severity, tt.wantSeverity)
			}
			if classification.Topic != tt.wantTopic {
				t.Errorf("Topic = %v, want %v", classification.Topic, tt.wantTopic)
			}
		})
	}
}
