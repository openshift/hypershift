package oadp

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/go-logr/logr/testr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestHasOADPPauseAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		expected    bool
	}{
		{
			name:     "nil cluster",
			expected: false,
		},
		{
			name:        "no annotations",
			annotations: nil,
			expected:    false,
		},
		{
			name:        "empty annotations",
			annotations: map[string]string{},
			expected:    false,
		},
		{
			name: "missing paused-by annotation",
			annotations: map[string]string{
				OADPAuditPausedAtAnnotation: "2023-01-01T00:00:00Z",
			},
			expected: false,
		},
		{
			name: "missing paused-at annotation",
			annotations: map[string]string{
				OADPAuditPausedByAnnotation: OADPAuditPausedPluginAuthor,
			},
			expected: false,
		},
		{
			name: "wrong paused-by value",
			annotations: map[string]string{
				OADPAuditPausedByAnnotation: "some-other-plugin",
				OADPAuditPausedAtAnnotation: "2023-01-01T00:00:00Z",
			},
			expected: false,
		},
		{
			name: "empty paused-at value",
			annotations: map[string]string{
				OADPAuditPausedByAnnotation: OADPAuditPausedPluginAuthor,
				OADPAuditPausedAtAnnotation: "",
			},
			expected: false,
		},
		{
			name: "valid OADP pause annotations",
			annotations: map[string]string{
				OADPAuditPausedByAnnotation: OADPAuditPausedPluginAuthor,
				OADPAuditPausedAtAnnotation: "2023-01-01T00:00:00Z",
			},
			expected: true,
		},
		{
			name: "valid OADP pause annotations with extra annotations",
			annotations: map[string]string{
				OADPAuditPausedByAnnotation: OADPAuditPausedPluginAuthor,
				OADPAuditPausedAtAnnotation: "2023-01-01T00:00:00Z",
				"other.annotation/key":      "value",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			var hc *hyperv1.HostedCluster
			if tt.annotations != nil || tt.name != "nil cluster" {
				hc = &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: tt.annotations,
					},
				}
			}

			result := HasOADPPauseAnnotations(hc)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestIsBackupInTerminalState(t *testing.T) {
	logger := testr.New(t)
	ctx := context.Background()

	tests := []struct {
		name           string
		backup         unstructured.Unstructured
		expectedResult bool
		expectedPhase  string
		expectError    bool
	}{
		{
			name: "backup in Completed state",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"phase": "Completed",
					},
				},
			},
			expectedResult: true,
			expectedPhase:  "Completed",
			expectError:    false,
		},
		{
			name: "backup in Failed state",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"phase": "Failed",
					},
				},
			},
			expectedResult: true,
			expectedPhase:  "Failed",
			expectError:    false,
		},
		{
			name: "backup in PartiallyFailed state",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"phase": "PartiallyFailed",
					},
				},
			},
			expectedResult: true,
			expectedPhase:  "PartiallyFailed",
			expectError:    false,
		},
		{
			name: "backup in Deleted state",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"phase": "Deleted",
					},
				},
			},
			expectedResult: true,
			expectedPhase:  "Deleted",
			expectError:    false,
		},
		{
			name: "backup in InProgress state",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"phase": "InProgress",
					},
				},
			},
			expectedResult: false,
			expectedPhase:  "InProgress",
			expectError:    false,
		},
		{
			name: "backup in New state",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"phase": "New",
					},
				},
			},
			expectedResult: false,
			expectedPhase:  "New",
			expectError:    false,
		},
		{
			name: "backup with no status",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
			expectedResult: false,
			expectedPhase:  "",
			expectError:    true,
		},
		{
			name: "backup with no phase",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{},
				},
			},
			expectedResult: false,
			expectedPhase:  "",
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			isTerminal, phase, err := IsBackupInTerminalState(ctx, tt.backup, logger)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}

			g.Expect(isTerminal).To(Equal(tt.expectedResult))
			g.Expect(phase).To(Equal(tt.expectedPhase))
		})
	}
}

func TestIsBackupRelatedToCluster(t *testing.T) {
	cluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
	}

	tests := []struct {
		name     string
		backup   unstructured.Unstructured
		expected bool
	}{
		{
			name: "backup name contains cluster name",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "backup-test-cluster-20231201",
					},
				},
			},
			expected: true,
		},
		{
			name: "backup name contains namespace-cluster pattern",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "backup-test-ns-test-cluster-20231201",
					},
				},
			},
			expected: true,
		},
		{
			name: "backup includedNamespaces contains cluster namespace",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "some-backup-name",
					},
					"spec": map[string]interface{}{
						"includedNamespaces": []interface{}{
							"test-ns",
							"other-ns",
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "backup includedNamespaces contains namespace-cluster pattern",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "some-backup-name",
					},
					"spec": map[string]interface{}{
						"includedNamespaces": []interface{}{
							"test-ns-test-cluster",
							"other-ns",
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "backup not related to cluster",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": map[string]interface{}{
						"name": "unrelated-backup",
					},
					"spec": map[string]interface{}{
						"includedNamespaces": []interface{}{
							"other-ns",
							"another-ns",
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "backup with no name or spec",
			backup: unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Set the GVK for the backup
			tt.backup.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "velero.io",
				Version: "v1",
				Kind:    "Backup",
			})

			result := IsBackupRelatedToCluster(tt.backup, cluster)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}