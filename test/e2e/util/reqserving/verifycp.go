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

package reqserving

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	kcpv1 "github.com/openshift/api/kubecontrolplane/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	KASGoMemLimitEnvVar = "GOMEMLIMIT"
)

// VerifyRequestServingCPEffects verifies that the request serving control plane has the
// values specified in the clustersizingconfiguration applied to its pods.
func VerifyRequestServingCPEffects(ctx context.Context, hc *hyperv1.HostedCluster) error {
	cpNamespace := fmt.Sprintf("%s-%s", hc.Namespace, hc.Name)
	client, err := e2eutil.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}
	csc := &schedulingv1alpha1.ClusterSizingConfiguration{}
	if err := client.Get(ctx, types.NamespacedName{Name: "cluster"}, csc); err != nil {
		return fmt.Errorf("failed to get clustersizingconfiguration: %w", err)
	}
	sizeLabel := hc.Labels[hyperv1.HostedClusterSizeLabel]
	if sizeLabel == "" {
		return fmt.Errorf("hostedcluster %s has no size label", hc.Name)
	}
	var effects *schedulingv1alpha1.Effects
	for _, size := range csc.Spec.Sizes {
		if size.Name == sizeLabel {
			effects = size.Effects
			break
		}
	}
	if effects == nil {
		return nil
	}

	var errs []error

	if effects.KASGoMemLimit != nil {
		// Poll until all kube-apiserver pods have the expected GOMEMLIMIT value
		pollCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		err := wait.PollUntilContextCancel(pollCtx, 30*time.Second, true, func(pctx context.Context) (bool, error) {
			kasPods := &corev1.PodList{}
			if err := client.List(pctx, kasPods, crclient.MatchingLabels{"app": "kube-apiserver"}, crclient.InNamespace(cpNamespace)); err != nil {
				return false, nil
			}
			if len(kasPods.Items) == 0 {
				return false, nil
			}
			for _, pod := range kasPods.Items {
				foundExpected := false
				for _, container := range pod.Spec.Containers {
					if container.Name != "kube-apiserver" {
						continue
					}
					for _, env := range container.Env {
						if env.Name == KASGoMemLimitEnvVar {
							if env.Value == *effects.KASGoMemLimit {
								foundExpected = true
							}
							break
						}
					}
					break
				}
				if !foundExpected {
					return false, nil
				}
			}
			return true, nil
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("timed out waiting for kube-apiserver pods to have %s=%s: %w", KASGoMemLimitEnvVar, *effects.KASGoMemLimit, err))
		}
	}

	if effects.MaximumRequestsInflight != nil || effects.MaximumMutatingRequestsInflight != nil {
		pollCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		err := wait.PollUntilContextCancel(pollCtx, 30*time.Second, true, func(pctx context.Context) (bool, error) {
			kasConfigMap := &corev1.ConfigMap{}
			if err := client.Get(pctx, types.NamespacedName{Name: "kas-config", Namespace: cpNamespace}, kasConfigMap); err != nil {
				return false, nil
			}
			data, ok := kasConfigMap.Data["config.json"]
			if !ok || data == "" {
				return false, nil
			}
			kasConfig := &kcpv1.KubeAPIServerConfig{}
			if err := json.Unmarshal([]byte(data), &kasConfig); err != nil {
				return false, nil
			}
			// Check expected values
			if effects.MaximumRequestsInflight != nil {
				expected := fmt.Sprintf("%d", *effects.MaximumRequestsInflight)
				actual := ""
				if kasConfig.APIServerArguments["max-requests-inflight"] != nil {
					actual = kasConfig.APIServerArguments["max-requests-inflight"][0]
				}
				if actual != expected {
					return false, nil
				}
			}
			if effects.MaximumMutatingRequestsInflight != nil {
				expected := fmt.Sprintf("%d", *effects.MaximumMutatingRequestsInflight)
				actual := ""
				if kasConfig.APIServerArguments["max-mutating-requests-inflight"] != nil {
					actual = kasConfig.APIServerArguments["max-mutating-requests-inflight"][0]
				}
				if actual != expected {
					return false, nil
				}
			}
			return true, nil
		})
		if err != nil {
			if effects.MaximumRequestsInflight != nil && effects.MaximumMutatingRequestsInflight != nil {
				errs = append(errs, fmt.Errorf("timed out waiting for kube-apiserver config to have max-requests-inflight=%d and max-mutating-requests-inflight=%d: %w", *effects.MaximumRequestsInflight, *effects.MaximumMutatingRequestsInflight, err))
			} else if effects.MaximumRequestsInflight != nil {
				errs = append(errs, fmt.Errorf("timed out waiting for kube-apiserver config to have max-requests-inflight=%d: %w", *effects.MaximumRequestsInflight, err))
			} else if effects.MaximumMutatingRequestsInflight != nil {
				errs = append(errs, fmt.Errorf("timed out waiting for kube-apiserver config to have max-mutating-requests-inflight=%d: %w", *effects.MaximumMutatingRequestsInflight, err))
			}
		}
	}

	if len(effects.ResourceRequests) > 0 {
		pollCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		err := wait.PollUntilContextCancel(pollCtx, 30*time.Second, true, func(pctx context.Context) (bool, error) {
			for _, effect := range effects.ResourceRequests {
				matched := false
				// etcd is deployed as a StatefulSet, not a Deployment
				if effect.DeploymentName == "etcd" {
					statefulSet := &appsv1.StatefulSet{}
					if err := client.Get(pctx, types.NamespacedName{Name: effect.DeploymentName, Namespace: cpNamespace}, statefulSet); err != nil {
						return false, nil
					}
					for _, container := range statefulSet.Spec.Template.Spec.Containers {
						if container.Name != effect.ContainerName {
							continue
						}
						// assume match and disprove if any expected resource does not match
						matched = true
						if effect.Memory != nil {
							if container.Resources.Requests.Memory().Cmp(*effect.Memory) != 0 {
								matched = false
							}
						}
						if effect.CPU != nil {
							if container.Resources.Requests.Cpu().Cmp(*effect.CPU) != 0 {
								matched = false
							}
						}
						break
					}
				} else {
					deployment := &appsv1.Deployment{}
					if err := client.Get(pctx, types.NamespacedName{Name: effect.DeploymentName, Namespace: cpNamespace}, deployment); err != nil {
						return false, nil
					}
					for _, container := range deployment.Spec.Template.Spec.Containers {
						if container.Name != effect.ContainerName {
							continue
						}
						// assume match and disprove if any expected resource does not match
						matched = true
						if effect.Memory != nil {
							if container.Resources.Requests.Memory().Cmp(*effect.Memory) != 0 {
								matched = false
							}
						}
						if effect.CPU != nil {
							if container.Resources.Requests.Cpu().Cmp(*effect.CPU) != 0 {
								matched = false
							}
						}
						break
					}
				}
				if !matched {
					return false, nil
				}
			}
			return true, nil
		})
		if err != nil {
			// Do a final pass to report detailed mismatches
			for _, effect := range effects.ResourceRequests {
				if effect.DeploymentName == "etcd" {
					statefulSet := &appsv1.StatefulSet{}
					if getErr := client.Get(ctx, types.NamespacedName{Name: effect.DeploymentName, Namespace: cpNamespace}, statefulSet); getErr != nil {
						errs = append(errs, fmt.Errorf("failed to get statefulset %s: %w", effect.DeploymentName, getErr))
						continue
					}
					for _, container := range statefulSet.Spec.Template.Spec.Containers {
						if container.Name != effect.ContainerName {
							continue
						}
						if effect.Memory != nil {
							if container.Resources.Requests.Memory().Cmp(*effect.Memory) != 0 {
								errs = append(errs, fmt.Errorf("statefulset %s has memory request set to %v, expected %v", statefulSet.Name, container.Resources.Requests.Memory(), *effect.Memory))
							}
						}
						if effect.CPU != nil {
							if container.Resources.Requests.Cpu().Cmp(*effect.CPU) != 0 {
								errs = append(errs, fmt.Errorf("statefulset %s has cpu request set to %v, expected %v", statefulSet.Name, container.Resources.Requests.Cpu(), *effect.CPU))
							}
						}
						break
					}
				} else {
					deployment := &appsv1.Deployment{}
					if getErr := client.Get(ctx, types.NamespacedName{Name: effect.DeploymentName, Namespace: cpNamespace}, deployment); getErr != nil {
						errs = append(errs, fmt.Errorf("failed to get deployment %s: %w", effect.DeploymentName, getErr))
						continue
					}
					for _, container := range deployment.Spec.Template.Spec.Containers {
						if container.Name != effect.ContainerName {
							continue
						}
						if effect.Memory != nil {
							if container.Resources.Requests.Memory().Cmp(*effect.Memory) != 0 {
								errs = append(errs, fmt.Errorf("deployment %s has memory request set to %v, expected %v", deployment.Name, container.Resources.Requests.Memory(), *effect.Memory))
							}
						}
						if effect.CPU != nil {
							if container.Resources.Requests.Cpu().Cmp(*effect.CPU) != 0 {
								errs = append(errs, fmt.Errorf("deployment %s has cpu request set to %v, expected %v", deployment.Name, container.Resources.Requests.Cpu(), *effect.CPU))
							}
						}
						break
					}
				}
			}
		}
	}

	return utilerrors.NewAggregate(errs)
}
