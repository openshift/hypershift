//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type AdditionalTrustBundlePropagationTest struct {
	DummyInfraSetup
	ctx        context.Context
	mgmtClient crclient.Client

	hostedCluster *hyperv1.HostedCluster
}

func NewAdditionalTrustBundlePropagation(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) *AdditionalTrustBundlePropagationTest {
	return &AdditionalTrustBundlePropagationTest{
		ctx:           ctx,
		mgmtClient:    mgmtClient,
		hostedCluster: hostedCluster,
	}
}

func (k *AdditionalTrustBundlePropagationTest) Setup(t *testing.T) {
	t.Log("Starting AdditionalTrustBundlePropagationTest.")
}

func (k *AdditionalTrustBundlePropagationTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.hostedCluster.Name + "-" + "test-additional-trust-bundle-propagation",
			Namespace: k.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)
	nodePool.Spec.Replicas = &oneReplicas

	return nodePool, nil
}

func (k *AdditionalTrustBundlePropagationTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	t.Run("AdditionalTrustBundlePropagationTest", func(t *testing.T) {
		e2eutil.AtLeast(t, e2eutil.Version418)

		additionalTrustBundle := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "additional-trust-bundle",
				Namespace: k.hostedCluster.Namespace,
			},
			Data: map[string]string{
				"ca-bundle.crt": "dummy",
			},
		}

		if err := k.mgmtClient.Create(k.ctx, additionalTrustBundle); err != nil {
			t.Fatalf("failed to create additional trust bundle configmap: %v", err)
		}

		t.Logf("Updating hosted cluster with additional trust bundle. Bundle: %s", additionalTrustBundle.Name)
		err := e2eutil.UpdateObject(t, k.ctx, k.mgmtClient, k.hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.AdditionalTrustBundle = &corev1.LocalObjectReference{Name: additionalTrustBundle.Name}
		})
		if err != nil {
			t.Fatalf("failed to update HostedCluster with additional trust bundle: %v", err)
		}

		e2eutil.EventuallyObject(t, k.ctx, fmt.Sprintf("Waiting for NodePool %s/%s to begin updating", nodePool.Namespace, nodePool.Name),
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				err := k.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
				return &nodePool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolUpdatingConfigConditionType,
					Status: metav1.ConditionTrue,
				}),
			},
			e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(5*time.Minute),
		)

		e2eutil.EventuallyObject(t, k.ctx, fmt.Sprintf("Waiting for NodePool %s/%s to stop updating", nodePool.Namespace, nodePool.Name),
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				err := k.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
				return &nodePool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolUpdatingConfigConditionType,
					Status: metav1.ConditionFalse,
				}),
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolAllNodesHealthyConditionType,
					Status: metav1.ConditionTrue,
				}),
			},
			e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(20*time.Minute),
		)

		t.Logf("Updating hosted cluster by removing additional trust bundle.")
		if err = e2eutil.UpdateObject(t, k.ctx, k.mgmtClient, k.hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.AdditionalTrustBundle = nil
		}); err != nil {
			t.Fatalf("failed to update HostedCluster with additional trust bundle: %v", err)
		}

		// Ensure the control plane operator deployment is updated and no longer mounts the additional trust bundle configmap
		controlPlaneOperatorDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "control-plane-operator",
				Namespace: manifests.HostedControlPlaneNamespace(k.hostedCluster.Namespace, k.hostedCluster.Name),
			},
		}
		e2eutil.EventuallyObject(t, k.ctx, "Waiting for control plane operator deployment to be updated",
			func(ctx context.Context) (*appsv1.Deployment, error) {
				err := k.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(controlPlaneOperatorDeployment), controlPlaneOperatorDeployment)
				return controlPlaneOperatorDeployment, err
			},
			[]e2eutil.Predicate[*appsv1.Deployment]{
				func(obj *appsv1.Deployment) (bool, string, error) {
					volumes := obj.Spec.Template.Spec.Volumes
					for _, volume := range volumes {
						if volume.ConfigMap != nil && volume.ConfigMap.Name == "trusted-ca" {
							return false, "Additional trust bundle configmap is still included in CPO", nil
						}
					}
					if ready := util.IsDeploymentReady(k.ctx, obj); !ready {
						return false, "Deployment is not ready", nil
					}
					return true, "Additional trust bundle configmap is not included in CPO", nil
				},
			},
		)

		e2eutil.EventuallyObject(t, k.ctx, fmt.Sprintf("Waiting for NodePool %s/%s to begin updating", nodePool.Namespace, nodePool.Name),
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				err := k.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
				return &nodePool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolUpdatingConfigConditionType,
					Status: metav1.ConditionTrue,
				}),
			},
			e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(5*time.Minute),
		)

		e2eutil.EventuallyObject(t, k.ctx, fmt.Sprintf("Waiting for NodePool %s/%s to stop updating", nodePool.Namespace, nodePool.Name),
			func(ctx context.Context) (*hyperv1.NodePool, error) {
				err := k.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
				return &nodePool, err
			},
			[]e2eutil.Predicate[*hyperv1.NodePool]{
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolUpdatingConfigConditionType,
					Status: metav1.ConditionFalse,
				}),
				e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
					Type:   hyperv1.NodePoolAllNodesHealthyConditionType,
					Status: metav1.ConditionTrue,
				}),
			},
			e2eutil.WithInterval(10*time.Second), e2eutil.WithTimeout(20*time.Minute),
		)
	})
}
