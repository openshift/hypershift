package util

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	capiagent "github.com/openshift/cluster-api-provider-agent/api/v1alpha1"

	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
)

func WaitForAgentMachines(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, count int32) {
	g := NewWithT(t)
	start := time.Now()

	t.Logf("Waiting for %d agent machines ready status", count)

	namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

	err := wait.PollUntil(5*time.Second, func() (done bool, err error) {
		var agentMachineList capiagent.AgentMachineList

		err = client.List(ctx, &agentMachineList, crclient.InNamespace(namespace))
		if err != nil {
			t.Errorf("Failed to list AgentMachines: %v", err)
			return false, nil
		}

		readyCount := 0
		for _, machine := range agentMachineList.Items {
			if machine.Status.Ready {
				readyCount++
			}
		}
		if int32(readyCount) < count {
			return false, nil
		}

		return true, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "timeout waiting for agent machines to become ready")

	t.Logf("AgentMachines are ready in %s", time.Since(start).Round(time.Second))
}

func WaitForAgentCluster(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	g := NewWithT(t)
	start := time.Now()

	namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

	t.Logf("Waiting for agent cluster to come online")
	err := wait.PollUntil(5*time.Second, func() (done bool, err error) {
		var agentClusterList capiagent.AgentClusterList

		err = client.List(ctx, &agentClusterList, crclient.InNamespace(namespace))
		if err != nil {
			t.Errorf("Failed to list AgentClusters: %v", err)
			return false, nil
		}

		if len(agentClusterList.Items) == 0 {
			// waiting on kubevirt cluster to be posted
			return false, nil
		}

		return agentClusterList.Items[0].Status.Ready, nil
	}, ctx.Done())
	g.Expect(err).NotTo(HaveOccurred(), "timeout waiting for agent cluster to become ready")

	t.Logf("AgentCluster is ready in %s", time.Since(start).Round(time.Second))
}
