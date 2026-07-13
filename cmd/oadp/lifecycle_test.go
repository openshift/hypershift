package oadp

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/openshift/hypershift/cmd/log"
	supportoadp "github.com/openshift/hypershift/support/oadp"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestVerifyBackup(t *testing.T) {
	tests := []struct {
		name        string
		backup      map[string]interface{}
		expectError bool
		failChecks  []string
	}{
		{
			name: "When backup is completed with valid items it should pass all checks",
			backup: map[string]interface{}{
				"apiVersion": "velero.io/v1",
				"kind":       "Backup",
				"metadata": map[string]interface{}{
					"name":              "test-backup",
					"namespace":         "openshift-adp",
					"creationTimestamp": "2026-06-01T10:00:00Z",
				},
				"spec": map[string]interface{}{
					"storageLocation": "default",
				},
				"status": map[string]interface{}{
					"phase":      "Completed",
					"expiration": time.Now().Add(24 * time.Hour).Format(time.RFC3339),
					"progress": map[string]interface{}{
						"itemsBackedUp": int64(142),
						"totalItems":    int64(142),
					},
				},
			},
			expectError: false,
		},
		{
			name: "When backup has failed phase it should fail the phase check",
			backup: map[string]interface{}{
				"apiVersion": "velero.io/v1",
				"kind":       "Backup",
				"metadata": map[string]interface{}{
					"name":              "failed-backup",
					"namespace":         "openshift-adp",
					"creationTimestamp": "2026-06-01T10:00:00Z",
				},
				"spec": map[string]interface{}{},
				"status": map[string]interface{}{
					"phase": "Failed",
					"progress": map[string]interface{}{
						"itemsBackedUp": int64(0),
						"totalItems":    int64(100),
					},
				},
			},
			expectError: false,
			failChecks:  []string{"phase", "items"},
		},
		{
			name: "When backup has expired it should fail the expiration check",
			backup: map[string]interface{}{
				"apiVersion": "velero.io/v1",
				"kind":       "Backup",
				"metadata": map[string]interface{}{
					"name":              "expired-backup",
					"namespace":         "openshift-adp",
					"creationTimestamp": "2026-06-01T10:00:00Z",
				},
				"spec": map[string]interface{}{},
				"status": map[string]interface{}{
					"phase":      "Completed",
					"expiration": time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
					"progress": map[string]interface{}{
						"itemsBackedUp": int64(142),
						"totalItems":    int64(142),
					},
				},
			},
			expectError: false,
			failChecks:  []string{"expiration"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()

			backupObj := &unstructured.Unstructured{Object: tt.backup}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(backupObj).
				Build()

			results, err := VerifyBackup(ctx, fakeClient, backupObj.GetName(), "openshift-adp", log.Log)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			failedChecks := make(map[string]bool)
			for _, r := range results {
				if !r.Passed {
					failedChecks[r.Check] = true
				}
			}

			for _, expected := range tt.failChecks {
				if !failedChecks[expected] {
					t.Errorf("expected check '%s' to fail, but it passed", expected)
				}
			}
		})
	}
}

func TestAutoIncludeAgentNamespace(t *testing.T) {
	tests := []struct {
		name              string
		platform          string
		agentNamespace    string
		existingNS        []string
		expectedNS        []string
		expectLogMessage  bool
	}{
		{
			name:           "When platform is AGENT with namespace it should auto-include",
			platform:       "AGENT",
			agentNamespace: "my-agent-ns",
			existingNS:     nil,
			expectedNS:     []string{"my-agent-ns"},
			expectLogMessage: true,
		},
		{
			name:           "When platform is AGENT and namespace already included it should not duplicate",
			platform:       "AGENT",
			agentNamespace: "my-agent-ns",
			existingNS:     []string{"my-agent-ns"},
			expectedNS:     []string{"my-agent-ns"},
			expectLogMessage: false,
		},
		{
			name:           "When platform is AWS it should not include agent namespace",
			platform:       "AWS",
			agentNamespace: "",
			existingNS:     nil,
			expectedNS:     nil,
			expectLogMessage: false,
		},
		{
			name:           "When platform is AGENT but namespace is empty it should skip",
			platform:       "AGENT",
			agentNamespace: "",
			existingNS:     nil,
			expectedNS:     nil,
			expectLogMessage: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &CreateOptions{
				Log:               log.Log,
				IncludeNamespaces: tt.existingNS,
			}
			info := &supportoadp.PlatformInfo{
				Type:           tt.platform,
				AgentNamespace: tt.agentNamespace,
			}

			autoIncludeAgentNamespace(opts, info)

			if len(tt.expectedNS) == 0 && len(opts.IncludeNamespaces) == 0 {
				return
			}
			if len(opts.IncludeNamespaces) != len(tt.expectedNS) {
				t.Errorf("expected namespaces %v, got %v", tt.expectedNS, opts.IncludeNamespaces)
			}
			for i, ns := range tt.expectedNS {
				if i >= len(opts.IncludeNamespaces) || opts.IncludeNamespaces[i] != ns {
					t.Errorf("expected namespace[%d]=%q, got %v", i, ns, opts.IncludeNamespaces)
				}
			}
		})
	}
}

func TestGetOutputTable(t *testing.T) {
	items := []unstructured.Unstructured{
		{
			Object: map[string]interface{}{
				"apiVersion": "velero.io/v1",
				"kind":       "Backup",
				"metadata": map[string]interface{}{
					"name":              "test-backup-1",
					"namespace":         "openshift-adp",
					"creationTimestamp": time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
				},
				"status": map[string]interface{}{
					"phase": "Completed",
				},
			},
		},
		{
			Object: map[string]interface{}{
				"apiVersion": "velero.io/v1",
				"kind":       "Backup",
				"metadata": map[string]interface{}{
					"name":              "test-backup-2",
					"namespace":         "openshift-adp",
					"creationTimestamp": time.Now().Add(-48 * time.Hour).Format(time.RFC3339),
				},
				"status": map[string]interface{}{
					"phase": "Failed",
				},
			},
		},
	}

	var buf bytes.Buffer
	err := outputTable(&buf, items, "Backup")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("NAME")) {
		t.Error("table output should contain NAME header")
	}
	if !bytes.Contains([]byte(output), []byte("test-backup-1")) {
		t.Error("table output should contain test-backup-1")
	}
	if !bytes.Contains([]byte(output), []byte("Completed")) {
		t.Error("table output should contain Completed status")
	}
}

func TestDestroyOptions(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()

	backup := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "velero.io/v1",
			"kind":       "Backup",
			"metadata": map[string]interface{}{
				"name":      "to-delete",
				"namespace": "openshift-adp",
			},
			"spec": map[string]interface{}{},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(backup).
		Build()

	opts := &DestroyOptions{
		Name:          "to-delete",
		OADPNamespace: "openshift-adp",
		Log:           log.Log,
		Client:        fakeClient,
	}

	err := opts.runDestroy(ctx, "Backup")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestDestroyOptionsNotFound(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	opts := &DestroyOptions{
		Name:          "nonexistent",
		OADPNamespace: "openshift-adp",
		Log:           log.Log,
		Client:        fakeClient,
	}

	err := opts.runDestroy(ctx, "Backup")
	if err == nil {
		t.Fatal("expected error for nonexistent backup, got none")
	}
}
