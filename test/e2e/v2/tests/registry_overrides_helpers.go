//go:build e2ev2

package tests

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/util/mirrorregistry"

	imagev1 "github.com/openshift/api/image/v1"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// registryOverridesNamespace is the namespace of the mirror deployed by
	// these tests. It differs from the PreCreate mirror (DefaultNamespace), so
	// the CA supplied by the job is always used and the two never interfere.
	registryOverridesNamespace = "test-mirror-registry-ho"

	// registryOverridesStorageSize holds two full release payloads (~22 GiB
	// each, for example N-1 and N in the upgrade lane) with headroom; layers
	// shared by the payloads are stored once.
	registryOverridesStorageSize = "80Gi"

	// registryOverridesReleaseMirrorRepository is the repository on the mirror
	// host that receives every mirrored payload, so that every source
	// repository has exactly one override target.
	registryOverridesReleaseMirrorRepository = "e2e/release"

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

	// registryOverridesTrustBundleName is the ConfigMap, in the HostedCluster
	// namespace, that holds the job CA as the HostedCluster
	// additionalTrustBundle, so the workers trust the mirror.
	registryOverridesTrustBundleName = "registry-overrides-mirror-ca"
	// registryOverridesTrustBundleKey is the key the HostedCluster
	// additionalTrustBundle ConfigMap must use.
	registryOverridesTrustBundleKey = "ca-bundle.crt"
	// registryOverridesNodePoolCurrentConfigAnnotation is set by the NodePool
	// controller to the target config hash once a config rollout completes.
	registryOverridesNodePoolCurrentConfigAnnotation = "hypershift.openshift.io/nodePoolCurrentConfig"
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

// registryOverridesLoadCA reads the mirror CA the job provides. It returns
// nil without error when neither path is set, so the caller can skip; one path
// without the other, an unreadable file or an empty file is an error.
func registryOverridesLoadCA(certPath, keyPath string) (*mirrorregistry.KeyPair, error) {
	if certPath == "" && keyPath == "" {
		return nil, nil
	}
	if certPath == "" || keyPath == "" {
		return nil, fmt.Errorf("the registry overrides CA needs both a certificate and a key file, got certificate %q and key %q", certPath, keyPath)
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("read registry overrides CA certificate: %w", err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read registry overrides CA key: %w", err)
	}
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		return nil, fmt.Errorf("registry overrides CA files %s and %s must not be empty", certPath, keyPath)
	}
	return &mirrorregistry.KeyPair{CertPEM: certPEM, KeyPEM: keyPEM}, nil
}

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

// registryOverridesFromResult builds the HO overrides from the mirror result:
// every mirrored source repository maps to its only mirror repository, which
// must be on the mirror host. These are the inputs of the test; the
// assertions use the HO arg read back from the cluster, not this result.
func registryOverridesFromResult(result *mirrorregistry.Result) (map[string]string, error) {
	if result == nil || result.Host == "" {
		return nil, fmt.Errorf("mirror result has no host")
	}
	if len(result.ImageContentSources) == 0 {
		return nil, fmt.Errorf("mirror result has no image content sources")
	}
	overrides := map[string]string{}
	for _, ics := range result.ImageContentSources {
		if len(ics.Mirrors) != 1 {
			return nil, fmt.Errorf("mirror source %q has %d mirrors, want exactly 1", ics.Source, len(ics.Mirrors))
		}
		mirror := ics.Mirrors[0]
		if !strings.HasPrefix(mirror, result.Host+"/") {
			return nil, fmt.Errorf("mirror %q of source %q is not a repository on %s", mirror, ics.Source, result.Host)
		}
		if _, dup := overrides[ics.Source]; dup {
			return nil, fmt.Errorf("duplicate mirror source %q", ics.Source)
		}
		overrides[ics.Source] = mirror
	}
	return overrides, nil
}

// registryOverridesReleaseImages returns the sorted, de-duplicated release
// images of every HostedCluster, plus latest when it is not empty: the
// release an upgrade step of the lane moves a cluster to, so it is mirrored
// before the operator is patched. The overrides are global to the HO and have
// no fallback, so every release in use must be mirrored. The fixture mirrors
// by digest, so tag references are rejected.
func registryOverridesReleaseImages(hcs []hyperv1.HostedCluster, latest string) ([]string, error) {
	if len(hcs) == 0 {
		return nil, fmt.Errorf("no HostedClusters on the management cluster")
	}
	images := sets.New[string]()
	if latest != "" {
		if !strings.Contains(latest, "@sha256:") {
			return nil, fmt.Errorf("latest release image %q is not referenced by digest", latest)
		}
		images.Insert(latest)
	}
	for _, hc := range hcs {
		refs := []string{hc.Spec.Release.Image}
		if hc.Spec.ControlPlaneRelease != nil {
			refs = append(refs, hc.Spec.ControlPlaneRelease.Image)
		}
		for _, ref := range refs {
			if !strings.Contains(ref, "@sha256:") {
				return nil, fmt.Errorf("HostedCluster %s/%s release image %q is not referenced by digest", hc.Namespace, hc.Name, ref)
			}
			images.Insert(ref)
		}
	}
	return sets.List(images), nil
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

// registryOverridesSetArg returns args with --registry-overrides set to value,
// replacing any existing occurrence in either form. args is not modified.
func registryOverridesSetArg(args []string, value string) []string {
	result := make([]string, 0, len(args)+1)
	for i := 0; i < len(args); i++ {
		switch {
		case strings.HasPrefix(args[i], registryOverridesFlag+"="):
			continue
		case args[i] == registryOverridesFlag:
			i++ // drop the value too
			continue
		}
		result = append(result, args[i])
	}
	return append(result, registryOverridesFlag+"="+value)
}

// registryOverridesApplied reports whether the HO already points every
// override at the mirror Route host, so a rerun of the setup can skip the
// payload copy and the HO rollout.
func registryOverridesApplied(current map[string]string, routeHost string) bool {
	if len(current) == 0 || routeHost == "" {
		return false
	}
	for _, target := range current {
		if !strings.HasPrefix(target, routeHost+"/") {
			return false
		}
	}
	return true
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

// registryOverridesNeedsTrustBundlePatch reports whether the HostedCluster
// must be patched to use name as its additionalTrustBundle: true when it has
// none, false when it already uses name. Another bundle is an error: the job
// would have to merge CAs, and the variant is expected to have none.
func registryOverridesNeedsTrustBundlePatch(hc *hyperv1.HostedCluster, name string) (bool, error) {
	if hc == nil {
		return false, fmt.Errorf("hostedCluster is nil")
	}
	switch {
	case hc.Spec.AdditionalTrustBundle == nil:
		return true, nil
	case hc.Spec.AdditionalTrustBundle.Name == name:
		return false, nil
	}
	return false, fmt.Errorf("HC %s/%s already has additionalTrustBundle %s, want none or %s", hc.Namespace, hc.Name, hc.Spec.AdditionalTrustBundle.Name, name)
}

// registryOverridesNodePoolConfigs returns the current config hash annotation
// of every NodePool of the cluster, keyed by NodePool name.
func registryOverridesNodePoolConfigs(nodePools []hyperv1.NodePool, clusterName string) map[string]string {
	configs := map[string]string{}
	for i := range nodePools {
		if nodePools[i].Spec.ClusterName == clusterName {
			configs[nodePools[i].Name] = nodePools[i].Annotations[registryOverridesNodePoolCurrentConfigAnnotation]
		}
	}
	return configs
}

// registryOverridesCheckNodePoolsRolledOut reports whether every NodePool of
// the cluster has completed its config rollout and is at its replica count:
// the current config annotation differs from its baseline value (when baseline
// has one for the NodePool), UpdatingConfig is not True, and the status
// replicas match the spec. A nil baseline only checks that no rollout is in
// progress. The cluster must have at least one NodePool.
func registryOverridesCheckNodePoolsRolledOut(nodePools []hyperv1.NodePool, clusterName string, baseline map[string]string) (bool, string) {
	var problems []string
	count := 0
	for i := range nodePools {
		np := &nodePools[i]
		if np.Spec.ClusterName != clusterName {
			continue
		}
		count++
		if previous, ok := baseline[np.Name]; ok && np.Annotations[registryOverridesNodePoolCurrentConfigAnnotation] == previous {
			problems = append(problems, fmt.Sprintf("NodePool %s/%s has not completed the config rollout", np.Namespace, np.Name))
			continue
		}
		if slices.ContainsFunc(np.Status.Conditions, func(c hyperv1.NodePoolCondition) bool {
			return c.Type == hyperv1.NodePoolUpdatingConfigConditionType && c.Status == corev1.ConditionTrue
		}) {
			problems = append(problems, fmt.Sprintf("NodePool %s/%s is updating its config", np.Namespace, np.Name))
			continue
		}
		if np.Spec.Replicas != nil && np.Status.Replicas != *np.Spec.Replicas {
			problems = append(problems, fmt.Sprintf("NodePool %s/%s has %d of %d replicas", np.Namespace, np.Name, np.Status.Replicas, *np.Spec.Replicas))
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

// registryOverridesCurrentPods returns the names of the pods that run the
// latest spec with the expected overrides: not terminating, Ready, and with a
// container named containerName that carries --registry-overrides with exactly
// the expected mapping (in Command or Args). A malformed flag value is returned
// as an error.
func registryOverridesCurrentPods(pods []corev1.Pod, containerName string, expected map[string]string) ([]string, error) {
	var names []string
	for i := range pods {
		pod := &pods[i]
		if pod.DeletionTimestamp != nil {
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

// registryOverridesLogHasLine reports whether a log stream has a line
// containing line.
func registryOverridesLogHasLine(r io.Reader, line string) (bool, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), line) {
			return true, nil
		}
	}
	return false, scanner.Err()
}

// registryOverridesCheckPayloadsGenerated reports whether the cluster has at
// least one NodePool and every one reports ValidGeneratedPayload True. The
// condition alone doesn't tell when the payload was generated; the caller pairs
// it with the log of the restarted ignition-server pods.
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

// registryOverridesCLIImage returns the "oc" image for the mirror copy Job:
// the imported latest tag of the openshift/cli ImageStream, by digest.
func registryOverridesCLIImage(ctx context.Context, client crclient.Client) (string, error) {
	stream := &imagev1.ImageStream{}
	if err := client.Get(ctx, crclient.ObjectKey{Namespace: "openshift", Name: "cli"}, stream); err != nil {
		return "", fmt.Errorf("read ImageStream openshift/cli: %w", err)
	}
	for _, tag := range stream.Status.Tags {
		if tag.Tag == "latest" && len(tag.Items) > 0 && strings.Contains(tag.Items[0].DockerImageReference, "@sha256:") {
			return tag.Items[0].DockerImageReference, nil
		}
	}
	return "", fmt.Errorf("ImageStream openshift/cli has no imported latest tag by digest")
}

// registryOverridesPullSecret returns the .dockerconfigjson of the HC pull
// secret, used to read the release payload from its source.
func registryOverridesPullSecret(ctx context.Context, client crclient.Client, hc *hyperv1.HostedCluster) ([]byte, error) {
	if hc == nil || hc.Spec.PullSecret.Name == "" {
		return nil, fmt.Errorf("HostedCluster has no pull secret")
	}
	secret := &corev1.Secret{}
	key := crclient.ObjectKey{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}
	if err := client.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("read pull secret %s: %w", key, err)
	}
	data := secret.Data[corev1.DockerConfigJsonKey]
	if len(data) == 0 {
		return nil, fmt.Errorf("pull secret %s has no %s", key, corev1.DockerConfigJsonKey)
	}
	return data, nil
}
