/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package hostedcluster

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/featuregate"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

type FeatureGateReconciler struct {
	*HostedClusterReconciler
}

func (r *FeatureGateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	if !featuregate.Gates.Enabled(featuregate.AutoProvision) {
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling featuregate: Autoprovision")
	// Look up the HostedCluster instance to reconcile
	hc := &hyperv1.HostedCluster{}
	err := r.Get(ctx, req.NamespacedName, hc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("hostedcluster not found, aborting reconcile", "name", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get cluster %q: %w", req.NamespacedName, err)
	}

	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("karpenter-shim-%s", hc.Name),
			Labels: map[string]string{
				"featuregate": "autoprovision",
			},
		},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: hc.Name,
			Release: hyperv1.Release{
				Image: hc.Spec.Release.Image,
			},
			Platform: hyperv1.NodePoolPlatform{
				Type: hyperv1.AWSPlatform,
				AWS:  &hyperv1.AWSNodePoolPlatform{},
			},
			Replicas: new(int32),
		},
	}

	createOrUpdate := r.createOrUpdate(req)
	if _, err := createOrUpdate(ctx, r.Client, nodePool, func() error {
		return nil
	}); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
