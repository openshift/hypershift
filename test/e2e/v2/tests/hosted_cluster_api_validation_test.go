//go:build e2ev2

package tests

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

func RegisterHostedClusterAPIValidationTests(getTestCtx internal.TestContextGetter) {
	It("should reject changes to API service publishing strategy", func() {
		tc := getTestCtx()
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		if !hostedClusterServicesImmutable(hc.Spec.Platform.Type) {
			Skip("Services may be changed on IBMCloud")
		}
		originalServices := hc.DeepCopy().Spec.Services
		found := false
		for index := range hc.Spec.Services {
			service := &hc.Spec.Services[index]
			if service.Service != hyperv1.APIServer {
				continue
			}
			found = true
			if service.Type == hyperv1.NodePort {
				service.Type = hyperv1.LoadBalancer
			} else {
				service.Type = hyperv1.NodePort
			}
		}
		Expect(found).To(BeTrue(), "hosted cluster must publish the API service")
		err = tc.MgmtClient.Update(tc.Context, hc, crclient.DryRunAll)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected validation rejection, got %v", err)
		Expect(err.Error()).To(ContainSubstring("Services is immutable"))
		Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), hc)).To(Succeed())
		Expect(hc.Spec.Services).To(Equal(originalServices), "rejected mutations must not change stored service configuration")
	})
	It("should reject changes to controller availability policy", func() {
		tc := getTestCtx()
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		originalPolicy := hc.Spec.ControllerAvailabilityPolicy
		if hc.Spec.ControllerAvailabilityPolicy == hyperv1.HighlyAvailable {
			hc.Spec.ControllerAvailabilityPolicy = hyperv1.SingleReplica
		} else {
			hc.Spec.ControllerAvailabilityPolicy = hyperv1.HighlyAvailable
		}
		err = tc.MgmtClient.Update(tc.Context, hc, crclient.DryRunAll)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected validation rejection, got %v", err)
		Expect(err.Error()).To(ContainSubstring("ControllerAvailabilityPolicy is immutable"))
		Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), hc)).To(Succeed())
		Expect(hc.Spec.ControllerAvailabilityPolicy).To(Equal(originalPolicy))
	})
	It("should reject changes to capabilities", func() {
		tc := getTestCtx()
		skipIfNativeOriginBelow(tc, semver.MustParse("4.19.0"))
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		originalCapabilities := hc.Spec.Capabilities.DeepCopy()
		if hc.Spec.Capabilities != nil && len(hc.Spec.Capabilities.Disabled) > 0 {
			hc.Spec.Capabilities = &hyperv1.Capabilities{}
		} else {
			hc.Spec.Capabilities = &hyperv1.Capabilities{Disabled: []hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability}}
		}
		err = tc.MgmtClient.Update(tc.Context, hc, crclient.DryRunAll)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected validation rejection, got %v", err)
		Expect(err.Error()).To(ContainSubstring("Capabilities is immutable"))
		Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), hc)).To(Succeed())
		Expect(hc.Spec.Capabilities).To(Equal(originalCapabilities))
	})
}

func hostedClusterServicesImmutable(platform hyperv1.PlatformType) bool {
	return platform != hyperv1.IBMCloudPlatform
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:APIValidation] Hosted Cluster API Validation", Label("hosted-cluster-api-validation"), func() {
	RegisterHostedClusterAPIValidationTests(internal.GetTestContext)
})
