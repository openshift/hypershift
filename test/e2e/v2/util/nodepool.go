package util

import (
	"context"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
	"github.com/onsi/gomega"
)

// WaitForNodePoolVersion waits until the NodePool status.version reports the
// wanted payload version.
//
// On the CAPI path status.version is only written once the MachineDeployment
// (or the in-place rollout) for the NodePool reports complete, which can lag
// behind the nodes themselves becoming Ready. Comparing against the exact
// payload version is race-free: it observes the value it asserts rather than a
// condition that may have been computed before the spec change. It is also safe
// for in-place upgrades, where a failed rollout leaves status.version at the old
// value.
func WaitForNodePoolVersion(g gomega.Gomega, ctx context.Context, client crclient.Client, key crclient.ObjectKey, want semver.Version, timeout, pollInterval time.Duration) {
	g.Eventually(func(inner gomega.Gomega) {
		nodePool := &hyperv1.NodePool{}
		inner.Expect(client.Get(ctx, key, nodePool)).To(gomega.Succeed())
		inner.Expect(nodePool.Status.Version).NotTo(gomega.BeEmpty(), "status.version is empty")
		observed, err := semver.ParseTolerant(nodePool.Status.Version)
		inner.Expect(err).NotTo(gomega.HaveOccurred(), "status.version %q is not valid semver", nodePool.Status.Version)
		inner.Expect(observed.Equals(want)).To(gomega.BeTrue(), "status.version is %s, want %s", observed, want)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(pollInterval).Should(gomega.Succeed())
}
