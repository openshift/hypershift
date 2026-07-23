package hcpstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestHCPStatusReconciler(t *testing.T) {
	t.Parallel()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-ns",
		},
	}

	expectedOAuthConfigMapName := "oauth-metadata-configmap"

	tests := []struct {
		name                 string
		hostedClusterObjects []crclient.Object
		expectError          bool
		expectedOAuthName    string
		validateConditions   func(g Gomega, hcp *hyperv1.HostedControlPlane)
	}{
		{
			name: "When Authentication resource exists it should propagate status to HCP",
			hostedClusterObjects: []crclient.Object{
				&configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}},
				&configv1.Authentication{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Status: configv1.AuthenticationStatus{
						IntegratedOAuthMetadata: configv1.ConfigMapNameReference{
							Name: expectedOAuthConfigMapName,
						},
					},
				},
			},
			expectedOAuthName: expectedOAuthConfigMapName,
		},
		{
			name: "When Authentication resource is missing it should return an error",
			hostedClusterObjects: []crclient.Object{
				&configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}},
			},
			expectError: true,
		},
		{
			name: "When ClusterVersion has conditions it should propagate them to HCP",
			hostedClusterObjects: []crclient.Object{
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{
								Type:    configv1.OperatorAvailable,
								Status:  configv1.ConditionTrue,
								Reason:  "AsExpected",
								Message: "cluster is available",
							},
							{
								Type:    configv1.OperatorProgressing,
								Status:  configv1.ConditionFalse,
								Reason:  "AsExpected",
								Message: "cluster is not progressing",
							},
						},
					},
				},
				&configv1.Authentication{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Status: configv1.AuthenticationStatus{
						IntegratedOAuthMetadata: configv1.ConfigMapNameReference{Name: expectedOAuthConfigMapName},
					},
				},
			},
			expectedOAuthName: expectedOAuthConfigMapName,
			validateConditions: func(g Gomega, hcp *hyperv1.HostedControlPlane) {
				availableCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionAvailable))
				g.Expect(availableCond).NotTo(BeNil())
				g.Expect(availableCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(availableCond.Reason).To(Equal("AsExpected"))

				progressingCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionProgressing))
				g.Expect(progressingCond).NotTo(BeNil())
				g.Expect(progressingCond.Status).To(Equal(metav1.ConditionFalse))
			},
		},
		{
			name: "When ClusterVersion has no Upgradeable condition it should default to True",
			hostedClusterObjects: []crclient.Object{
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{
								Type:   configv1.OperatorAvailable,
								Status: configv1.ConditionTrue,
								Reason: "AsExpected",
							},
						},
					},
				},
				&configv1.Authentication{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Status: configv1.AuthenticationStatus{
						IntegratedOAuthMetadata: configv1.ConfigMapNameReference{Name: expectedOAuthConfigMapName},
					},
				},
			},
			expectedOAuthName: expectedOAuthConfigMapName,
			validateConditions: func(g Gomega, hcp *hyperv1.HostedControlPlane) {
				upgradeableCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionUpgradeable))
				g.Expect(upgradeableCond).NotTo(BeNil())
				g.Expect(upgradeableCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(upgradeableCond.Reason).To(Equal(hyperv1.FromClusterVersionReason))
			},
		},
		{
			name: "When CVO condition has empty Reason it should use FromClusterVersionReason",
			hostedClusterObjects: []crclient.Object{
				&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{
								Type:    configv1.OperatorAvailable,
								Status:  configv1.ConditionTrue,
								Reason:  "",
								Message: "all good",
							},
						},
					},
				},
				&configv1.Authentication{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Status: configv1.AuthenticationStatus{
						IntegratedOAuthMetadata: configv1.ConfigMapNameReference{Name: expectedOAuthConfigMapName},
					},
				},
			},
			expectedOAuthName: expectedOAuthConfigMapName,
			validateConditions: func(g Gomega, hcp *hyperv1.HostedControlPlane) {
				availableCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionAvailable))
				g.Expect(availableCond).NotTo(BeNil())
				g.Expect(availableCond.Reason).To(Equal(hyperv1.FromClusterVersionReason))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			mgmtClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(hcp.DeepCopy()).
				WithStatusSubresource(&hyperv1.HostedControlPlane{}).
				Build()

			hostedClusterClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tt.hostedClusterObjects...).
				Build()

			reconciler := &hcpStatusReconciler{
				mgtClusterClient:    mgmtClient,
				hostedClusterClient: hostedClusterClient,
			}

			_, err := reconciler.Reconcile(t.Context(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      hcp.Name,
					Namespace: hcp.Namespace,
				},
			})

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("Authentication"),
					"error should be about missing Authentication resource, got: %v", err)
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			updatedHCP := &hyperv1.HostedControlPlane{}
			g.Expect(mgmtClient.Get(t.Context(), crclient.ObjectKeyFromObject(hcp), updatedHCP)).To(Succeed())
			g.Expect(updatedHCP.Status.Configuration).NotTo(BeNil())
			g.Expect(updatedHCP.Status.Configuration.Authentication.IntegratedOAuthMetadata.Name).To(Equal(tt.expectedOAuthName))

			if tt.validateConditions != nil {
				tt.validateConditions(g, updatedHCP)
			}
		})
	}
}

func TestReconcileConsoleURL(t *testing.T) {
	t.Parallel()

	baseObjects := func(extra ...crclient.Object) []crclient.Object {
		objs := []crclient.Object{
			&configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}},
			&configv1.Authentication{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
			},
		}
		return append(objs, extra...)
	}

	t.Run("When Console resource exists, it should set ConsoleURL on HCP status", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
			Spec:       hyperv1.HostedControlPlaneSpec{Capabilities: &hyperv1.Capabilities{}},
		}

		hostedClusterClient := fake.NewClientBuilder().
			WithScheme(api.Scheme).
			WithObjects(baseObjects(&configv1.Console{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Status:     configv1.ConsoleStatus{ConsoleURL: "https://console.example.com"},
			})...).
			Build()

		reconciler := &hcpStatusReconciler{
			hostedClusterClient: hostedClusterClient,
		}

		err := reconciler.reconcile(t.Context(), hcp)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(hcp.Status.ConsoleURL).To(Equal("https://console.example.com"))

		statusCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.DataPlaneStatusSynced))
		g.Expect(statusCond).NotTo(BeNil(), "DataPlaneStatusSynced condition should be set")
		g.Expect(statusCond.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(statusCond.Reason).To(Equal(hyperv1.AsExpectedReason))
	})

	t.Run("When Console resource is not found, it should clear ConsoleURL without error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
			Spec:       hyperv1.HostedControlPlaneSpec{Capabilities: &hyperv1.Capabilities{}},
			Status:     hyperv1.HostedControlPlaneStatus{ConsoleURL: "https://old.example.com"},
		}

		hostedClusterClient := fake.NewClientBuilder().
			WithScheme(api.Scheme).
			WithObjects(baseObjects()...).
			Build()

		reconciler := &hcpStatusReconciler{
			hostedClusterClient: hostedClusterClient,
		}

		err := reconciler.reconcile(t.Context(), hcp)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(hcp.Status.ConsoleURL).To(BeEmpty())
		statusCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.DataPlaneStatusSynced))
		g.Expect(statusCond).NotTo(BeNil(), "DataPlaneStatusSynced condition should be set even when Console is not found")
		g.Expect(statusCond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(statusCond.Message).To(ContainSubstring("Console resource not found"))
	})

	t.Run("When Console resource fetch returns a generic error, it should log and continue without blocking other updates", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
			Spec:       hyperv1.HostedControlPlaneSpec{Capabilities: &hyperv1.Capabilities{}},
			Status:     hyperv1.HostedControlPlaneStatus{ConsoleURL: "https://previous.example.com"},
		}

		hostedClusterClient := &erroringClient{
			Client:    fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(baseObjects()...).Build(),
			errorOn:   "Console",
			returnErr: fmt.Errorf("connection refused"),
		}

		reconciler := &hcpStatusReconciler{
			hostedClusterClient: hostedClusterClient,
		}

		err := reconciler.reconcile(t.Context(), hcp)
		g.Expect(err).NotTo(HaveOccurred(), "transient Console error should not fail the reconcile")
		g.Expect(hcp.Status.ConsoleURL).To(Equal("https://previous.example.com"),
			"ConsoleURL should remain unchanged when Console fetch fails")
		g.Expect(hcp.Status.Configuration).NotTo(BeNil(),
			"Authentication/Configuration should still be populated despite Console error")
	})

	t.Run("When Console capability is disabled, it should not update ConsoleURL even if Console resource exists", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
			Spec: hyperv1.HostedControlPlaneSpec{Capabilities: &hyperv1.Capabilities{
				Disabled: []hyperv1.OptionalCapability{hyperv1.ConsoleCapability},
			}},
			Status: hyperv1.HostedControlPlaneStatus{ConsoleURL: "https://existing.example.com"},
		}

		hostedClusterClient := fake.NewClientBuilder().
			WithScheme(api.Scheme).
			WithObjects(baseObjects(&configv1.Console{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Status:     configv1.ConsoleStatus{ConsoleURL: "https://different.example.com"},
			})...).
			Build()

		reconciler := &hcpStatusReconciler{
			hostedClusterClient: hostedClusterClient,
		}

		err := reconciler.reconcile(t.Context(), hcp)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(hcp.Status.ConsoleURL).To(Equal("https://existing.example.com"),
			"ConsoleURL should remain unchanged when Console capability is disabled")
		statusCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.DataPlaneStatusSynced))
		g.Expect(statusCond).NotTo(BeNil(), "DataPlaneStatusSynced condition should be set even when Console capability is disabled")
		g.Expect(statusCond.Status).To(Equal(metav1.ConditionTrue))
	})

	t.Run("When ConsoleURL exceeds MaxLength, it should not update ConsoleURL", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
			Spec:       hyperv1.HostedControlPlaneSpec{Capabilities: &hyperv1.Capabilities{}},
			Status:     hyperv1.HostedControlPlaneStatus{ConsoleURL: "https://previous.example.com"},
		}

		longURL := "https://console." + string(make([]byte, 4097))
		hostedClusterClient := fake.NewClientBuilder().
			WithScheme(api.Scheme).
			WithObjects(baseObjects(&configv1.Console{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Status:     configv1.ConsoleStatus{ConsoleURL: longURL},
			})...).
			Build()

		reconciler := &hcpStatusReconciler{
			hostedClusterClient: hostedClusterClient,
		}

		err := reconciler.reconcile(t.Context(), hcp)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(hcp.Status.ConsoleURL).To(Equal("https://previous.example.com"),
			"ConsoleURL should remain unchanged when value exceeds MaxLength")

		statusCond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.DataPlaneStatusSynced))
		g.Expect(statusCond).NotTo(BeNil(), "DataPlaneStatusSynced condition should be set")
		g.Expect(statusCond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(statusCond.Reason).To(Equal(hyperv1.DataPlaneStatusSyncFailedReason))
		g.Expect(statusCond.Message).To(ContainSubstring("ConsoleURL"))
	})
}

func TestBuildStatusPatch(t *testing.T) {
	t.Parallel()

	t.Run("When versionStatus changes, it should produce a replace op with optimistic lock", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test",
				Namespace:       "test-ns",
				ResourceVersion: "100",
			},
			Status: hyperv1.HostedControlPlaneStatus{
				VersionStatus: &hyperv1.ClusterVersionStatus{
					Desired: configv1.Release{Version: "4.16.0", Image: "quay.io/old:latest"},
				},
			},
		}
		original := hcp.DeepCopy()

		hcp.Status.VersionStatus = &hyperv1.ClusterVersionStatus{
			Desired: configv1.Release{Version: "4.17.0", Image: "quay.io/new:latest"},
		}

		patchBytes, err := buildStatusPatch(original, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		var ops []jsonPatchOp
		g.Expect(json.Unmarshal(patchBytes, &ops)).To(Succeed())

		g.Expect(ops).To(HaveLen(2))

		g.Expect(ops[0].Op).To(Equal("test"))
		g.Expect(ops[0].Path).To(Equal("/metadata/resourceVersion"))
		g.Expect(ops[0].Value).To(Equal("100"))

		g.Expect(ops[1].Op).To(Equal("replace"))
		g.Expect(ops[1].Path).To(Equal("/status/versionStatus"))
	})

	t.Run("When versionStatus is added for the first time, it should produce an add op", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test",
				Namespace:       "test-ns",
				ResourceVersion: "100",
			},
		}
		original := hcp.DeepCopy()

		hcp.Status.VersionStatus = &hyperv1.ClusterVersionStatus{
			Desired: configv1.Release{Version: "4.17.0", Image: "quay.io/test:latest"},
		}

		patchBytes, err := buildStatusPatch(original, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		var ops []jsonPatchOp
		g.Expect(json.Unmarshal(patchBytes, &ops)).To(Succeed())

		g.Expect(ops).To(HaveLen(2))
		g.Expect(ops[1].Op).To(Equal("add"))
		g.Expect(ops[1].Path).To(Equal("/status/versionStatus"))
	})

	t.Run("When nothing changes, it should produce only the test op", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test",
				Namespace:       "test-ns",
				ResourceVersion: "100",
			},
			Status: hyperv1.HostedControlPlaneStatus{
				Conditions: []metav1.Condition{
					{Type: "ConditionA", Status: metav1.ConditionTrue, Reason: "OK"},
				},
			},
		}
		original := hcp.DeepCopy()

		patchBytes, err := buildStatusPatch(original, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		var ops []jsonPatchOp
		g.Expect(json.Unmarshal(patchBytes, &ops)).To(Succeed())

		g.Expect(ops).To(HaveLen(1))
		g.Expect(ops[0].Op).To(Equal("test"))
		g.Expect(ops[0].Path).To(Equal("/metadata/resourceVersion"))
	})

	t.Run("When conditions change, it should replace the entire conditions array", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test",
				Namespace:       "test-ns",
				ResourceVersion: "100",
			},
			Status: hyperv1.HostedControlPlaneStatus{
				Conditions: []metav1.Condition{
					{Type: "ConditionA", Status: metav1.ConditionTrue, Reason: "OK"},
					{Type: "ConditionB", Status: metav1.ConditionTrue, Reason: "OK"},
				},
			},
		}
		original := hcp.DeepCopy()

		meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
			Type:   "ConditionA",
			Status: metav1.ConditionFalse,
			Reason: "NowBad",
		})

		patchBytes, err := buildStatusPatch(original, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		var ops []jsonPatchOp
		g.Expect(json.Unmarshal(patchBytes, &ops)).To(Succeed())

		g.Expect(ops).To(HaveLen(2))
		g.Expect(ops[1].Op).To(Equal("replace"))
		g.Expect(ops[1].Path).To(Equal("/status/conditions"))

		conditionsJSON, err := json.Marshal(ops[1].Value)
		g.Expect(err).ToNot(HaveOccurred())
		var conditions []metav1.Condition
		g.Expect(json.Unmarshal(conditionsJSON, &conditions)).To(Succeed())
		g.Expect(conditions).To(HaveLen(2), "all conditions from the read should be in the patch")
	})

	t.Run("When versionStatus has nil availableUpdates and nil completionTime, the patch should preserve null values", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test",
				Namespace:       "test-ns",
				ResourceVersion: "100",
			},
		}
		original := hcp.DeepCopy()

		startedTime := metav1.Now()
		hcp.Status.VersionStatus = &hyperv1.ClusterVersionStatus{
			Desired: configv1.Release{Version: "4.17.0", Image: "quay.io/test:latest"},
			History: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: startedTime,
					// CompletionTime is nil — update in progress
					Version: "4.17.0",
					Image:   "quay.io/test:latest",
				},
			},
			// AvailableUpdates is nil — CVO hasn't checked yet
		}

		patchBytes, err := buildStatusPatch(original, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		// Parse the raw JSON to verify null handling.
		// JSON Patch (RFC 6902) uses "value": null to mean "set to null",
		// unlike JSON Merge Patch (RFC 7386) where null means "delete".
		var rawOps []json.RawMessage
		g.Expect(json.Unmarshal(patchBytes, &rawOps)).To(Succeed())
		g.Expect(rawOps).To(HaveLen(2))

		// Parse the versionStatus op's value to check null fields
		var op struct {
			Op    string          `json:"op"`
			Path  string          `json:"path"`
			Value json.RawMessage `json:"value"`
		}
		g.Expect(json.Unmarshal(rawOps[1], &op)).To(Succeed())
		g.Expect(op.Op).To(Equal("add"))
		g.Expect(op.Path).To(Equal("/status/versionStatus"))

		var vs map[string]interface{}
		g.Expect(json.Unmarshal(op.Value, &vs)).To(Succeed())

		// availableUpdates should be null (nil slice serializes as null with no omitempty)
		au, ok := vs["availableUpdates"]
		g.Expect(ok).To(BeTrue(), "availableUpdates key must be present in the patch")
		g.Expect(au).To(BeNil(), "availableUpdates should be null — JSON Patch preserves this correctly")

		// completionTime in history should be null (nil *metav1.Time)
		history := vs["history"].([]interface{})
		entry := history[0].(map[string]interface{})
		ct, ok := entry["completionTime"]
		g.Expect(ok).To(BeTrue(), "completionTime key must be present in the patch")
		g.Expect(ct).To(BeNil(), "completionTime should be null — JSON Patch preserves this correctly")
	})

	t.Run("When configuration changes, it should produce a replace op", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test",
				Namespace:       "test-ns",
				ResourceVersion: "100",
			},
			Status: hyperv1.HostedControlPlaneStatus{
				Configuration: &hyperv1.ConfigurationStatus{},
			},
		}
		original := hcp.DeepCopy()

		hcp.Status.Configuration = &hyperv1.ConfigurationStatus{
			Authentication: configv1.AuthenticationStatus{
				IntegratedOAuthMetadata: configv1.ConfigMapNameReference{Name: "oauth-metadata"},
			},
		}

		patchBytes, err := buildStatusPatch(original, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		var ops []jsonPatchOp
		g.Expect(json.Unmarshal(patchBytes, &ops)).To(Succeed())

		g.Expect(ops).To(HaveLen(2))
		g.Expect(ops[1].Op).To(Equal("replace"))
		g.Expect(ops[1].Path).To(Equal("/status/configuration"))
	})

	t.Run("When versionStatus is removed, it should produce a remove op", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test",
				Namespace:       "test-ns",
				ResourceVersion: "100",
			},
			Status: hyperv1.HostedControlPlaneStatus{
				VersionStatus: &hyperv1.ClusterVersionStatus{
					Desired: configv1.Release{Version: "4.17.0", Image: "quay.io/test:latest"},
				},
			},
		}
		original := hcp.DeepCopy()

		hcp.Status.VersionStatus = nil

		patchBytes, err := buildStatusPatch(original, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		var ops []jsonPatchOp
		g.Expect(json.Unmarshal(patchBytes, &ops)).To(Succeed())

		g.Expect(ops).To(HaveLen(2))
		g.Expect(ops[0].Op).To(Equal("test"))
		g.Expect(ops[1].Op).To(Equal("remove"))
		g.Expect(ops[1].Path).To(Equal("/status/versionStatus"))
	})

	t.Run("When multiple fields change, it should produce ops for each changed field", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test",
				Namespace:       "test-ns",
				ResourceVersion: "100",
			},
			Status: hyperv1.HostedControlPlaneStatus{
				VersionStatus: &hyperv1.ClusterVersionStatus{
					Desired: configv1.Release{Version: "4.16.0"},
				},
				Conditions: []metav1.Condition{
					{Type: "ConditionA", Status: metav1.ConditionTrue, Reason: "OK"},
				},
				Configuration: &hyperv1.ConfigurationStatus{},
				Version:       "4.16.0",
				ReleaseImage:  "quay.io/old:latest",
			},
		}
		original := hcp.DeepCopy()

		hcp.Status.VersionStatus.Desired.Version = "4.17.0"
		meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
			Type: "ConditionA", Status: metav1.ConditionFalse, Reason: "Bad",
		})
		hcp.Status.Configuration = &hyperv1.ConfigurationStatus{
			Authentication: configv1.AuthenticationStatus{
				IntegratedOAuthMetadata: configv1.ConfigMapNameReference{Name: "new"},
			},
		}
		hcp.Status.Version = "4.17.0"
		hcp.Status.ReleaseImage = "quay.io/new:latest"

		patchBytes, err := buildStatusPatch(original, hcp)
		g.Expect(err).ToNot(HaveOccurred())

		var ops []jsonPatchOp
		g.Expect(json.Unmarshal(patchBytes, &ops)).To(Succeed())

		// test + versionStatus + conditions + configuration + version + releaseImage
		g.Expect(ops).To(HaveLen(6))

		paths := make([]string, len(ops))
		for i, op := range ops {
			paths[i] = op.Path
		}
		g.Expect(paths).To(ContainElements(
			"/metadata/resourceVersion",
			"/status/versionStatus",
			"/status/conditions",
			"/status/configuration",
			"/status/version",
			"/status/releaseImage",
		))
	})
}

// erroringClient wraps a crclient.Client and returns a specific error when
// Get is called for an object whose kind name contains the errorOn string.
type erroringClient struct {
	crclient.Client
	errorOn   string
	returnErr error
}

func (e *erroringClient) Get(ctx context.Context, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
	if reflect.TypeOf(obj).Elem().Name() == e.errorOn {
		return e.returnErr
	}
	return e.Client.Get(ctx, key, obj, opts...)
}
