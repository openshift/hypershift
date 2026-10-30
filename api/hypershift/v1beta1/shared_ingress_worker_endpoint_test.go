package v1beta1

import (
	"encoding/json"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSharedIngressWorkerAPIEndpointSerialization(t *testing.T) {
	tests := map[string]struct {
		endpoint SharedIngressWorkerAPIEndpoint
		want     map[string]any
	}{
		"When endpoint is absent, it should omit the field": {},
		"When IP is populated, it should emit IP and port": {
			endpoint: SharedIngressWorkerAPIEndpoint{
				IP:   "10.0.0.1",
				Port: 6443,
			},
			want: map[string]any{
				"ip":   "10.0.0.1",
				"port": float64(6443),
			},
		},
		"When hostname is populated, it should emit hostname and port": {
			endpoint: SharedIngressWorkerAPIEndpoint{
				Hostname: "router.example.com",
				Port:     6443,
			},
			want: map[string]any{
				"hostname": "router.example.com",
				"port":     float64(6443),
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(HostedControlPlaneStatus{SharedIngressWorkerAPIEndpoint: tt.endpoint})
			if err != nil {
				t.Fatal(err)
			}
			var status map[string]any
			if err := json.Unmarshal(encoded, &status); err != nil {
				t.Fatal(err)
			}
			got, present := status["sharedIngressWorkerAPIEndpoint"]
			if present != (tt.want != nil) || (present && !reflect.DeepEqual(got, tt.want)) {
				t.Fatalf("endpoint: got %#v (present=%t), want %#v", got, present, tt.want)
			}
		})
	}
}

// hostedControlPlaneStatusNMinus1 matches the previous status representation at
// 0b354df4b8, before sharedIngressWorkerAPIEndpoint. Keep its wire tags unchanged.
// The per-field lint exceptions preserve that historical API representation for rollback testing.
type hostedControlPlaneStatusNMinus1 struct {
	// conditions contains details for one aspect of the current state of the HostedControlPlane.
	// Current condition types are: "Available"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +kubebuilder:validation:MaxItems=100
	//nolint:kubeapilinter // Preserve the previous API representation.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ready denotes that the HostedControlPlane API Server is ready to
	// receive requests
	// This satisfies CAPI contract https://github.com/kubernetes-sigs/cluster-api/blob/cd3a694deac89d5ebeb888307deaa61487207aa0/controllers/cluster_controller_phases.go#L226-L230
	// +required
	// +kubebuilder:default=false
	//nolint:kubeapilinter // Preserve the previous API representation.
	Ready bool `json:"ready"`

	// initialized denotes whether or not the control plane has
	// provided a kubeadm-config.
	// Once this condition is marked true, its value is never changed. See the Ready condition for an indication of
	// the current readiness of the cluster's control plane.
	// This satisfies CAPI contract https://github.com/kubernetes-sigs/cluster-api/blob/cd3a694deac89d5ebeb888307deaa61487207aa0/controllers/cluster_controller_phases.go#L238-L252
	// +required
	// +kubebuilder:default=false
	//nolint:kubeapilinter // Preserve the previous API representation.
	Initialized bool `json:"initialized"`

	// externalManagedControlPlane indicates to cluster-api that the control plane
	// is managed by an external service.
	// https://github.com/kubernetes-sigs/cluster-api/blob/65e5385bffd71bf4aad3cf34a537f11b217c7fab/controllers/machine_controller.go#L468
	// +optional
	// +kubebuilder:default=true
	//nolint:kubeapilinter // Preserve the previous API representation.
	ExternalManagedControlPlane *bool `json:"externalManagedControlPlane,omitempty"`

	// controlPlaneEndpoint contains the endpoint information by which
	// external clients can access the control plane.  This is populated
	// after the infrastructure is ready.
	// +optional
	//nolint:kubeapilinter // Preserve the previous API representation.
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// oauthCallbackURLTemplate contains a template for the URL to use as a callback
	// for identity providers. The [identity-provider-name] placeholder must be replaced
	// with the name of an identity provider defined on the HostedCluster.
	// This is populated after the infrastructure is ready.
	// +optional
	// +kubebuilder:validation:MaxLength=255
	//nolint:kubeapilinter // Preserve the previous API representation.
	OAuthCallbackURLTemplate string `json:"oauthCallbackURLTemplate,omitempty"`

	// controlPlaneVersion tracks the rollout status of the control plane
	// components running on the management cluster, independently from
	// the data-plane version reported in the version field.
	// +optional
	ControlPlaneVersion ControlPlaneVersionStatus `json:"controlPlaneVersion,omitzero"`

	// versionStatus is the status of the release version applied by the
	// hosted control plane operator.
	// +optional
	//nolint:kubeapilinter // Preserve the previous API representation.
	VersionStatus *ClusterVersionStatus `json:"versionStatus,omitempty"`

	// version is the semantic version of the release applied by
	// the hosted control plane operator
	//
	// Deprecated: Use versionStatus.desired.version instead.
	// +optional
	// +kubebuilder:validation:MaxLength=255
	//nolint:kubeapilinter // Preserve the previous API representation.
	Version string `json:"version,omitempty"`

	// releaseImage is the release image applied to the hosted control plane.
	//
	// Deprecated: Use versionStatus.desired.image instead.
	// +optional
	// +kubebuilder:validation:MaxLength=255
	//nolint:kubeapilinter // Preserve the previous API representation.
	ReleaseImage string `json:"releaseImage,omitempty"`

	// lastReleaseImageTransitionTime is the time of the last update to the current
	// releaseImage property.
	//
	// Deprecated: Use versionStatus.history[0].startedTime instead.
	// +optional
	LastReleaseImageTransitionTime *metav1.Time `json:"lastReleaseImageTransitionTime,omitempty"`

	// kubeConfig is a reference to the secret containing the default kubeconfig
	// for this control plane.
	// +optional
	KubeConfig *KubeconfigSecretRef `json:"kubeConfig,omitempty"`

	// customKubeconfig references an external custom kubeconfig secret.
	// This field is populated in the status when a custom kubeconfig secret has been generated
	// for the hosted cluster. It contains the name and key of the secret located in the
	// hostedCluster namespace. This field is only populated when kubeApiExternalName is set.
	// If this field is removed during a day 2 operation, the referenced secret will be deleted
	// and this field will be removed from the hostedCluster status.
	// +optional
	CustomKubeconfig *KubeconfigSecretRef `json:"customKubeconfig,omitempty"`

	// kubeadminPassword is a reference to the secret containing the initial kubeadmin password
	// for the guest cluster.
	// +optional
	KubeadminPassword *corev1.LocalObjectReference `json:"kubeadminPassword,omitempty"`

	// platform contains platform-specific status of the HostedCluster
	// +optional
	//nolint:kubeapilinter // Preserve the previous API representation.
	Platform *PlatformStatus `json:"platform,omitempty"`

	// nodeCount tracks the number of nodes in the HostedControlPlane.
	// +optional
	//nolint:kubeapilinter // Preserve the previous API representation.
	NodeCount *int `json:"nodeCount,omitempty"`

	// autoNode contains the observed state of the autoNode (Karpenter) provisioner.
	// +optional
	AutoNode AutoNodeStatus `json:"autoNode,omitzero"`

	// configuration contains the cluster configuration status of the HostedCluster
	// +optional
	//nolint:kubeapilinter // Preserve the previous API representation.
	Configuration *ConfigurationStatus `json:"configuration,omitempty"`

	// secretEncryption tracks the state of secret encryption key rotation and re-encryption.
	// +optional
	SecretEncryption SecretEncryptionStatus `json:"secretEncryption,omitzero"`
	// initialization contains fields that track the status of the initialization of the HostedControlPlane.
	// +optional
	Initialization HostedControlPlaneInitializationStatus `json:"initialization,omitzero"`
}

func TestHostedControlPlaneStatusSerializationCompatibility(t *testing.T) {
	managed := true
	tests := map[string]struct {
		previous hostedControlPlaneStatusNMinus1
	}{
		"When previous status is zero, it should deserialize in new code without an endpoint": {},
		"When previous status is populated, it should preserve existing fields in new code": {
			previous: hostedControlPlaneStatusNMinus1{
				Ready:                       true,
				Initialized:                 true,
				ExternalManagedControlPlane: &managed,
				ControlPlaneEndpoint: APIEndpoint{
					Host: "api.cluster.example.com",
					Port: 6443,
				},
				KubeConfig: &KubeconfigSecretRef{
					Name: "admin-kubeconfig",
					Key:  "kubeconfig",
				},
				Version: "4.21.10",
				Conditions: []metav1.Condition{{
					Type:   "Available",
					Status: metav1.ConditionTrue,
					Reason: "AsExpected",
				}},
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			previous := tt.previous
			data, err := json.Marshal(previous)
			if err != nil {
				t.Fatal(err)
			}
			var current HostedControlPlaneStatus
			if err := json.Unmarshal(data, &current); err != nil {
				t.Fatal(err)
			}
			if current.SharedIngressWorkerAPIEndpoint != (SharedIngressWorkerAPIEndpoint{}) {
				t.Fatal("old data invented an endpoint")
			}
			for _, endpoint := range []SharedIngressWorkerAPIEndpoint{
				{},
				{IP: "10.0.0.1", Port: 6443},
				{Hostname: "router.example.com", Port: 6443},
			} {
				current.SharedIngressWorkerAPIEndpoint = endpoint
				encoded, err := json.Marshal(current)
				if err != nil {
					t.Fatal(err)
				}
				var rollback hostedControlPlaneStatusNMinus1
				if err := json.Unmarshal(encoded, &rollback); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(rollback, previous) {
					t.Fatalf("new-to-old lost existing status: got %#v, want %#v", rollback, previous)
				}
				// Old typed code ignores unknown fields. A full old-type rewrite
				// cannot preserve the new observation; producer must republish it.
				oldRewrite, err := json.Marshal(rollback)
				if err != nil {
					t.Fatal(err)
				}
				var reread HostedControlPlaneStatus
				if err := json.Unmarshal(oldRewrite, &reread); err != nil {
					t.Fatal(err)
				}
				if reread.SharedIngressWorkerAPIEndpoint != (SharedIngressWorkerAPIEndpoint{}) {
					t.Fatal("old rewrite unexpectedly preserved an unknown field")
				}
			}
		})
	}
}
