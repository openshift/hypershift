//go:build envtest

package envtest

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// This fixture is the unmodified Hypershift-CustomNoUpgrade Infrastructure CRD
// from github.com/openshift/api v0.0.0-20260820183036-3db6c4b03286, the
// version pinned in the root go.mod. Keep it in sync when that module changes.
const guestInfrastructureCRD = "guest-infrastructures-Hypershift-CustomNoUpgrade.crd.yaml"

var _ = Describe("Guest Infrastructure GCP resourceTags lifecycle", func() {
	It("When GCP tags are written with initial platform status, it should accept them and reject later changes", func() {
		server, err := discovery.NewDiscoveryClientForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())
		version, err := server.ServerVersion()
		Expect(err).NotTo(HaveOccurred())
		minor, err := strconv.Atoi(strings.Split(version.Minor, "+")[0])
		Expect(err).NotTo(HaveOccurred())
		if minor < 31 {
			Skip("The pinned current Infrastructure CRD uses universeDomain CEL unavailable before Kubernetes 1.31")
		}

		crd, err := loadCRDFromFile(filepath.Join("testdata", guestInfrastructureCRD))
		Expect(err).NotTo(HaveOccurred())
		if err := k8sClient.Create(ctx, crd); err != nil {
			// Vanilla Kubernetes 1.31.0 does not yet have the CEL format
			// library used by this pinned, newer CRD's universeDomain field.
			// OpenShift's 1.31.2 envtest binary accepts the same CRD.
			if minor == 31 && strings.Contains(err.Error(), "universeDomain") && strings.Contains(err.Error(), "undeclared reference to 'format'") {
				Skip("The pinned current Infrastructure CRD uses universeDomain CEL unavailable in this Kubernetes 1.31 API server")
			}
			Fail(err.Error())
		}
		DeferCleanup(func() {
			Expect(k8sClient.Delete(context.Background(), crd)).To(Succeed())
		})
		Eventually(func(g Gomega) {
			current := crd.DeepCopy()
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(crd), current)).To(Succeed())
			g.Expect(current.Status.Conditions).To(ContainElement(And(
				HaveField("Type", apiextensionsv1.Established), HaveField("Status", apiextensionsv1.ConditionTrue),
			)))
		}, "30s", "100ms").Should(Succeed())

		scheme := runtime.NewScheme()
		Expect(configv1.AddToScheme(scheme)).To(Succeed())
		guestClient, err := client.New(cfg, client.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred())

		infra := &configv1.Infrastructure{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
		Expect(guestClient.Create(ctx, infra)).To(Succeed())
		infra.Status.Platform = configv1.GCPPlatformType
		infra.Status.ControlPlaneTopology = configv1.ExternalTopologyMode
		infra.Status.PlatformStatus = &configv1.PlatformStatus{
			Type: configv1.GCPPlatformType,
			GCP: &configv1.GCPPlatformStatus{
				ProjectID: "customer-project",
				Region:    "us-central1",
				ResourceTags: []configv1.GCPResourceTag{
					{ParentID: "customer-project", Key: "environment", Value: "production"},
					{ParentID: "123456789012", Key: "environment", Value: "shared"},
				},
			},
		}
		By("Rejecting duplicate short keys from different parents in the initial status co-write")
		err = guestClient.Status().Update(ctx, infra)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected an invalid duplicate-key status update, got %v", err)
		cause, found := apierrors.StatusCause(err, metav1.CauseTypeFieldValueDuplicate)
		Expect(found).To(BeTrue())
		Expect(cause.Field).To(Equal("status.platformStatus.gcp.resourceTags[1]"))
		Expect(cause.Message).To(ContainSubstring(`{"key":"environment"}`))

		By("Accepting a project tag and an organization tag with distinct short keys")
		infra.Status.PlatformStatus.GCP.ResourceTags[1].Key = "cost-center"
		Expect(guestClient.Status().Update(ctx, infra)).To(Succeed())

		unchanged := &configv1.Infrastructure{}
		Expect(guestClient.Get(ctx, client.ObjectKeyFromObject(infra), unchanged)).To(Succeed())
		Expect(unchanged.Status.PlatformStatus.GCP.ResourceTags).To(Equal(infra.Status.PlatformStatus.GCP.ResourceTags))
		Expect(guestClient.Status().Update(ctx, unchanged)).To(Succeed())

		for _, change := range []struct {
			name          string
			tags          []configv1.GCPResourceTag
			expectedError string
		}{
			{"When a tag is added, it should reject the update", []configv1.GCPResourceTag{
				{ParentID: "customer-project", Key: "environment", Value: "production"},
				{ParentID: "123456789012", Key: "cost-center", Value: "shared"},
				{ParentID: "customer-project", Key: "team", Value: "platform"},
			}, "resourceTags are immutable"},
			{"When a tag value changes, it should reject the update", []configv1.GCPResourceTag{
				{ParentID: "customer-project", Key: "environment", Value: "staging"},
				{ParentID: "123456789012", Key: "cost-center", Value: "shared"},
			}, "resourceTags are immutable"},
			{"When a tag is removed, it should reject the update", nil, "resourceTags may only be configured during installation"},
		} {
			By(change.name)
			current := &configv1.Infrastructure{}
			Expect(guestClient.Get(ctx, client.ObjectKeyFromObject(infra), current)).To(Succeed())
			current.Status.PlatformStatus.GCP.ResourceTags = change.tags
			Expect(guestClient.Status().Update(ctx, current)).To(MatchError(ContainSubstring(change.expectedError)))
		}

		// A second create shows why tags must be included when GCP status first
		// materializes: the guest API rejects absent-to-present transitions.
		Expect(guestClient.Delete(ctx, infra)).To(Succeed())
		Eventually(func() bool {
			err := guestClient.Get(ctx, client.ObjectKeyFromObject(infra), &configv1.Infrastructure{})
			return apierrors.IsNotFound(err)
		}, "30s", "100ms").Should(BeTrue())
		withoutTags := &configv1.Infrastructure{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
		Expect(guestClient.Create(ctx, withoutTags)).To(Succeed())
		withoutTags.Status.Platform = configv1.GCPPlatformType
		withoutTags.Status.ControlPlaneTopology = configv1.ExternalTopologyMode
		withoutTags.Status.PlatformStatus = &configv1.PlatformStatus{
			Type: configv1.GCPPlatformType,
			GCP: &configv1.GCPPlatformStatus{
				ProjectID: "customer-project",
				Region:    "us-central1",
			},
		}
		Expect(guestClient.Status().Update(ctx, withoutTags)).To(Succeed())
		withoutTags.Status.PlatformStatus.GCP.ResourceTags = []configv1.GCPResourceTag{
			{ParentID: "customer-project", Key: "environment", Value: "production"},
		}
		Expect(guestClient.Status().Update(ctx, withoutTags)).To(MatchError(ContainSubstring("resourceTags may only be configured during installation")))
	})
})
