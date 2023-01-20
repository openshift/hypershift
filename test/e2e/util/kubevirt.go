package util

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"

	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
)

func WaitForKubeVirtMachines(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, count int32) {
	g := NewWithT(t)
	start := time.Now()

	t.Logf("Waiting for %d kubevirt machines to come online", count)

	namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

	err := wait.PollUntil(5*time.Second, func() (done bool, err error) {
		var kvMachineList capikubevirt.KubevirtMachineList

		err = client.List(ctx, &kvMachineList, crclient.InNamespace(namespace))
		if err != nil {
			t.Errorf("Failed to list KubeVirtMachines: %v", err)
			return false, nil
		}

		readyCount := 0
		for _, machine := range kvMachineList.Items {
			if machine.Status.Ready {
				readyCount++
			}
		}
		if int32(readyCount) < count {
			return false, nil
		}

		return true, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "timeout waiting for kubevirt machines to become ready")

	t.Logf("KubeVirtMachines are ready in %s", time.Since(start).Round(time.Second))
}

func WaitForKubeVirtCluster(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	g := NewWithT(t)
	start := time.Now()

	namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

	t.Logf("Waiting for kubevirt cluster to come online")
	err := wait.PollUntil(5*time.Second, func() (done bool, err error) {
		var kvClusterList capikubevirt.KubevirtClusterList

		err = client.List(ctx, &kvClusterList, crclient.InNamespace(namespace))
		if err != nil {
			t.Errorf("Failed to list KubeVirtClusters: %v", err)
			return false, nil
		}

		if len(kvClusterList.Items) == 0 {
			// waiting on kubevirt cluster to be posted
			return false, nil
		}

		return kvClusterList.Items[0].Status.Ready, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "timeout waiting for kubevirt cluster to become ready")

	t.Logf("KubeVirtCluster is ready in %s", time.Since(start).Round(time.Second))
}
