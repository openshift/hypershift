//go:build e2ev2

package tests

import (
	"bufio"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	// registryOverridesInputEnv is the input of these specs: the
	// --registry-overrides value ("src1=dst1,src2=dst2") the job configured
	// on the HyperShift operator before creating the hosted clusters. The specs
	// skip when it is unset or empty.
	registryOverridesInputEnv = "E2E_REGISTRY_OVERRIDES"

	// The HyperShift operator Deployment and container, as created by
	// "hypershift install" (cmd/install/assets).
	registryOverridesHONamespace     = "hypershift"
	registryOverridesHOName          = "operator"
	registryOverridesHOContainerName = "operator"

	registryOverridesFlag = "--registry-overrides"

	// registryOverridesIgnitionServerName is the ignition-server Deployment,
	// its primary container and the value of its "app" pod label.
	registryOverridesIgnitionServerName = "ignition-server"
	// registryOverridesPayloadGeneratedLine is logged by the ignition-server
	// token secret controller after it generates a payload
	// (ignition-server/controllers/tokensecret_controller.go).
	registryOverridesPayloadGeneratedLine = "IgnitionProvider generated payload"

	// registryOverridesSyncerNamespace and registryOverridesSyncerName are the
	// hosted cluster DaemonSet the HCCO runs with its own image
	// (HOSTED_CLUSTER_CONFIG_OPERATOR_IMAGE), which the HyperShift operator
	// resolves from the release like the CPO image. Its container is named
	// after it.
	registryOverridesSyncerNamespace = "kube-system"
	registryOverridesSyncerName      = "global-pull-secret-syncer"
)

// The syncer DaemonSet requires, through node affinity, either of these node
// labels: the HyperShift operator sets the first ("true") on AWS and Azure
// Replace NodePools, Karpenter sets the second on its nodes. Other workers run
// no syncer pod (docs/content/how-to/common/global-pull-secret.md).
const (
	registryOverridesSyncerNodeLabel      = "hypershift.openshift.io/nodepool-globalps-enabled"
	registryOverridesSyncerKarpenterLabel = "karpenter.sh/nodepool"
)

// registryOverridesMandatoryWorkloads are control plane Deployments whose
// primary container (named after the Deployment) must run a mirror image for
// the check to be meaningful: the operator the HO deploys from the release, and
// two components the CPO deploys from the release.
var registryOverridesMandatoryWorkloads = []string{"control-plane-operator", "kube-apiserver", "kube-controller-manager"}

// registryOverridesPropagationTargets are the control plane Deployments whose
// primary container (named after the Deployment) receives --registry-overrides
// as its own flag.
var registryOverridesPropagationTargets = []string{"control-plane-operator", "hosted-cluster-config-operator", "ignition-server"}

// registryOverridesParseArg parses a --registry-overrides value
// ("src1=dst1,src2=dst2"). "" and "=" mean no overrides, as in the HO and CPO
// command lines.
func registryOverridesParseArg(value string) (map[string]string, error) {
	overrides := map[string]string{}
	if value == "" || value == "=" {
		return overrides, nil
	}
	for _, pair := range strings.Split(value, ",") {
		source, target, ok := strings.Cut(pair, "=")
		if !ok || source == "" || target == "" {
			return nil, fmt.Errorf("invalid registry override %q in %q: want source=target", pair, value)
		}
		if _, dup := overrides[source]; dup {
			return nil, fmt.Errorf("duplicate registry override source %q in %q", source, value)
		}
		overrides[source] = target
	}
	return overrides, nil
}

// registryOverridesFormatArg formats overrides as a --registry-overrides value
// with sorted pairs, so repeated runs produce the same Deployment spec.
func registryOverridesFormatArg(overrides map[string]string) string {
	pairs := make([]string, 0, len(overrides))
	for source, target := range overrides {
		pairs = append(pairs, source+"="+target)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

// registryOverridesOperatorContainer returns the operator container of the HO
// Deployment.
func registryOverridesOperatorContainer(deployment *appsv1.Deployment) (*corev1.Container, error) {
	if deployment == nil {
		return nil, fmt.Errorf("the HyperShift operator Deployment is nil")
	}
	for i := range deployment.Spec.Template.Spec.Containers {
		if deployment.Spec.Template.Spec.Containers[i].Name == registryOverridesHOContainerName {
			return &deployment.Spec.Template.Spec.Containers[i], nil
		}
	}
	return nil, fmt.Errorf("deployment %s/%s has no %q container", deployment.Namespace, deployment.Name, registryOverridesHOContainerName)
}

// registryOverridesArgValue returns the value of --registry-overrides in args,
// accepting both "--registry-overrides=v" and "--registry-overrides v". found
// is false when the flag is absent.
func registryOverridesArgValue(args []string) (value string, found bool, err error) {
	for i, arg := range args {
		switch {
		case strings.HasPrefix(arg, registryOverridesFlag+"="):
			if found {
				return "", false, fmt.Errorf("%s is set more than once", registryOverridesFlag)
			}
			value, found = strings.TrimPrefix(arg, registryOverridesFlag+"="), true
		case arg == registryOverridesFlag:
			if found {
				return "", false, fmt.Errorf("%s is set more than once", registryOverridesFlag)
			}
			if i+1 >= len(args) {
				return "", false, fmt.Errorf("%s has no value", registryOverridesFlag)
			}
			value, found = args[i+1], true
		}
	}
	return value, found, nil
}

// registryOverridesCheckDeploymentRolledOut reports whether a Deployment has
// observed its latest spec and all replicas are updated and available.
func registryOverridesCheckDeploymentRolledOut(deployment *appsv1.Deployment) (bool, string) {
	if deployment == nil {
		return false, "Deployment is nil"
	}
	replicas := int32(1)
	if deployment.Spec.Replicas != nil {
		replicas = *deployment.Spec.Replicas
	}
	status := deployment.Status
	switch {
	case status.ObservedGeneration < deployment.Generation:
		return false, fmt.Sprintf("Deployment %s/%s observed generation %d, want %d", deployment.Namespace, deployment.Name, status.ObservedGeneration, deployment.Generation)
	case status.UpdatedReplicas != replicas || status.Replicas != replicas:
		return false, fmt.Sprintf("Deployment %s/%s has %d updated of %d replicas (%d total)", deployment.Namespace, deployment.Name, status.UpdatedReplicas, replicas, status.Replicas)
	case status.AvailableReplicas != replicas:
		return false, fmt.Sprintf("Deployment %s/%s has %d available of %d replicas", deployment.Namespace, deployment.Name, status.AvailableReplicas, replicas)
	}
	return true, ""
}

// registryOverridesCheckStatefulSetRolledOut reports whether a StatefulSet has
// observed its latest spec and all replicas run the current revision and are
// ready.
func registryOverridesCheckStatefulSetRolledOut(statefulSet *appsv1.StatefulSet) (bool, string) {
	if statefulSet == nil {
		return false, "StatefulSet is nil"
	}
	replicas := int32(1)
	if statefulSet.Spec.Replicas != nil {
		replicas = *statefulSet.Spec.Replicas
	}
	status := statefulSet.Status
	switch {
	case status.ObservedGeneration < statefulSet.Generation:
		return false, fmt.Sprintf("StatefulSet %s/%s observed generation %d, want %d", statefulSet.Namespace, statefulSet.Name, status.ObservedGeneration, statefulSet.Generation)
	case status.UpdateRevision != "" && status.CurrentRevision != status.UpdateRevision:
		return false, fmt.Sprintf("StatefulSet %s/%s is at revision %s, want %s", statefulSet.Namespace, statefulSet.Name, status.CurrentRevision, status.UpdateRevision)
	case status.UpdatedReplicas != replicas || status.ReadyReplicas != replicas:
		return false, fmt.Sprintf("StatefulSet %s/%s has %d updated and %d ready of %d replicas", statefulSet.Namespace, statefulSet.Name, status.UpdatedReplicas, status.ReadyReplicas, replicas)
	}
	return true, ""
}

// registryOverridesCheckPropagated reports whether the primary container of
// the Deployment (named after it) carries --registry-overrides with exactly the
// expected mapping. The flag is looked up in Command followed by Args, because
// components set it in either (the HCCO in Command, the CPO and
// ignition-server in Args). A malformed flag value is returned as an error.
func registryOverridesCheckPropagated(deployment *appsv1.Deployment, expected map[string]string) (bool, string, error) {
	if deployment == nil {
		return false, "", fmt.Errorf("deployment is nil")
	}
	idx := slices.IndexFunc(deployment.Spec.Template.Spec.Containers, func(c corev1.Container) bool { return c.Name == deployment.Name })
	if idx < 0 {
		return false, fmt.Sprintf("Deployment %s/%s has no container %s", deployment.Namespace, deployment.Name, deployment.Name), nil
	}
	container := deployment.Spec.Template.Spec.Containers[idx]
	value, found, err := registryOverridesArgValue(append(slices.Clone(container.Command), container.Args...))
	if err != nil {
		return false, "", fmt.Errorf("deployment %s/%s container %s: %w", deployment.Namespace, deployment.Name, container.Name, err)
	}
	if !found {
		return false, fmt.Sprintf("Deployment %s/%s container %s has no %s", deployment.Namespace, deployment.Name, container.Name, registryOverridesFlag), nil
	}
	got, err := registryOverridesParseArg(value)
	if err != nil {
		return false, "", fmt.Errorf("deployment %s/%s container %s: %w", deployment.Namespace, deployment.Name, container.Name, err)
	}
	if registryOverridesFormatArg(got) == registryOverridesFormatArg(expected) {
		return true, "", nil
	}
	return false, fmt.Sprintf("Deployment %s/%s container %s has %s=%q, want %q", deployment.Namespace, deployment.Name, container.Name, registryOverridesFlag, value, registryOverridesFormatArg(expected)), nil
}

// registryOverridesMatchesPrefix reports whether image is prefix itself or
// starts with prefix followed by a reference separator, so that a repository
// prefix never matches a longer repository name. As in the product matcher, ":"
// is a separator only when prefix has a path: after a bare host it starts a
// port, so "quay.io" does not match "quay.io:5000/a".
func registryOverridesMatchesPrefix(image, prefix string) bool {
	if prefix == "" || !strings.HasPrefix(image, prefix) {
		return false
	}
	if len(image) == len(prefix) {
		return true
	}
	switch image[len(prefix)] {
	case '/', '@':
		return true
	case ':':
		return strings.Contains(prefix, "/")
	}
	return false
}

// registryOverridesCheckImages reports whether the control plane runs from the
// mirror: no container image of the Deployments, StatefulSets and CronJob pod
// templates still starts with an overridden source, the primary container
// (named after the workload) of every mandatory workload runs an image on a
// mirror repository, and every Deployment and StatefulSet has rolled out.
// CronJobs are checked on their template only: no Job has to run. Images equal
// to the HO image are ignored by the source check: the HO injects its own image
// (for example as the utilities or karpenter operator image), which overrides
// don't apply to. They never satisfy the mandatory check.
func registryOverridesCheckImages(deployments []appsv1.Deployment, statefulSets []appsv1.StatefulSet, cronJobs []batchv1.CronJob, overrides map[string]string, hoImage string, mandatory []string) (bool, string, error) {
	if len(overrides) == 0 {
		return false, "", fmt.Errorf("no registry overrides to check against")
	}
	targets := sets.New[string]()
	for _, target := range overrides {
		targets.Insert(target)
	}
	matchesAny := func(image string, prefixes sets.Set[string]) bool {
		for prefix := range prefixes {
			if registryOverridesMatchesPrefix(image, prefix) {
				return true
			}
		}
		return false
	}
	sources := sets.KeySet(overrides)

	var problems []string
	mirrored := sets.New[string]()
	checkPodSpec := func(kind, namespace, name string, spec corev1.PodSpec) {
		containers := append(slices.Clone(spec.InitContainers), spec.Containers...)
		for _, container := range containers {
			switch {
			case container.Image == hoImage:
			case matchesAny(container.Image, sources):
				problems = append(problems, fmt.Sprintf("%s %s/%s container %s still uses %s", kind, namespace, name, container.Name, container.Image))
			case matchesAny(container.Image, targets) && container.Name == name:
				mirrored.Insert(name)
			}
		}
	}
	for i := range deployments {
		d := &deployments[i]
		checkPodSpec("Deployment", d.Namespace, d.Name, d.Spec.Template.Spec)
		if done, reason := registryOverridesCheckDeploymentRolledOut(d); !done {
			problems = append(problems, reason)
		}
	}
	for i := range statefulSets {
		s := &statefulSets[i]
		checkPodSpec("StatefulSet", s.Namespace, s.Name, s.Spec.Template.Spec)
		if done, reason := registryOverridesCheckStatefulSetRolledOut(s); !done {
			problems = append(problems, reason)
		}
	}
	for i := range cronJobs {
		c := &cronJobs[i]
		checkPodSpec("CronJob", c.Namespace, c.Name, c.Spec.JobTemplate.Spec.Template.Spec)
	}
	for _, name := range mandatory {
		if !mirrored.Has(name) {
			problems = append(problems, fmt.Sprintf("workload %s has no container %s with an image on a mirror repository", name, name))
		}
	}
	if len(problems) > 0 {
		return false, strings.Join(problems, "; "), nil
	}
	return true, "", nil
}

// registryOverridesPrimaryContainer returns the container named after the
// Deployment.
func registryOverridesPrimaryContainer(deployment *appsv1.Deployment) (*corev1.Container, error) {
	for i := range deployment.Spec.Template.Spec.Containers {
		if deployment.Spec.Template.Spec.Containers[i].Name == deployment.Name {
			return &deployment.Spec.Template.Spec.Containers[i], nil
		}
	}
	return nil, fmt.Errorf("deployment %s/%s has no container %s", deployment.Namespace, deployment.Name, deployment.Name)
}

// registryOverridesCurrentPods returns the names of the pods of the current
// ReplicaSet that run with the expected overrides: labeled with templateHash
// (pod-template-hash), not terminating, Ready, and with a container named
// containerName that carries --registry-overrides with exactly the expected
// mapping (in Command or Args). A malformed flag value is returned as an error.
func registryOverridesCurrentPods(pods []corev1.Pod, templateHash, containerName string, expected map[string]string) ([]string, error) {
	if templateHash == "" {
		return nil, fmt.Errorf("no pod template hash to select the current pods")
	}
	var names []string
	for i := range pods {
		pod := &pods[i]
		if pod.DeletionTimestamp != nil || pod.Labels[appsv1.DefaultDeploymentUniqueLabelKey] != templateHash {
			continue
		}
		ready := slices.ContainsFunc(pod.Status.Conditions, func(c corev1.PodCondition) bool {
			return c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue
		})
		if !ready {
			continue
		}
		idx := slices.IndexFunc(pod.Spec.Containers, func(c corev1.Container) bool { return c.Name == containerName })
		if idx < 0 {
			continue
		}
		container := pod.Spec.Containers[idx]
		value, found, err := registryOverridesArgValue(append(slices.Clone(container.Command), container.Args...))
		if err != nil {
			return nil, fmt.Errorf("pod %s/%s container %s: %w", pod.Namespace, pod.Name, containerName, err)
		}
		if !found {
			continue
		}
		got, err := registryOverridesParseArg(value)
		if err != nil {
			return nil, fmt.Errorf("pod %s/%s container %s: %w", pod.Namespace, pod.Name, containerName, err)
		}
		if registryOverridesFormatArg(got) == registryOverridesFormatArg(expected) {
			names = append(names, pod.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// registryOverridesLogContainsText reads the log stream r line by line and
// reports whether any line contains text. Lines up to 1 MiB are supported;
// a longer line or a read error is returned as an error.
func registryOverridesLogContainsText(r io.Reader, text string) (bool, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), text) {
			return true, nil
		}
	}
	return false, scanner.Err()
}

// registryOverridesCheckPayloadsGenerated reports whether the cluster has at
// least one NodePool and every one reports ValidGeneratedPayload True. The
// condition alone doesn't tell which ignition-server generated the payload; the
// caller pairs it with the log of the ignition-server pods that run with the
// overrides.
func registryOverridesCheckPayloadsGenerated(nodePools []hyperv1.NodePool, clusterName string) (bool, string) {
	var problems []string
	count := 0
	for i := range nodePools {
		np := &nodePools[i]
		if np.Spec.ClusterName != clusterName {
			continue
		}
		count++
		idx := slices.IndexFunc(np.Status.Conditions, func(c hyperv1.NodePoolCondition) bool {
			return c.Type == hyperv1.NodePoolValidGeneratedPayloadConditionType
		})
		if idx < 0 {
			problems = append(problems, fmt.Sprintf("NodePool %s/%s has no %s condition", np.Namespace, np.Name, hyperv1.NodePoolValidGeneratedPayloadConditionType))
			continue
		}
		if c := np.Status.Conditions[idx]; c.Status != corev1.ConditionTrue {
			problems = append(problems, fmt.Sprintf("NodePool %s/%s %s is %s: %s: %s", np.Namespace, np.Name, c.Type, c.Status, c.Reason, c.Message))
		}
	}
	if count == 0 {
		return false, fmt.Sprintf("no NodePool for cluster %s", clusterName)
	}
	if len(problems) > 0 {
		return false, strings.Join(problems, "; ")
	}
	return true, ""
}

// registryOverridesCheckDaemonSet reports whether the container named after
// the DaemonSet runs image and the DaemonSet has rolled out on at least one
// node: the latest spec is observed and every scheduled pod is updated and
// available. image must be on a mirror repository of overrides, otherwise the
// check would prove nothing about the mirror and an error is returned.
func registryOverridesCheckDaemonSet(daemonSet *appsv1.DaemonSet, image string, overrides map[string]string) (bool, string, error) {
	if daemonSet == nil {
		return false, "", fmt.Errorf("daemonSet is nil")
	}
	onMirror := false
	for _, target := range overrides {
		if registryOverridesMatchesPrefix(image, target) {
			onMirror = true
			break
		}
	}
	if !onMirror {
		return false, "", fmt.Errorf("image %s is not on a mirror repository", image)
	}
	idx := slices.IndexFunc(daemonSet.Spec.Template.Spec.Containers, func(c corev1.Container) bool { return c.Name == daemonSet.Name })
	if idx < 0 {
		return false, fmt.Sprintf("DaemonSet %s/%s has no container %s", daemonSet.Namespace, daemonSet.Name, daemonSet.Name), nil
	}
	if got := daemonSet.Spec.Template.Spec.Containers[idx].Image; got != image {
		return false, fmt.Sprintf("DaemonSet %s/%s container %s runs %s, want %s", daemonSet.Namespace, daemonSet.Name, daemonSet.Name, got, image), nil
	}
	status := daemonSet.Status
	switch {
	case status.ObservedGeneration < daemonSet.Generation:
		return false, fmt.Sprintf("DaemonSet %s/%s observed generation %d, want %d", daemonSet.Namespace, daemonSet.Name, status.ObservedGeneration, daemonSet.Generation), nil
	case status.DesiredNumberScheduled == 0:
		return false, fmt.Sprintf("DaemonSet %s/%s is scheduled on no node", daemonSet.Namespace, daemonSet.Name), nil
	case status.UpdatedNumberScheduled != status.DesiredNumberScheduled:
		return false, fmt.Sprintf("DaemonSet %s/%s has %d updated of %d scheduled pods", daemonSet.Namespace, daemonSet.Name, status.UpdatedNumberScheduled, status.DesiredNumberScheduled), nil
	case status.NumberAvailable != status.DesiredNumberScheduled || status.NumberUnavailable != 0:
		return false, fmt.Sprintf("DaemonSet %s/%s has %d available and %d unavailable of %d scheduled pods", daemonSet.Namespace, daemonSet.Name, status.NumberAvailable, status.NumberUnavailable, status.DesiredNumberScheduled), nil
	}
	return true, "", nil
}

// registryOverridesInput parses the value of registryOverridesInputEnv.
// configured is false when it is unset or empty, so the caller can skip; a
// non-empty value that is malformed or holds no override is an error.
func registryOverridesInput(value string) (overrides map[string]string, configured bool, err error) {
	if value == "" {
		return nil, false, nil
	}
	overrides, err = registryOverridesParseArg(value)
	if err != nil {
		return nil, true, fmt.Errorf("%s: %w", registryOverridesInputEnv, err)
	}
	if len(overrides) == 0 {
		return nil, true, fmt.Errorf("%s=%q holds no override", registryOverridesInputEnv, value)
	}
	return overrides, true, nil
}

// registryOverridesOperatorState returns the --registry-overrides mapping
// of the HyperShift operator Deployment and the operator image. found is false
// when the flag is absent. A malformed flag value is an error.
func registryOverridesOperatorState(deployment *appsv1.Deployment) (overrides map[string]string, found bool, image string, err error) {
	container, err := registryOverridesOperatorContainer(deployment)
	if err != nil {
		return nil, false, "", err
	}
	value, found, err := registryOverridesArgValue(append(slices.Clone(container.Command), container.Args...))
	if err != nil {
		return nil, false, "", fmt.Errorf("deployment %s/%s container %s: %w", deployment.Namespace, deployment.Name, container.Name, err)
	}
	if !found {
		return nil, false, container.Image, nil
	}
	overrides, err = registryOverridesParseArg(value)
	if err != nil {
		return nil, false, "", fmt.Errorf("deployment %s/%s container %s: %w", deployment.Namespace, deployment.Name, container.Name, err)
	}
	return overrides, true, container.Image, nil
}

// registryOverridesSyncerEligibleNodes returns the sorted names of the nodes
// the syncer DaemonSet can run on.
func registryOverridesSyncerEligibleNodes(nodes []corev1.Node) []string {
	var names []string
	for i := range nodes {
		labels := nodes[i].Labels
		_, karpenter := labels[registryOverridesSyncerKarpenterLabel]
		if labels[registryOverridesSyncerNodeLabel] == "true" || karpenter {
			names = append(names, nodes[i].Name)
		}
	}
	sort.Strings(names)
	return names
}

// registryOverridesDeploymentRevisionAnnotation is set by the Deployment
// controller on a Deployment and on its ReplicaSets to their revision.
const registryOverridesDeploymentRevisionAnnotation = "deployment.kubernetes.io/revision"

// registryOverridesCurrentTemplateHash returns the pod-template-hash of the
// current ReplicaSet of the Deployment: the one it owns at its own revision.
func registryOverridesCurrentTemplateHash(deployment *appsv1.Deployment, replicaSets []appsv1.ReplicaSet) (string, error) {
	if deployment == nil {
		return "", fmt.Errorf("deployment is nil")
	}
	revision := deployment.Annotations[registryOverridesDeploymentRevisionAnnotation]
	if revision == "" {
		return "", fmt.Errorf("deployment %s/%s has no %s annotation", deployment.Namespace, deployment.Name, registryOverridesDeploymentRevisionAnnotation)
	}
	for i := range replicaSets {
		rs := &replicaSets[i]
		owned := slices.ContainsFunc(rs.OwnerReferences, func(ref metav1.OwnerReference) bool { return ref.UID == deployment.UID })
		if !owned || rs.Annotations[registryOverridesDeploymentRevisionAnnotation] != revision {
			continue
		}
		hash := rs.Labels[appsv1.DefaultDeploymentUniqueLabelKey]
		if hash == "" {
			return "", fmt.Errorf("ReplicaSet %s/%s has no %s label", rs.Namespace, rs.Name, appsv1.DefaultDeploymentUniqueLabelKey)
		}
		return hash, nil
	}
	return "", fmt.Errorf("deployment %s/%s has no ReplicaSet at revision %s", deployment.Namespace, deployment.Name, revision)
}

// registryOverridesCheckReleaseCovered reports whether the HyperShift
// operator must have resolved the release of hc through the overrides: the
// release image (and the control plane release image, when set) starts with
// an overridden source, so RegistryMirrorProviderDecorator rewrites it to the
// mirror before the lookup, which has no fallback to the source. It also
// requires that hc doesn't pin the control plane operator image with the
// annotation, which bypasses the release.
func registryOverridesCheckReleaseCovered(hc *hyperv1.HostedCluster, overrides map[string]string) (bool, string) {
	if hc == nil {
		return false, "HostedCluster is nil"
	}
	if image, ok := hc.Annotations[hyperv1.ControlPlaneOperatorImageAnnotation]; ok {
		return false, fmt.Sprintf("HC %s/%s pins the control plane operator image %q with %s, which bypasses the release", hc.Namespace, hc.Name, image, hyperv1.ControlPlaneOperatorImageAnnotation)
	}
	refs := []string{hc.Spec.Release.Image}
	if hc.Spec.ControlPlaneRelease != nil {
		refs = append(refs, hc.Spec.ControlPlaneRelease.Image)
	}
	var problems []string
	for _, ref := range refs {
		covered := false
		for source := range overrides {
			if registryOverridesMatchesPrefix(ref, source) {
				covered = true
				break
			}
		}
		if !covered {
			problems = append(problems, fmt.Sprintf("HC %s/%s release image %q starts with no overridden source", hc.Namespace, hc.Name, ref))
		}
	}
	if len(problems) > 0 {
		return false, strings.Join(problems, "; ")
	}
	return true, ""
}

// registryOverridesCheckPodImages reports whether no container (init
// containers included) of the non-terminating pods still uses an overridden
// source. Images equal to the HO image are ignored, as in
// registryOverridesCheckImages. Images outside the sources are accepted: the
// overrides don't apply to them.
func registryOverridesCheckPodImages(pods []corev1.Pod, overrides map[string]string, hoImage string) (bool, string, error) {
	if len(overrides) == 0 {
		return false, "", fmt.Errorf("no registry overrides to check against")
	}
	var problems []string
	for i := range pods {
		pod := &pods[i]
		if pod.DeletionTimestamp != nil {
			continue
		}
		for _, container := range append(slices.Clone(pod.Spec.InitContainers), pod.Spec.Containers...) {
			if container.Image == hoImage {
				continue
			}
			for source := range overrides {
				if registryOverridesMatchesPrefix(container.Image, source) {
					problems = append(problems, fmt.Sprintf("pod %s/%s container %s still uses %s", pod.Namespace, pod.Name, container.Name, container.Image))
					break
				}
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return false, strings.Join(problems, "; "), nil
	}
	return true, "", nil
}

// registryOverridesPolicyMirrorsEnv is the CPO Deployment env var where the
// HyperShift operator writes the management cluster IDMS/ICSP mirrors it uses
// ("src1=dst1,src2=dst2", "=" when there are none).
const registryOverridesPolicyMirrorsEnv = "OPENSHIFT_IMG_OVERRIDES"

// registryOverridesCheckNoPolicyMirror reports whether no management cluster
// IDMS/ICSP mirror applies to the release of hc. The HyperShift operator wraps
// RegistryMirrorProviderDecorator in a decorator that first tries the
// IDMS/ICSP mirrors whose source the release image contains; when one
// applies, the release can be served by that mirror instead of through the
// overrides. The mirrors are read from registryOverridesPolicyMirrorsEnv on
// the primary container of the CPO Deployment; a missing variable is an
// error, because the check can't be made.
func registryOverridesCheckNoPolicyMirror(hc *hyperv1.HostedCluster, cpo *appsv1.Deployment) (bool, string, error) {
	if hc == nil || cpo == nil {
		return false, "", fmt.Errorf("HostedCluster and CPO Deployment must not be nil")
	}
	container, err := registryOverridesPrimaryContainer(cpo)
	if err != nil {
		return false, "", err
	}
	idx := slices.IndexFunc(container.Env, func(e corev1.EnvVar) bool { return e.Name == registryOverridesPolicyMirrorsEnv })
	if idx < 0 {
		return false, "", fmt.Errorf("deployment %s/%s container %s has no %s", cpo.Namespace, cpo.Name, container.Name, registryOverridesPolicyMirrorsEnv)
	}
	value := container.Env[idx].Value
	refs := []string{hc.Spec.Release.Image}
	if hc.Spec.ControlPlaneRelease != nil {
		refs = append(refs, hc.Spec.ControlPlaneRelease.Image)
	}
	var problems []string
	for _, pair := range strings.Split(value, ",") {
		source, mirror, _ := strings.Cut(pair, "=")
		if source == "" || mirror == "" {
			continue
		}
		for _, ref := range refs {
			if strings.Contains(ref, source) {
				problems = append(problems, fmt.Sprintf("management cluster mirror %s=%s applies to HC %s/%s release image %q", source, mirror, hc.Namespace, hc.Name, ref))
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return false, strings.Join(problems, "; "), nil
	}
	return true, "", nil
}
