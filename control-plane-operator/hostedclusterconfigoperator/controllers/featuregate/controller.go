package featuregate

import (
	"context"
	"fmt"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1beta1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/hypershift/v1beta1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	"github.com/openshift/hypershift/support/util"

	nodelib "github.com/openshift/library-go/pkg/apiserver/node"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/blang/semver/v4"
)

type minimumKubeletVersionReconciler struct {
	hcpName, hcpNamespace string
	client                hypershiftclient.Interface
	lister                client.Client

	guestClusterClient client.Client
}

func (r *minimumKubeletVersionReconciler) Reconcile(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	var hcp hypershiftv1beta1.HostedControlPlane
	if err := r.lister.Get(ctx, client.ObjectKey{
		Namespace: r.hcpNamespace,
		Name:      r.hcpName,
	}, &hcp); err != nil {
		return reconcile.Result{}, err
	}
	if isPaused, duration := util.IsReconciliationPaused(log, hcp.Spec.PausedUntil); isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hcp.Spec.PausedUntil)
		return ctrl.Result{
			RequeueAfter: duration,
		}, nil
	}
	if hcp.ObjectMeta.DeletionTimestamp != nil {
		return reconcile.Result{}, nil
	}

	nodes := &corev1.NodeList{}
	if err := r.guestClusterClient.List(ctx, nodes); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Nodes: %w", err)
	}

	currentOldestKubelet := getOldestKubeletVersion(nodes.Items)
	if currentOldestKubelet == nil {
		// no valid nodes, leave the field unset
		return reconcile.Result{}, nil
	}

	cfg := hypershiftv1beta1applyconfigurations.HostedControlPlane(r.hcpName, r.hcpNamespace)
	cfg.Status = hypershiftv1beta1applyconfigurations.HostedControlPlaneStatus().WithOldestKubeletVersion(currentOldestKubelet.String())
	_, err := r.client.HypershiftV1beta1().HostedControlPlanes(r.hcpNamespace).ApplyStatus(ctx, cfg, metav1.ApplyOptions{FieldManager: ControllerName})
	return reconcile.Result{}, err
}

func getOldestKubeletVersion(nodes []corev1.Node) *semver.Version {
	var oldestVersion *semver.Version
	for _, node := range nodes {
		vStr := node.Status.NodeInfo.KubeletVersion
		v, err := nodelib.ParseKubeletVersion(vStr)
		if err != nil {
			continue
		}
		if oldestVersion == nil || v.LT(*oldestVersion) {
			oldestVersion = v
		}
	}
	return oldestVersion
}
