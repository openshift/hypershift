package reqserving

import (
	"context"
	"fmt"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportutil "github.com/openshift/hypershift/support/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// WaitForHostedClusterSizeLabel waits for the hosted cluster to have the expected size label
func WaitForHostedClusterSizeLabel(ctx context.Context, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, size string) {
	var err error
	log := ctrl.LoggerFrom(ctx)
	log.Info("waiting for hosted cluster size label", "hostedCluster", hostedCluster.Name, "size", size)
	pollCtx, cancel := context.WithTimeout(ctx, DefaultVerificationTimeout)
	defer cancel()
	err = wait.PollUntilContextCancel(pollCtx, DefaultPollingInterval, true, func(pctx context.Context) (bool, error) {
		hc := &hyperv1.HostedCluster{}
		if err := mgtClient.Get(pctx, types.NamespacedName{Name: hostedCluster.Name, Namespace: hostedCluster.Namespace}, hc); err != nil {
			log.Error(err, "failed to get hosted cluster")
			return false, nil
		}
		sizeLabel := hc.Labels[hyperv1.HostedClusterSizeLabel]
		if sizeLabel != size {
			return false, nil
		}
		log.Info("hosted cluster has expected size label", "hostedCluster", hostedCluster.Name, "size", sizeLabel)
		return true, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("Failed waiting for hostedcluster %s/%s to get size label %s", hostedCluster.Namespace, hostedCluster.Name, size))
}

// WaitForControlPlaneWorkloadsReady waits until all control plane Deployments and StatefulSets
// in the control plane namespace are successfully rolled out and ready.
func WaitForControlPlaneWorkloadsReady(ctx context.Context, hc *hyperv1.HostedCluster) error {
	cpNamespace := fmt.Sprintf("%s-%s", hc.Namespace, hc.Name)
	client, err := e2eutil.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}
	deployments := &appsv1.DeploymentList{}
	deploymentCtx, cancel := context.WithTimeout(ctx, DefaultVerificationTimeout)
	defer cancel()
	err = wait.PollUntilContextCancel(deploymentCtx, DefaultPollingInterval, true, func(ctx context.Context) (bool, error) {
		if err := client.List(ctx, deployments, crclient.InNamespace(cpNamespace)); err != nil {
			return false, err
		}
		if len(deployments.Items) == 0 {
			return false, nil
		}
		for _, deployment := range deployments.Items {
			if supportutil.IsDeploymentReady(ctx, &deployment) {
				continue
			}
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("failed to wait for deployments to be ready: %w", err)
	}

	statefulSets := &appsv1.StatefulSetList{}
	statefulSetCtx, cancel := context.WithTimeout(ctx, DefaultVerificationTimeout)
	defer cancel()
	err = wait.PollUntilContextCancel(statefulSetCtx, DefaultPollingInterval, true, func(ctx context.Context) (bool, error) {
		if err := client.List(ctx, statefulSets, crclient.InNamespace(cpNamespace)); err != nil {
			return false, nil
		}
		if len(statefulSets.Items) == 0 {
			return false, nil
		}
		for _, statefulSet := range statefulSets.Items {
			if supportutil.IsStatefulSetReady(ctx, &statefulSet) {
				continue
			}
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("failed to wait for statefulsets to be ready: %w", err)
	}
	return nil
}
