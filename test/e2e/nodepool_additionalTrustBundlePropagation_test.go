//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
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

	})
}
