package util

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"github.com/google/go-cmp/cmp"
)

func TestEnforceRestrictedSecurityContextToContainers(t *testing.T) {
	tests := []struct {
		name     string
		podSpec  *corev1.PodSpec
		expected *corev1.PodSpec
	}{
		{
			name: "basic application with no exceptions",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test-container",
						Image: "test-image",
					},
				},
			},

			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test-container",
						Image: "test-image",
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
					},
				},
			},
		},
		{
			name: "preserves capabilities from deployment template",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "haproxy",
						Image: "haproxy-image",
						SecurityContext: &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Add: []corev1.Capability{"NET_BIND_SERVICE"},
							},
						},
					},
					{
						Name:  "other-container",
						Image: "other-image",
					},
				},
			},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "haproxy",
						Image: "haproxy-image",
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
								Add:  []corev1.Capability{"NET_BIND_SERVICE"},
							},
						},
					},
					{
						Name:  "other-container",
						Image: "other-image",
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
					},
				},
			},
		},
		{
			name: "application with init containers",
			podSpec: &corev1.PodSpec{
				InitContainers: []corev1.Container{
					{
						Name:  "init-container",
						Image: "init-image",
					},
				},
				Containers: []corev1.Container{
					{
						Name:  "main-container",
						Image: "main-image",
					},
				},
			},

			expected: &corev1.PodSpec{
				InitContainers: []corev1.Container{
					{
						Name:  "init-container",
						Image: "init-image",
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
					},
				},
				Containers: []corev1.Container{
					{
						Name:  "main-container",
						Image: "main-image",
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
					},
				},
			},
		},
		{
			name: "preserves existing security context fields",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test-container",
						Image: "test-image",
						SecurityContext: &corev1.SecurityContext{
							RunAsUser: ptr.To[int64](1001),
						},
					},
				},
			},

			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test-container",
						Image: "test-image",
						SecurityContext: &corev1.SecurityContext{
							RunAsUser:                ptr.To[int64](1001),
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
					},
				},
			},
		},
		{
			name: "preserves different capabilities for multiple containers",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "haproxy",
						Image: "haproxy-image",
						SecurityContext: &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Add: []corev1.Capability{"NET_BIND_SERVICE"},
							},
						},
					},
					{
						Name:  "router",
						Image: "router-image",
						SecurityContext: &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Add: []corev1.Capability{"NET_BIND_SERVICE"},
							},
						},
					},
					{
						Name:  "normal-container",
						Image: "normal-image",
					},
				},
			},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "haproxy",
						Image: "haproxy-image",
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
								Add:  []corev1.Capability{"NET_BIND_SERVICE"},
							},
						},
					},
					{
						Name:  "router",
						Image: "router-image",
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
								Add:  []corev1.Capability{"NET_BIND_SERVICE"},
							},
						},
					},
					{
						Name:  "normal-container",
						Image: "normal-image",
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
					},
				},
			},
		},
		{
			name: "overrides insecure AllowPrivilegeEscalation",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test-container",
						Image: "test-image",
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(true),
							RunAsUser:                ptr.To[int64](1001),
						},
					},
				},
			},

			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test-container",
						Image: "test-image",
						SecurityContext: &corev1.SecurityContext{
							RunAsUser:                ptr.To[int64](1001),
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
					},
				},
			},
		},
		{
			name: "overrides insecure RunAsNonRoot",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test-container",
						Image: "test-image",
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot: ptr.To(false),
							RunAsUser:    ptr.To[int64](1001),
						},
					},
				},
			},

			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test-container",
						Image: "test-image",
						SecurityContext: &corev1.SecurityContext{
							RunAsUser:                ptr.To[int64](1001),
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
					},
				},
			},
		},
		{
			name: "preserves existing add capabilities",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test-container",
						Image: "test-image",
						SecurityContext: &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Add:  []corev1.Capability{"NET_BIND_SERVICE"},
								Drop: []corev1.Capability{"CHOWN"},
							},
						},
					},
				},
			},

			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test-container",
						Image: "test-image",
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
								Add:  []corev1.Capability{"NET_BIND_SERVICE"},
							},
						},
					},
				},
			},
		},
		{
			name: "empty pod spec",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{},
			},

			expected: &corev1.PodSpec{
				Containers: []corev1.Container{},
			},
		},
		{
			name: "containers with explicitly nil security context",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:            "test-container",
						Image:           "test-image",
						SecurityContext: nil,
					},
				},
			},

			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test-container",
						Image: "test-image",
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							RunAsNonRoot:             ptr.To(true),
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := EnforceRestrictedSecurityContextToContainers(tt.podSpec)
			if err != nil {
				t.Errorf("EnforceRestrictedSecurityContextToContainers() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.expected, tt.podSpec); diff != "" {
				t.Errorf("EnforceRestrictedSecurityContextToContainers() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEnforceRestrictedSecurityContextToContainers_InvalidCapabilities(t *testing.T) {
	tests := []struct {
		name          string
		podSpec       *corev1.PodSpec
		expectedError string
	}{
		{
			name: "rejects SYS_ADMIN capability",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "bad-container",
						Image: "bad-image",
						SecurityContext: &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Add: []corev1.Capability{"SYS_ADMIN"},
							},
						},
					},
				},
			},
			expectedError: `container "bad-container": capability "SYS_ADMIN" is not allowed by restricted pod security standards (only NET_BIND_SERVICE is permitted)`,
		},
		{
			name: "rejects NET_ADMIN capability",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "network-container",
						Image: "network-image",
						SecurityContext: &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Add: []corev1.Capability{"NET_ADMIN"},
							},
						},
					},
				},
			},
			expectedError: `container "network-container": capability "NET_ADMIN" is not allowed by restricted pod security standards (only NET_BIND_SERVICE is permitted)`,
		},
		{
			name: "rejects invalid capability in init container",
			podSpec: &corev1.PodSpec{
				InitContainers: []corev1.Container{
					{
						Name:  "bad-init-container",
						Image: "bad-init-image",
						SecurityContext: &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Add: []corev1.Capability{"SYS_MODULE"},
							},
						},
					},
				},
			},
			expectedError: `container "bad-init-container": capability "SYS_MODULE" is not allowed by restricted pod security standards (only NET_BIND_SERVICE is permitted)`,
		},
		{
			name: "rejects multiple invalid capabilities",
			podSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "multi-cap-container",
						Image: "multi-cap-image",
						SecurityContext: &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Add: []corev1.Capability{"NET_BIND_SERVICE", "SYS_ADMIN"},
							},
						},
					},
				},
			},
			expectedError: `container "multi-cap-container": capability "SYS_ADMIN" is not allowed by restricted pod security standards (only NET_BIND_SERVICE is permitted)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := EnforceRestrictedSecurityContextToContainers(tt.podSpec)
			if err == nil {
				t.Errorf("EnforceRestrictedSecurityContextToContainers() expected error but got none")
				return
			}
			if err.Error() != tt.expectedError {
				t.Errorf("EnforceRestrictedSecurityContextToContainers() error = %q, want %q", err.Error(), tt.expectedError)
			}
		})
	}
}
