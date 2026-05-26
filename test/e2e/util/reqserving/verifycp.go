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
		if err := verifyKASGoMemLimit(ctx, client, cpNamespace, effects); err != nil {
			errs = append(errs, err)
		}
	}

	if effects.MaximumRequestsInflight != nil || effects.MaximumMutatingRequestsInflight != nil {
		if err := verifyInflightConfig(ctx, client, cpNamespace, effects); err != nil {
			errs = append(errs, err)
		}
	}

	if len(effects.ResourceRequests) > 0 {
		errs = append(errs, verifyResourceRequests(ctx, client, cpNamespace, effects)...)
	}

	return utilerrors.NewAggregate(errs)
}

func verifyKASGoMemLimit(ctx context.Context, client crclient.Client, cpNamespace string, effects *schedulingv1alpha1.Effects) error {
	pollCtx, cancel := context.WithTimeout(ctx, DefaultVerificationTimeout)
	defer cancel()
	err := wait.PollUntilContextCancel(pollCtx, DefaultPollingInterval, true, func(pctx context.Context) (bool, error) {
		kasPods := &corev1.PodList{}
		if err := client.List(pctx, kasPods, crclient.MatchingLabels{"app": "kube-apiserver"}, crclient.InNamespace(cpNamespace)); err != nil {
			return false, nil
		}
		if len(kasPods.Items) == 0 {
			return false, nil
		}
		for _, pod := range kasPods.Items {
			if !podHasExpectedEnvVar(pod, "kube-apiserver", KASGoMemLimitEnvVar, *effects.KASGoMemLimit) {
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("timed out waiting for kube-apiserver pods to have %s=%s: %w", KASGoMemLimitEnvVar, *effects.KASGoMemLimit, err)
	}
	return nil
}

func podHasExpectedEnvVar(pod corev1.Pod, containerName, envName, expectedValue string) bool {
	for _, container := range pod.Spec.Containers {
		if container.Name != containerName {
			continue
		}
		for _, env := range container.Env {
			if env.Name == envName {
				return env.Value == expectedValue
			}
		}
		return false
	}
	return false
}

func verifyInflightConfig(ctx context.Context, client crclient.Client, cpNamespace string, effects *schedulingv1alpha1.Effects) error {
	pollCtx, cancel := context.WithTimeout(ctx, DefaultVerificationTimeout)
	defer cancel()
	err := wait.PollUntilContextCancel(pollCtx, DefaultPollingInterval, true, func(pctx context.Context) (bool, error) {
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
		if !inflightArgMatches(kasConfig, "max-requests-inflight", effects.MaximumRequestsInflight) {
			return false, nil
		}
		if !inflightArgMatches(kasConfig, "max-mutating-requests-inflight", effects.MaximumMutatingRequestsInflight) {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return inflightTimeoutError(effects, err)
	}
	return nil
}

func inflightArgMatches(kasConfig *kcpv1.KubeAPIServerConfig, argName string, expected *int) bool {
	if expected == nil {
		return true
	}
	expectedStr := fmt.Sprintf("%d", *expected)
	actual := ""
	if args := kasConfig.APIServerArguments[argName]; len(args) > 0 {
		actual = args[0]
	}
	return actual == expectedStr
}

func inflightTimeoutError(effects *schedulingv1alpha1.Effects, err error) error {
	if effects.MaximumRequestsInflight != nil && effects.MaximumMutatingRequestsInflight != nil {
		return fmt.Errorf("timed out waiting for kube-apiserver config to have max-requests-inflight=%d and max-mutating-requests-inflight=%d: %w", *effects.MaximumRequestsInflight, *effects.MaximumMutatingRequestsInflight, err)
	}
	if effects.MaximumRequestsInflight != nil {
		return fmt.Errorf("timed out waiting for kube-apiserver config to have max-requests-inflight=%d: %w", *effects.MaximumRequestsInflight, err)
	}
	return fmt.Errorf("timed out waiting for kube-apiserver config to have max-mutating-requests-inflight=%d: %w", *effects.MaximumMutatingRequestsInflight, err)
}

func verifyResourceRequests(ctx context.Context, client crclient.Client, cpNamespace string, effects *schedulingv1alpha1.Effects) []error {
	pollCtx, cancel := context.WithTimeout(ctx, DefaultVerificationTimeout)
	defer cancel()
	err := wait.PollUntilContextCancel(pollCtx, DefaultPollingInterval, true, func(pctx context.Context) (bool, error) {
		for _, effect := range effects.ResourceRequests {
			containers, err := getContainersForEffect(pctx, client, cpNamespace, effect)
			if err != nil {
				return false, nil
			}
			if !containerResourcesMatch(containers, effect) {
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return reportResourceMismatches(ctx, client, cpNamespace, effects)
	}
	return nil
}

func getContainersForEffect(ctx context.Context, client crclient.Client, cpNamespace string, effect schedulingv1alpha1.ResourceRequest) ([]corev1.Container, error) {
	if effect.DeploymentName == "etcd" {
		statefulSet := &appsv1.StatefulSet{}
		if err := client.Get(ctx, types.NamespacedName{Name: effect.DeploymentName, Namespace: cpNamespace}, statefulSet); err != nil {
			return nil, err
		}
		return statefulSet.Spec.Template.Spec.Containers, nil
	}
	deployment := &appsv1.Deployment{}
	if err := client.Get(ctx, types.NamespacedName{Name: effect.DeploymentName, Namespace: cpNamespace}, deployment); err != nil {
		return nil, err
	}
	return deployment.Spec.Template.Spec.Containers, nil
}

func containerResourcesMatch(containers []corev1.Container, effect schedulingv1alpha1.ResourceRequest) bool {
	for _, container := range containers {
		if container.Name != effect.ContainerName {
			continue
		}
		matched := true
		if effect.Memory != nil {
			if mr := container.Resources.Requests.Memory(); mr == nil || mr.Cmp(*effect.Memory) != 0 {
				matched = false
			}
		}
		if effect.CPU != nil {
			if cr := container.Resources.Requests.Cpu(); cr == nil || cr.Cmp(*effect.CPU) != 0 {
				matched = false
			}
		}
		return matched
	}
	return false
}

func reportResourceMismatches(ctx context.Context, client crclient.Client, cpNamespace string, effects *schedulingv1alpha1.Effects) []error {
	var errs []error
	for _, effect := range effects.ResourceRequests {
		workloadKind := "deployment"
		if effect.DeploymentName == "etcd" {
			workloadKind = "statefulset"
		}
		containers, getErr := getContainersForEffect(ctx, client, cpNamespace, effect)
		if getErr != nil {
			errs = append(errs, fmt.Errorf("failed to get %s %s: %w", workloadKind, effect.DeploymentName, getErr))
			continue
		}
		for _, container := range containers {
			if container.Name != effect.ContainerName {
				continue
			}
			if effect.Memory != nil {
				if container.Resources.Requests.Memory().Cmp(*effect.Memory) != 0 {
					errs = append(errs, fmt.Errorf("%s %s has memory request set to %v, expected %v", workloadKind, effect.DeploymentName, container.Resources.Requests.Memory(), *effect.Memory))
				}
			}
			if effect.CPU != nil {
				if container.Resources.Requests.Cpu().Cmp(*effect.CPU) != 0 {
					errs = append(errs, fmt.Errorf("%s %s has cpu request set to %v, expected %v", workloadKind, effect.DeploymentName, container.Resources.Requests.Cpu(), *effect.CPU))
				}
			}
			break
		}
	}
	return errs
}
