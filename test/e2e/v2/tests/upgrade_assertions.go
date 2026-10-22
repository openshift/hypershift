//go:build e2ev2

package tests

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ExpectHostedClusterUpgradeToComplete waits for the HostedCluster's control
// plane and overall version status to report the expected release image and a
// completed update. It polls every 10 seconds for up to 30 minutes and fails
// the current Ginkgo assertion if the upgrade does not complete.
func ExpectHostedClusterUpgradeToComplete(
	ctx context.Context,
	client crclient.Client,
	hc *hyperv1.HostedCluster,
	image string,
) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		ExpectHostedClusterUpgradeComplete(ctx, g, client, hc, image)
	}).
		WithContext(ctx).
		WithTimeout(30 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())
}

// ExpectHostedClusterUpgradeComplete performs one check that the HostedCluster
// reports the expected release image and completed update in both its control
// plane and overall version status. The supplied Gomega instance must be used
// for assertions so this function can be called from Eventually.
func ExpectHostedClusterUpgradeComplete(ctx context.Context, g Gomega, mgmtClient crclient.Client, hc *hyperv1.HostedCluster, image string) {
	GinkgoHelper()

	if !g.Expect(hc).NotTo(BeNil(), "HostedCluster input cannot be nil") {
		return
	}

	currentHC := &hyperv1.HostedCluster{}
	if err := mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), currentHC); err != nil {
		g.Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster %s/%s", hc.Namespace, hc.Name)
		return
	}

	g.Expect(currentHC.Status.ControlPlaneVersion.Desired.Image).To(Equal(image))
	if len(currentHC.Status.ControlPlaneVersion.History) == 0 {
		g.Expect(currentHC.Status.ControlPlaneVersion.History).NotTo(BeEmpty())
		return
	}
	g.Expect(currentHC.Status.ControlPlaneVersion.History[0].State).To(Equal(configv1.CompletedUpdate))

	if currentHC.Status.Version == nil {
		g.Expect(currentHC.Status.Version).NotTo(BeNil())
		return
	}
	g.Expect(currentHC.Status.Version.Desired.Image).To(Equal(image))
	if len(currentHC.Status.Version.History) == 0 {
		g.Expect(currentHC.Status.Version.History).NotTo(BeEmpty())
		return
	}
	g.Expect(currentHC.Status.Version.History[0].State).To(Equal(configv1.CompletedUpdate))
}

// ExpectNoResourceDeletesInProgress verifies that the hosted cluster
// ClusterVersion does not have Upgradeable=False with reason
// ResourceDeletesInProgress. This catches regressions where CVO resource
// deletions get stuck and block subsequent upgrades (OCPBUGS-130965).
// Other reasons for Upgradeable=False (e.g. cloud-credential annotation) are
// not checked since they are expected behavior.
func ExpectNoResourceDeletesInProgress(ctx context.Context, hostedClusterClient crclient.Client) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		cv := &configv1.ClusterVersion{}
		g.Expect(hostedClusterClient.Get(ctx, crclient.ObjectKey{Name: "version"}, cv)).To(Succeed(),
			"failed to get hosted cluster ClusterVersion")

		for _, cond := range cv.Status.Conditions {
			if cond.Type == "Upgradeable" {
				g.Expect(cond.Reason).NotTo(Equal("ResourceDeletesInProgress"),
					"hosted cluster ClusterVersion has ResourceDeletesInProgress: %s",
					cond.Message)
				// Also check MultipleReasons which may wrap ResourceDeletesInProgress
				if cond.Reason == "MultipleReasons" {
					g.Expect(cond.Message).NotTo(ContainSubstring("resource deletions are in progress"),
						"hosted cluster ClusterVersion has ResourceDeletesInProgress in MultipleReasons: %s",
						cond.Message)
				}
				break
			}
		}
	}).
		WithContext(ctx).
		WithTimeout(30 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())
}
