package kas

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/imageregistry"
	"github.com/openshift/hypershift/support/podspec"
	dockerv1client "github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

const (
	// ReportedVersionConfigMapName is the name of the ConfigMap in the HCP
	// namespace that carries the reported kube-apiserver version.Info.
	ReportedVersionConfigMapName = "kas-reported-version"

	// ReportedVersionFileKey is the ConfigMap key (and file name) holding the
	// JSON-serialized version.Info consumed via the KAS_REPORTED_VERSION_FILE
	// environment variable.
	ReportedVersionFileKey = "version.json"

	// ReportedVersionFileEnvVar is the environment variable, read by the
	// kube-apiserver binary in the openshift/kubernetes fork, pointing at a JSON
	// file whose contents replace the version.Info served by /version. An env
	// var is used instead of a command line flag so that control plane payload
	// images built before the fork support existed simply ignore it instead of
	// failing to start on an unknown flag.
	ReportedVersionFileEnvVar = "KAS_REPORTED_VERSION_FILE"

	// reportedVersionVolumeName is the name of the ConfigMap volume mounted into
	// the kube-apiserver pod when version masking is enabled.
	reportedVersionVolumeName = "kas-reported-version"

	// reportedVersionMountPath is the mount path of the reported version
	// ConfigMap inside the kube-apiserver container.
	reportedVersionMountPath = "/etc/kubernetes/kas-reported-version"

	// hyperkubeComponentKey is the release payload component key of the
	// kube-apiserver image.
	hyperkubeComponentKey = "hyperkube"

	// kubernetesVersionComponentKey is the component versions key that carries
	// the Kubernetes version of a release payload.
	kubernetesVersionComponentKey = "kubernetes"

	// buildLabels of OpenShift component images, used to reconstruct the
	// version.Info of the kube-apiserver binary shipped in a payload.
	buildVersionsLabel   = "io.openshift.build.versions"
	buildCommitIDLabel   = "io.openshift.build.commit.id"
	buildCommitDateLabel = "io.openshift.build.commit.date"
	buildGoVersionLabel  = "io.openshift.build.go-version"
	buildPlatformLabel   = "io.openshift.build.platform"
)

// kasReportedVersionInfo is the single decision point for kube-apiserver
// /version masking (see the KAS /version masking design). It engages only when
// spec.controlPlaneReleaseImage is set and differs from spec.release (payload
// skew). In that case it returns the user payload's kube-apiserver
// version.Info, which CPO delivers to the control plane kube-apiserver so that
// /version keeps reporting the user payload's version. When masking is not
// engaged it returns (nil, false, nil).
func kasReportedVersionInfo(cpContext component.WorkloadContext) (*version.Info, bool, error) {
	hcp := cpContext.HCP
	if !kasVersionSkew(hcp) {
		return nil, false, nil
	}
	if cpContext.ImageMetadataProvider == nil {
		return nil, false, nil
	}

	// Mask only same-minor skew. A cross-minor skew would tell every kubelet
	// and client the server is on an older minor than it really is, defeating
	// the kubelet/client-go skew safeguards that key off the minor. There the
	// honest control plane version must be served instead, keeping the skew
	// visible on /version.
	sameMinor, err := sameKubeMinorVersion(cpContext)
	if err != nil {
		return nil, false, err
	}
	if !sameMinor {
		return nil, false, nil
	}

	info, err := userPayloadKASVersionInfo(cpContext)
	if err != nil {
		return nil, false, fmt.Errorf("extracting the kube-apiserver version info of the user payload %s: %w", hcp.Spec.ReleaseImage, err)
	}
	return info, true, nil
}

// kasVersionSkew reports whether the control plane release payload differs
// from the user-facing (spec.release) payload.
func kasVersionSkew(hcp *hyperv1.HostedControlPlane) bool {
	return hcp.Spec.ControlPlaneReleaseImage != nil && *hcp.Spec.ControlPlaneReleaseImage != hcp.Spec.ReleaseImage
}

// sameKubeMinorVersion reports whether the control plane and user payloads
// ship the same Kubernetes major and minor version, comparing the kubernetes
// component version of both payloads.
func sameKubeMinorVersion(cpContext component.WorkloadContext) (bool, error) {
	controlPlaneVersion, err := kubeVersionOf(cpContext.ReleaseImageProvider.ComponentVersions)
	if err != nil {
		return false, fmt.Errorf("failed to get the control plane payload kubernetes version: %w", err)
	}
	userVersion, err := kubeVersionOf(cpContext.UserReleaseImageProvider.ComponentVersions)
	if err != nil {
		return false, fmt.Errorf("failed to get the user payload kubernetes version: %w", err)
	}
	return controlPlaneVersion.Major == userVersion.Major && controlPlaneVersion.Minor == userVersion.Minor, nil
}

// kubeVersionOf parses the kubernetes component version of a release payload.
func kubeVersionOf(componentVersions func() (map[string]string, error)) (semver.Version, error) {
	versions, err := componentVersions()
	if err != nil {
		return semver.Version{}, err
	}
	kubeVersion, ok := versions[kubernetesVersionComponentKey]
	if !ok || kubeVersion == "" {
		return semver.Version{}, fmt.Errorf("payload does not report a %q component version", kubernetesVersionComponentKey)
	}
	parsed, err := semver.Parse(kubeVersion)
	if err != nil {
		return semver.Version{}, fmt.Errorf("invalid kubernetes version %q: %w", kubeVersion, err)
	}
	return parsed, nil
}

// userPayloadKASVersionInfo reconstructs the version.Info of the kube-apiserver
// binary shipped in the user payload (spec.release). It is assembled once per
// reconcile from payload metadata (component versions and image build labels);
// the underlying image metadata lookups are cached by the
// ImageMetadataProvider, so this does not become a per-reconcile registry
// round-trip after the first lookup.
func userPayloadKASVersionInfo(cpContext component.WorkloadContext) (*version.Info, error) {
	componentVersions, err := cpContext.UserReleaseImageProvider.ComponentVersions()
	if err != nil {
		return nil, fmt.Errorf("failed to get component versions of the user payload: %w", err)
	}
	kubeVersion, ok := componentVersions[kubernetesVersionComponentKey]
	if !ok || kubeVersion == "" {
		return nil, fmt.Errorf("user payload does not report a %q component version", kubernetesVersionComponentKey)
	}

	kasImage := cpContext.UserReleaseImageProvider.GetImage(hyperkubeComponentKey)
	if kasImage == "" {
		return nil, fmt.Errorf("user payload has no %s component image", hyperkubeComponentKey)
	}

	pullSecret, err := hcpPullSecretBytes(cpContext)
	if err != nil {
		return nil, err
	}

	metadata, err := cpContext.ImageMetadataProvider.ImageMetadata(cpContext, kasImage, pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get image metadata for user payload kube-apiserver image %s: %w", kasImage, err)
	}

	// Prefer the kubernetes version pinned on the image itself; fall back to
	// the payload's component versions.
	if v, ok := kubernetesVersionFromBuildVersionsLabel(imageregistry.ImageLabels(metadata)[buildVersionsLabel]); ok {
		kubeVersion = v
	}

	return kasVersionInfo(kubeVersion, metadata)
}

// kubernetesVersionFromBuildVersionsLabel parses a comma-separated
// key=version build label (e.g. "kubernetes=1.34.2,...") and returns the
// kubernetes version, if present.
func kubernetesVersionFromBuildVersionsLabel(label string) (string, bool) {
	for _, pair := range strings.Split(label, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(pair), "=")
		if found && strings.TrimSpace(key) == kubernetesVersionComponentKey {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// kasVersionInfo builds the version.Info served by the masked /version
// endpoint from the Kubernetes version of the user payload and the build
// labels of its kube-apiserver image. It fails closed: incomplete input is an
// error rather than a partial mask.
func kasVersionInfo(kubeVersion string, metadata *dockerv1client.DockerImageConfig) (*version.Info, error) {
	parsed, err := semver.Parse(kubeVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid kubernetes version %q: %w", kubeVersion, err)
	}

	labels := imageregistry.ImageLabels(metadata)
	gitCommit := labels[buildCommitIDLabel]
	if gitCommit == "" {
		return nil, fmt.Errorf("kube-apiserver image is missing the %s build label", buildCommitIDLabel)
	}

	buildDate := labels[buildCommitDateLabel]
	if buildDate == "" && metadata != nil && !metadata.Created.IsZero() {
		buildDate = metadata.Created.UTC().Format(time.RFC3339)
	}
	if buildDate == "" {
		return nil, fmt.Errorf("kube-apiserver image is missing the %s build label and image creation timestamp", buildCommitDateLabel)
	}

	goVersion := labels[buildGoVersionLabel]
	if goVersion == "" {
		goVersion = runtime.Version()
	}

	platform := labels[buildPlatformLabel]
	if platform == "" && metadata != nil && metadata.OS != "" && metadata.Architecture != "" {
		platform = fmt.Sprintf("%s/%s", metadata.OS, metadata.Architecture)
	}
	if platform == "" {
		platform = fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	}

	return &version.Info{
		Major:        strconv.FormatUint(parsed.Major, 10),
		Minor:        strconv.FormatUint(parsed.Minor, 10),
		GitVersion:   "v" + parsed.String(),
		GitCommit:    gitCommit,
		GitTreeState: "clean",
		BuildDate:    buildDate,
		GoVersion:    goVersion,
		Compiler:     "gc",
		Platform:     platform,
	}, nil
}

// hcpPullSecretBytes reads the pull secret in the HCP namespace, used to
// authenticate image metadata lookups.
func hcpPullSecretBytes(cpContext component.WorkloadContext) ([]byte, error) {
	pullSecret := common.PullSecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return nil, fmt.Errorf("failed to get pull secret in namespace %s: %w", cpContext.HCP.Namespace, err)
	}
	return pullSecret.Data[corev1.DockerConfigJsonKey], nil
}

// reportedVersionEnabled is the manifest adapter predicate for the
// kas-reported-version ConfigMap. Extraction errors keep the ConfigMap in
// place: the kube-apiserver deployment mounts it non-optionally while masking
// is engaged, so deleting it on a transient registry or metadata failure
// would prevent replacement pods from starting. The adapt function surfaces
// the error instead, and cleanup still runs when extraction succeeds and
// masking is disabled.
//
// When masking is disabled, cleanup of an existing ConfigMap is additionally
// deferred until the kube-apiserver deployment no longer mounts it: manifest
// adapters run before the workload reconcile updates the deployment, so
// deleting it eagerly would leave a window where replacement pods cannot
// start. While the deployment still mounts it, the ConfigMap keeps its
// current content (see adaptReportedVersionConfigMap) and the deployment
// update removes the mount; a subsequent reconcile deletes the ConfigMap.
// Deferral only ever retains an existing ConfigMap: it never creates one for
// a control plane that is not masking.
func reportedVersionEnabled(cpContext component.WorkloadContext) bool {
	_, enabled, err := kasReportedVersionInfo(cpContext)
	if err != nil {
		return true
	}
	if enabled {
		return true
	}
	exists, err := reportedVersionConfigMapExists(cpContext)
	if err != nil {
		// Conservative: keep the ConfigMap when its existence cannot be
		// determined. adaptReportedVersionConfigMap refuses to apply it
		// unless its existing content was loaded.
		return true
	}
	if !exists {
		return false
	}
	mounted, err := kasMountsReportedVersionConfigMap(cpContext)
	if err != nil {
		// Conservative: keep the ConfigMap when its consumer cannot be
		// inspected.
		return true
	}
	return mounted
}

// reportedVersionConfigMapExists reports whether the kas-reported-version
// ConfigMap exists in the HCP namespace.
func reportedVersionConfigMapExists(cpContext component.WorkloadContext) (bool, error) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ReportedVersionConfigMapName,
			Namespace: cpContext.HCP.Namespace,
		},
	}
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(cm), cm); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// kasMountsReportedVersionConfigMap reports whether any kube-apiserver pod
// can still be created with the kas-reported-version ConfigMap mounted:
// either the current deployment template mounts it, or the rollout away from
// a template that mounts it has not completed yet and the old ReplicaSet can
// still recreate pods.
func kasMountsReportedVersionConfigMap(cpContext component.WorkloadContext) (bool, error) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ComponentName,
			Namespace: cpContext.HCP.Namespace,
		},
	}
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(deployment), deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.Name == reportedVersionVolumeName {
			return true, nil
		}
	}
	return !kasRolloutComplete(cpContext, deployment), nil
}

// kasRolloutComplete reports whether the deployment's rollout has finished and
// no ReplicaSet with an older template can create pods anymore.
//
// Every count must equal the desired replica count, not merely reach it.
// updatedReplicas counts pods on the current template including unready ones,
// so during a surge rollout it can equal the desired count while an old
// ReplicaSet is still scaled up and able to recreate a pod that mounts the
// ConfigMap. The kube-apiserver deployment does surge: its manifest sets
// maxSurge to 25%, which with one replica permits one extra pod, and the two
// replica request serving topology sets maxSurge to one.
//
// podspec.IsDeploymentReady is the exact check. Beyond matching replicas,
// updatedReplicas and availableReplicas it also requires readyReplicas to
// match and unavailableReplicas to be zero. That last field is what closes
// the remaining window: unavailableReplicas is derived from the sum of the
// ReplicaSets' desired replicas minus the available ones, so it stays
// positive while an old ReplicaSet is still scaled up even after its pod has
// gone away. Without it, a single replica rollout whose old pod was evicted
// reports replicas, updatedReplicas and availableReplicas all at one while
// the old ReplicaSet is still about to recreate a pod that mounts the
// ConfigMap non-optionally.
func kasRolloutComplete(ctx context.Context, deployment *appsv1.Deployment) bool {
	return podspec.IsDeploymentReady(ctx, deployment)
}

// adaptReportedVersionConfigMap fills the kas-reported-version ConfigMap with
// the user payload's kube-apiserver version.Info. If extraction fails, the
// error aborts reconciliation: there must never be a
// KAS_REPORTED_VERSION_FILE env var without a ConfigMap, or a ConfigMap
// without a complete version.Info. When masking is not engaged, existing
// content is preserved: the ConfigMap is only deleted once the
// kube-apiserver deployment no longer mounts it (see
// reportedVersionEnabled), and until then its consumer must keep finding a
// complete version.Info.
func adaptReportedVersionConfigMap(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	info, enabled, err := kasReportedVersionInfo(cpContext)
	if err != nil {
		return err
	}
	if !enabled {
		// The predicate keeps an existing ConfigMap while the deployment still
		// mounts it; preserve its current content. The predicate deletes the
		// ConfigMap in every other not-enabled case.
		//
		// The generic adapter passes the bare manifest when it fails to load
		// the existing object (ReconcileExisting ignores read errors). Never
		// apply that: it would overwrite the retained version.Info with an
		// empty ConfigMap. Fail and retry instead.
		if cm.Data[ReportedVersionFileKey] == "" {
			return fmt.Errorf("%s ConfigMap is retained until the kube-apiserver deployment stops mounting it, but its existing content could not be loaded", ReportedVersionConfigMapName)
		}
		return nil
	}

	raw, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to serialize reported kube-apiserver version info: %w", err)
	}
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[ReportedVersionFileKey] = string(raw)
	return nil
}

// applyReportedVersionConfig mounts the kas-reported-version ConfigMap into
// the kube-apiserver container and sets KAS_REPORTED_VERSION_FILE pointing at
// it, but only when version masking is engaged. The rendered pod spec is
// otherwise byte-for-byte identical to the unmasked deployment. Control plane
// payload images that do not yet read the env var simply ignore it, so they
// keep reporting their own version on /version instead of failing to start.
func applyReportedVersionConfig(cpContext component.WorkloadContext, podSpec *corev1.PodSpec) error {
	_, enabled, err := kasReportedVersionInfo(cpContext)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}

	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: reportedVersionVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: ReportedVersionConfigMapName},
			},
		},
	})

	podspec.UpdateContainer(ComponentName, podSpec.Containers, func(c *corev1.Container) {
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      reportedVersionVolumeName,
			MountPath: reportedVersionMountPath,
		})
		c.Env = append(c.Env, corev1.EnvVar{
			Name:  ReportedVersionFileEnvVar,
			Value: fmt.Sprintf("%s/%s", reportedVersionMountPath, ReportedVersionFileKey),
		})
	})

	return nil
}
