package hostedcluster

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	platformgcp "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/internal/platform/gcp"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/conditions"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestReconcileGCPCredentialConditions(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		hcVersion, hcpVersion              string
		initialStatus, runtimeStatus       metav1.ConditionStatus
		expectedStatus, expectedHealth     metav1.ConditionStatus
		expectedCredentials                platformgcp.CredentialStatus
		missingHCP, deleting, patchFailure bool
		conflictingWrite                   bool
		expectedPatches                    int
	}{
		{
			name:                "When HCP upgrades to 5.1, it should publish the version and successful validation together",
			hcVersion:           "5.0.0",
			hcpVersion:          "5.1.0-0.ci-20260909",
			runtimeStatus:       metav1.ConditionTrue,
			expectedStatus:      metav1.ConditionTrue,
			expectedHealth:      metav1.ConditionTrue,
			expectedCredentials: platformgcp.CredentialStatusValid,
			expectedPatches:     1,
		},
		{
			name:                "When HCP is 5.0 and HC is stale 5.1, it should publish unavailable validation with version 5.0",
			hcVersion:           "5.1.0",
			hcpVersion:          "5.0.0",
			initialStatus:       metav1.ConditionTrue,
			expectedStatus:      metav1.ConditionUnknown,
			expectedHealth:      metav1.ConditionUnknown,
			expectedCredentials: platformgcp.CredentialStatusUnknown,
			expectedPatches:     1,
		},
		{
			name:                "When only the control plane version changes, it should still persist the new version",
			hcVersion:           "5.1.0",
			hcpVersion:          "5.2.0",
			initialStatus:       metav1.ConditionTrue,
			runtimeStatus:       metav1.ConditionTrue,
			expectedStatus:      metav1.ConditionTrue,
			expectedHealth:      metav1.ConditionTrue,
			expectedCredentials: platformgcp.CredentialStatusValid,
			expectedPatches:     1,
		},
		{
			name:                "When status is unchanged, it should avoid a status patch",
			hcVersion:           "5.1.0",
			hcpVersion:          "5.1.0",
			initialStatus:       metav1.ConditionTrue,
			runtimeStatus:       metav1.ConditionTrue,
			expectedStatus:      metav1.ConditionTrue,
			expectedHealth:      metav1.ConditionTrue,
			expectedCredentials: platformgcp.CredentialStatusValid,
		},
		{
			name:                "When deleting with a stale HC version, it should publish the runtime failure and version together",
			hcVersion:           "5.0.0",
			hcpVersion:          "5.1.0",
			runtimeStatus:       metav1.ConditionFalse,
			deleting:            true,
			expectedStatus:      metav1.ConditionFalse,
			expectedHealth:      metav1.ConditionTrue,
			expectedCredentials: platformgcp.CredentialStatusInvalid,
			expectedPatches:     1,
		},
		{
			name:                "When HCP disappears during deletion, it should preserve the persisted version and runtime failure",
			hcVersion:           "5.1.0",
			initialStatus:       metav1.ConditionFalse,
			missingHCP:          true,
			deleting:            true,
			expectedStatus:      metav1.ConditionFalse,
			expectedHealth:      metav1.ConditionTrue,
			expectedCredentials: platformgcp.CredentialStatusInvalid,
		},
		{
			name:                "When HCP disappears with persisted version 5.0, it should disable cleanup despite stale failures",
			hcVersion:           "5.0.0",
			initialStatus:       metav1.ConditionFalse,
			missingHCP:          true,
			deleting:            true,
			expectedStatus:      metav1.ConditionUnknown,
			expectedHealth:      metav1.ConditionUnknown,
			expectedCredentials: platformgcp.CredentialStatusUnknown,
			expectedPatches:     1,
		},
		{
			name:                "When version and HCP are missing, it should report unknown without relaxing health expectations",
			missingHCP:          true,
			expectedStatus:      metav1.ConditionUnknown,
			expectedHealth:      metav1.ConditionTrue,
			expectedCredentials: platformgcp.CredentialStatusUnknown,
			expectedPatches:     1,
		},
		{
			name:                "When HCP version is empty and HC version is stale, it should persist the undetermined version",
			hcVersion:           "5.0.0",
			initialStatus:       metav1.ConditionTrue,
			expectedStatus:      metav1.ConditionUnknown,
			expectedHealth:      metav1.ConditionTrue,
			expectedCredentials: platformgcp.CredentialStatusUnknown,
			expectedPatches:     1,
		},
		{
			name:                "When HCP version is malformed, it should report unknown without relaxing health expectations",
			hcVersion:           "5.0.0",
			hcpVersion:          "malformed",
			initialStatus:       metav1.ConditionTrue,
			expectedStatus:      metav1.ConditionUnknown,
			expectedHealth:      metav1.ConditionTrue,
			expectedCredentials: platformgcp.CredentialStatusUnknown,
			expectedPatches:     1,
		},
		{
			name:                "When another writer changes status, it should retry and preserve the unrelated condition",
			hcVersion:           "5.0.0",
			hcpVersion:          "5.1.0",
			runtimeStatus:       metav1.ConditionTrue,
			expectedStatus:      metav1.ConditionTrue,
			expectedHealth:      metav1.ConditionTrue,
			expectedCredentials: platformgcp.CredentialStatusValid,
			conflictingWrite:    true,
			expectedPatches:     2,
		},
		{
			name:                "When the status patch fails, it should return the error without publishing partial status",
			hcVersion:           "5.0.0",
			hcpVersion:          "5.1.0",
			runtimeStatus:       metav1.ConditionTrue,
			expectedStatus:      metav1.ConditionTrue,
			expectedHealth:      metav1.ConditionTrue,
			expectedCredentials: platformgcp.CredentialStatusValid,
			patchFailure:        true,
			expectedPatches:     1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "gcp", Namespace: "clusters", Generation: 3}}
			hc.Spec.Platform.Type = hyperv1.GCPPlatform
			hc.Status.ControlPlaneVersion.Desired.Version = tc.hcVersion
			if tc.deleting {
				hc.DeletionTimestamp = ptr.To(metav1.Now())
				hc.Finalizers = []string{"hypershift.openshift.io/finalizer"}
			}
			hcp := &hyperv1.HostedControlPlane{}
			hcp.Status.ControlPlaneVersion.Desired.Version = tc.hcpVersion
			for _, conditionType := range []hyperv1.ConditionType{hyperv1.ValidGCPWorkloadIdentity, hyperv1.ValidGCPCredentials} {
				for _, result := range []struct {
					conditions *[]metav1.Condition
					status     metav1.ConditionStatus
				}{
					{&hc.Status.Conditions, tc.initialStatus},
					{&hcp.Status.Conditions, tc.runtimeStatus},
				} {
					if result.status == "" {
						continue
					}
					reason := hyperv1.AsExpectedReason
					if result.status == metav1.ConditionFalse {
						reason = hyperv1.InvalidIdentityProvider
					}
					meta.SetStatusCondition(result.conditions, metav1.Condition{Type: string(conditionType), Status: result.status, Reason: reason, ObservedGeneration: hc.Generation})
				}
			}
			expectedVersion := tc.hcpVersion
			if tc.missingHCP {
				hcp = nil
				expectedVersion = tc.hcVersion
			}
			patches := 0
			patchError := errors.New("status patch failed")
			unrelatedCondition := metav1.Condition{
				Type:               string(hyperv1.HostedClusterAvailable),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				ObservedGeneration: hc.Generation,
				LastTransitionTime: metav1.NewTime(time.Unix(1700000000, 0)),
			}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(ctx context.Context, c client.Client, subResource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						patches++
						g.Expect(subResource).To(Equal("status"))
						patchData, err := patch.Data(obj)
						g.Expect(err).NotTo(HaveOccurred())
						g.Expect(string(patchData)).To(ContainSubstring("\"resourceVersion\""))
						published := obj.(*hyperv1.HostedCluster)
						g.Expect(published.Status.ControlPlaneVersion.Desired.Version).To(Equal(expectedVersion))
						for _, conditionType := range []hyperv1.ConditionType{hyperv1.ValidGCPWorkloadIdentity, hyperv1.ValidGCPCredentials} {
							g.Expect(meta.FindStatusCondition(published.Status.Conditions, string(conditionType)).Status).To(Equal(tc.expectedStatus))
						}
						if tc.patchFailure {
							return patchError
						}
						if tc.conflictingWrite && patches == 1 {
							latest := &hyperv1.HostedCluster{}
							g.Expect(c.Get(ctx, client.ObjectKeyFromObject(obj), latest)).To(Succeed())
							meta.SetStatusCondition(&latest.Status.Conditions, unrelatedCondition)
							g.Expect(c.Status().Update(ctx, latest)).To(Succeed())
							err := c.SubResource(subResource).Patch(ctx, obj, patch, opts...)
							g.Expect(apierrors.IsConflict(err)).To(BeTrue(), "a stale patch must conflict")
							return err
						}
						return c.SubResource(subResource).Patch(ctx, obj, patch, opts...)
					},
				}).Build()
			r := &HostedClusterReconciler{Client: c}
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
			before := hc.DeepCopy()
			err := r.reconcileGCPCredentialConditions(t.Context(), hc, hcp)
			g.Expect(patches).To(Equal(tc.expectedPatches))
			persisted := &hyperv1.HostedCluster{}
			g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), persisted)).To(Succeed())
			if tc.patchFailure {
				g.Expect(err).To(MatchError(ContainSubstring("failed to update GCP credential status")))
				g.Expect(errors.Is(err, patchError)).To(BeTrue())
				g.Expect(persisted.Status).To(Equal(before.Status))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			if tc.conflictingWrite {
				g.Expect(meta.FindStatusCondition(persisted.Status.Conditions, unrelatedCondition.Type)).To(Equal(&unrelatedCondition))
			}
			g.Expect(persisted.Status.ControlPlaneVersion.Desired.Version).To(Equal(expectedVersion))
			for _, conditionType := range []hyperv1.ConditionType{hyperv1.ValidGCPWorkloadIdentity, hyperv1.ValidGCPCredentials} {
				condition := meta.FindStatusCondition(persisted.Status.Conditions, string(conditionType))
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(tc.expectedStatus))
				g.Expect(conditions.ExpectedHCConditions(persisted)[conditionType]).To(Equal(tc.expectedHealth))
			}
			g.Expect(platformgcp.GetCredentialStatus(persisted)).To(Equal(tc.expectedCredentials))
			g.Expect(r.reconcileGCPCredentialConditions(t.Context(), persisted, hcp)).To(Succeed())
			g.Expect(patches).To(Equal(tc.expectedPatches), "unchanged reconciliation must not write status again")
		})
	}
	t.Run("When the HostedCluster cannot be fetched, it should return the error", func(t *testing.T) {
		g := NewWithT(t)
		hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "gcp", Namespace: "clusters"}}
		c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).Build()
		r := &HostedClusterReconciler{Client: c}
		err := r.reconcileGCPCredentialConditions(t.Context(), hc, nil)
		g.Expect(err).To(MatchError(ContainSubstring("failed to update GCP credential status")))
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
}
