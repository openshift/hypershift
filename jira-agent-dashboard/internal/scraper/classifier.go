package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Classification represents the result of classifying a code review comment.
type Classification struct {
	Severity string `json:"severity"`
	Topic    string `json:"topic"`
}

var validSeverities = map[string]bool{
	"nitpick":         true,
	"suggestion":      true,
	"required_change": true,
	"question":        true,
}

var validTopics = map[string]bool{
	"style":         true,
	"logic_bug":     true,
	"test_gap":      true,
	"api_design":    true,
	"documentation": true,
}

// Classifier uses Claude API to classify code review comments.
type Classifier struct {
	apiEndpoint string
	apiKey      string
	httpClient  *http.Client
}

// NewClassifier creates a new Classifier with the given API endpoint and key.
func NewClassifier(apiEndpoint, apiKey string) *Classifier {
	return &Classifier{
		apiEndpoint: apiEndpoint,
		apiKey:      apiKey,
		httpClient:  &http.Client{},
	}
}

// Classify sends a comment body to Claude API and returns the classification.
func (c *Classifier) Classify(ctx context.Context, commentBody string) (*Classification, error) {
	// Construct the request payload
	systemPrompt := `Classify this code review comment. Return JSON with two fields:
- "severity": one of "nitpick", "suggestion", "required_change", "question"
- "topic": one of "style", "logic_bug", "test_gap", "api_design", "documentation"`

	requestBody := map[string]interface{}{
		"model":      "claude-haiku-4-5-20251001",
		"max_tokens": 100,
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": commentBody,
			},
		},
		"system": systemPrompt,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", c.apiEndpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set required headers
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the Anthropic API response
	var apiResponse struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	// Extract the text content
	if len(apiResponse.Content) == 0 {
		return nil, fmt.Errorf("API response contains no content")
	}

	text := apiResponse.Content[0].Text

	// Parse the classification JSON from the text
	classification, err := parseClassificationJSON(text)
	if err != nil {
		return nil, fmt.Errorf("failed to parse classification: %w", err)
	}

	// Validate and apply defaults
	if !validSeverities[classification.Severity] {
		classification.Severity = "suggestion"
	}
	if !validTopics[classification.Topic] {
		classification.Topic = "style"
	}

	return classification, nil
}

// parseClassificationJSON extracts and parses the classification JSON from the response text.
func parseClassificationJSON(text string) (*Classification, error) {
	// The text might contain additional explanation, so we try to extract JSON
	// First, try parsing the entire text as JSON
	var classification Classification
	if err := json.Unmarshal([]byte(text), &classification); err == nil {
		return &classification, nil
	}

	// If that fails, try to find JSON-like structure in the text
	// Look for { and } to extract JSON
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")

	if start == -1 || end == -1 || start >= end {
		return nil, fmt.Errorf("no valid JSON found in response text")
	}

	jsonStr := text[start : end+1]
	if err := json.Unmarshal([]byte(jsonStr), &classification); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return &classification, nil
}
