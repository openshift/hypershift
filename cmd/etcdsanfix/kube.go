package etcdsanfix

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	hyperutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// newKube builds a management-cluster client and returns the rest config alongside it
// (the config is needed to open port-forwards in out-of-cluster mode).
func newKube() (crclient.Client, *rest.Config, error) {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("get management-cluster restconfig: %w", err)
	}
	c, err := crclient.New(cfg, crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, nil, fmt.Errorf("build management-cluster client: %w", err)
	}
	return c, cfg, nil
}

// setupKube populates the management-cluster client and rest config on options. It is
// idempotent, so calling it from a command's RunE and lazily from etcdDial is safe.
func (o *options) setupKube() error {
	if o.kube != nil {
		return nil
	}
	c, cfg, err := newKube()
	if err != nil {
		return err
	}
	o.kube = c
	o.restConfig = cfg
	return nil
}

// getHCP returns the single HostedControlPlane in the namespace.
func getHCP(ctx context.Context, c crclient.Client, namespace string) (*hyperv1.HostedControlPlane, error) {
	list := &hyperv1.HostedControlPlaneList{}
	if err := c.List(ctx, list, crclient.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("list hostedcontrolplanes in %s: %w", namespace, err)
	}
	if len(list.Items) != 1 {
		return nil, fmt.Errorf("expected exactly 1 HostedControlPlane in %s, found %d", namespace, len(list.Items))
	}
	return &list.Items[0], nil
}

// setHostedClusterPause sets (or, with nil, clears) spec.pausedUntil on the HostedCluster.
func setHostedClusterPause(ctx context.Context, c crclient.Client, hcNamespace, hcName string, pausedUntil *string) error {
	hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: hcNamespace, Name: hcName}}
	return hyperutil.UpdateObject(ctx, c, hc, func() error {
		hc.Spec.PausedUntil = pausedUntil
		return nil
	})
}

func deletePod(ctx context.Context, c crclient.Client, namespace, name string) error {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	_, err := hyperutil.DeleteIfNeeded(ctx, c, pod)
	return err
}
