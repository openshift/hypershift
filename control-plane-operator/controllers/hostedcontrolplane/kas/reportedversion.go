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
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	dockerv1client "github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/version"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

const (
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

	// reportedVersionHashAnnotation carries a hash of the reported version.Info
	// so that changing it rolls the kube-apiserver out. It is only set while
	// masking is engaged.
	reportedVersionHashAnnotation = "kube-apiserver.hypershift.openshift.io/reported-version-hash"

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

// ReportedVersionInfo is the single decision point for kube-apiserver /version
// masking (see the KAS /version masking design). It engages only when
// spec.controlPlaneReleaseImage is set and differs from spec.release (payload
// skew). In that case it returns the user payload's kube-apiserver
// version.Info, which CPO delivers to the control plane kube-apiserver so that
// /version keeps reporting the user payload's version. When masking is not
// engaged it returns (nil, false, nil).
func ReportedVersionInfo(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane, releaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider, imageMetadataProvider util.ImageMetadataProvider) (*version.Info, bool, error) {
	if !kasVersionSkew(hcp) {
		return nil, false, nil
	}
	if imageMetadataProvider == nil {
		return nil, false, nil
	}

	// Mask only same-minor skew. A cross-minor skew would tell every kubelet
	// and client the server is on an older minor than it really is, defeating
	// the kubelet/client-go skew safeguards that key off the minor. There the
	// honest control plane version must be served instead, keeping the skew
	// visible on /version.
	sameMinor, err := sameKubeMinorVersion(releaseImageProvider, userReleaseImageProvider)
	if err != nil {
		return nil, false, err
	}
	if !sameMinor {
		return nil, false, nil
	}

	info, err := userPayloadKASVersionInfo(ctx, c, hcp, userReleaseImageProvider, imageMetadataProvider)
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
func sameKubeMinorVersion(releaseImageProvider, userReleaseImageProvider imageprovider.ReleaseImageProvider) (bool, error) {
	controlPlaneVersion, err := kubeVersionOf(releaseImageProvider)
	if err != nil {
		return false, fmt.Errorf("failed to get the control plane payload kubernetes version: %w", err)
	}
	userVersion, err := kubeVersionOf(userReleaseImageProvider)
	if err != nil {
		return false, fmt.Errorf("failed to get the user payload kubernetes version: %w", err)
	}
	return controlPlaneVersion.Major == userVersion.Major && controlPlaneVersion.Minor == userVersion.Minor, nil
}

// kubeVersionOf parses the kubernetes component version of a release payload.
func kubeVersionOf(releaseImageProvider imageprovider.ReleaseImageProvider) (semver.Version, error) {
	versions, err := releaseImageProvider.ComponentVersions()
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
func userPayloadKASVersionInfo(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane, userReleaseImageProvider imageprovider.ReleaseImageProvider, imageMetadataProvider util.ImageMetadataProvider) (*version.Info, error) {
	componentVersions, err := userReleaseImageProvider.ComponentVersions()
	if err != nil {
		return nil, fmt.Errorf("failed to get component versions of the user payload: %w", err)
	}
	kubeVersion, ok := componentVersions[kubernetesVersionComponentKey]
	if !ok || kubeVersion == "" {
		return nil, fmt.Errorf("user payload does not report a %q component version", kubernetesVersionComponentKey)
	}

	kasImage := userReleaseImageProvider.GetImage(hyperkubeComponentKey)
	if kasImage == "" {
		return nil, fmt.Errorf("user payload has no %s component image", hyperkubeComponentKey)
	}

	pullSecret, err := hcpPullSecretBytes(ctx, c, hcp)
	if err != nil {
		return nil, err
	}

	metadata, err := imageMetadataProvider.ImageMetadata(ctx, kasImage, pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get image metadata for user payload kube-apiserver image %s: %w", kasImage, err)
	}

	// Prefer the kubernetes version pinned on the image itself; fall back to
	// the payload's component versions.
	if v, ok := kubernetesVersionFromBuildVersionsLabel(kasBuildLabels(metadata)[buildVersionsLabel]); ok {
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

// kasBuildLabels returns the build labels recorded in the given kube-apiserver
// image metadata. Depending on how the image was built its labels live on
// Config or on ContainerConfig, so Config.Labels is used only when it carries
// at least one label and ContainerConfig.Labels is the fallback. Reading
// Config.Labels unconditionally, as support/util.ImageLabels does, makes
// labels that only live on ContainerConfig read as absent, which fails the
// mask closed on a kube-apiserver image that records them there.
//
// The fallback is deliberately scoped to this lookup rather than applied to
// support/util.ImageLabels: that helper is shared with control plane operator
// capability detection in the HostedCluster, NodePool and sizing controllers,
// where widening the lookup would flip capability labels from absent to
// present for the same images and change rendered settings, including rolling
// NodePools once after a HyperShift operator upgrade.
func kasBuildLabels(metadata *dockerv1client.DockerImageConfig) map[string]string {
	if metadata == nil {
		return nil
	}
	if metadata.Config != nil && len(metadata.Config.Labels) > 0 {
		return metadata.Config.Labels
	}
	return metadata.ContainerConfig.Labels
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

	labels := kasBuildLabels(metadata)
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
func hcpPullSecretBytes(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane) ([]byte, error) {
	pullSecret := common.PullSecret(hcp.Namespace)
	if err := c.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return nil, fmt.Errorf("failed to get pull secret in namespace %s: %w", hcp.Namespace, err)
	}
	return pullSecret.Data[corev1.DockerConfigJsonKey], nil
}

// ReconcileReportedVersionConfigMap fills the kas-reported-version ConfigMap
// with the user payload's kube-apiserver version.Info. There must never be a
// KAS_REPORTED_VERSION_FILE env var without a ConfigMap, or a ConfigMap
// without a complete version.Info, so the caller only invokes this while
// masking is engaged.
func ReconcileReportedVersionConfigMap(cm *corev1.ConfigMap, ownerRef config.OwnerRef, info *version.Info) error {
	ownerRef.ApplyTo(cm)
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

// CleanupReportedVersionConfigMap deletes the kas-reported-version ConfigMap
// once masking is disabled, but only after the kube-apiserver deployment no
// longer mounts it and its rollout is complete. The deployment mounts the
// ConfigMap non-optionally while masking is engaged, so deleting it earlier
// would leave a window where replacement pods cannot start.
func CleanupReportedVersionConfigMap(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane) error {
	cm := manifests.KASReportedVersionConfigMap(hcp.Namespace)
	if err := c.Get(ctx, client.ObjectKeyFromObject(cm), cm); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get %s ConfigMap: %w", cm.Name, err)
	}

	deployment := manifests.KASDeployment(hcp.Namespace)
	if err := c.Get(ctx, client.ObjectKeyFromObject(deployment), deployment); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get kube-apiserver deployment: %w", err)
		}
	} else if kasMountsReportedVersionConfigMap(ctx, deployment) {
		return nil
	}

	if err := c.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete %s ConfigMap: %w", cm.Name, err)
	}
	return nil
}

// kasMountsReportedVersionConfigMap reports whether any kube-apiserver pod can
// still be created with the kas-reported-version ConfigMap mounted: either the
// current deployment template mounts it, or the rollout away from a template
// that mounts it has not completed yet and the old ReplicaSet can still
// recreate pods.
func kasMountsReportedVersionConfigMap(ctx context.Context, deployment *appsv1.Deployment) bool {
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.Name == reportedVersionVolumeName {
			return true
		}
	}
	return !kasRolloutComplete(ctx, deployment)
}

// kasRolloutComplete reports whether the deployment's rollout has finished and
// no ReplicaSet with an older template can create pods anymore.
//
// Every count must equal the desired replica count, not merely reach it.
// updatedReplicas counts pods on the current template including unready ones,
// so during a surge rollout it can equal the desired count while an old
// ReplicaSet is still scaled up and able to recreate a pod that mounts the
// ConfigMap. The kube-apiserver deployment does surge: DeploymentConfig only
// sets a rollout strategy when Replicas is greater than one, so a single
// replica falls back to Kubernetes' default 25% maxSurge, which rounds up to
// one, and the two replica request serving topology sets maxSurge to one
// explicitly.
//
// util.IsDeploymentReady is the exact check. Beyond matching replicas,
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
	return util.IsDeploymentReady(ctx, deployment)
}

// applyReportedVersionConfig mounts the kas-reported-version ConfigMap into
// the kube-apiserver container and sets KAS_REPORTED_VERSION_FILE pointing at
// it. It is only called when version masking is engaged; the rendered pod
// template is otherwise byte-for-byte identical to the unmasked deployment.
// Control plane payload images that do not yet read the env var simply ignore
// it, so they keep reporting their own version on /version instead of failing
// to start.
//
// The reported version is also hashed into a pod template annotation so that a
// change to the ConfigMap content rolls the kube-apiserver out: unlike the
// CPOv2 workload framework, which hashes every mounted ConfigMap into
// component.hypershift.openshift.io/config-hash, nothing else here ties the
// pod template to this ConfigMap's content.
func applyReportedVersionConfig(podTemplate *corev1.PodTemplateSpec, info *version.Info) error {
	podSpec := &podTemplate.Spec
	container := util.FindContainer(kasContainerMain().Name, podSpec.Containers)
	if container == nil {
		return fmt.Errorf("main kube-apiserver container not found in the pod spec")
	}

	raw, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to serialize reported kube-apiserver version info: %w", err)
	}
	if podTemplate.Annotations == nil {
		podTemplate.Annotations = map[string]string{}
	}
	podTemplate.Annotations[reportedVersionHashAnnotation] = util.ComputeHash(string(raw))

	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: reportedVersionVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: manifests.KASReportedVersionConfigMap("").Name,
				},
			},
		},
	})
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      reportedVersionVolumeName,
		MountPath: reportedVersionMountPath,
	})
	container.Env = append(container.Env, corev1.EnvVar{
		Name:  ReportedVersionFileEnvVar,
		Value: fmt.Sprintf("%s/%s", reportedVersionMountPath, ReportedVersionFileKey),
	})
	return nil
}
