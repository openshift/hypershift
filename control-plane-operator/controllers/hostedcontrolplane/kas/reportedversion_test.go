package kas

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/testutil"
	dockerv1client "github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"

	"github.com/openshift/api/image/docker10"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/docker/distribution"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// fakeImageMetadataProvider implements util.ImageMetadataProvider by serving
// canned image configs keyed by pull spec.
type fakeImageMetadataProvider struct {
	configs map[string]*dockerv1client.DockerImageConfig
}

type notFoundError struct{}

func (notFoundError) Error() string { return "not found" }

func (f *fakeImageMetadataProvider) ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {
	if config, ok := f.configs[imageRef]; ok {
		return config, nil
	}
	return nil, notFoundError{}
}

func (f *fakeImageMetadataProvider) GetManifest(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Manifest, error) {
	return nil, notFoundError{}
}

func (f *fakeImageMetadataProvider) GetDigest(ctx context.Context, imageRef string, pullSecret []byte) (digest.Digest, *reference.DockerImageReference, error) {
	return "", nil, notFoundError{}
}

func (f *fakeImageMetadataProvider) GetMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, []distribution.Descriptor, distribution.BlobStore, error) {
	config, err := f.ImageMetadata(ctx, imageRef, pullSecret)
	if err != nil {
		return nil, nil, nil, err
	}
	return config, nil, nil, nil
}

func (f *fakeImageMetadataProvider) GetOverride(ctx context.Context, imageRef string, pullSecret []byte) (*reference.DockerImageReference, error) {
	return nil, notFoundError{}
}

func kasImageMetadata(labels map[string]string, created metav1.Time) *dockerv1client.DockerImageConfig {
	return &dockerv1client.DockerImageConfig{
		Created:      created.Time,
		OS:           "linux",
		Architecture: "amd64",
		Config: &docker10.DockerConfig{
			Labels: labels,
		},
	}
}

// kasImageMetadataOnContainerConfig returns image metadata whose labels live
// only on ContainerConfig, as some images built outside the standard OpenShift
// build pipeline record them.
func kasImageMetadataOnContainerConfig(labels map[string]string, created metav1.Time) *dockerv1client.DockerImageConfig {
	return &dockerv1client.DockerImageConfig{
		Created:         created.Time,
		OS:              "linux",
		Architecture:    "amd64",
		Config:          &docker10.DockerConfig{},
		ContainerConfig: docker10.DockerConfig{Labels: labels},
	}
}

func newSkewedHCP() *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "hcp", Namespace: "clusters-example"},
		Spec: hyperv1.HostedControlPlaneSpec{
			ReleaseImage:             "quay.io/openshift/release:user",
			ControlPlaneReleaseImage: ptr.To("quay.io/openshift/release:control-plane"),
		},
	}
}

func reportedVersionTestClient(t *testing.T, hcp *hyperv1.HostedControlPlane, objects ...client.Object) client.Client {
	t.Helper()
	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: hcp.Namespace},
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte("{}")},
	}
	return fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(append([]client.Object{pullSecret}, objects...)...).
		Build()
}

// controlPlaneImageProvider reports an explicit kubernetes component version:
// the fake provider's default ("1.30") is not a valid semver, which
// sameKubeMinorVersion would reject before masking is ever considered.
func controlPlaneImageProvider() imageprovider.ReleaseImageProvider {
	return testutil.FakeImageProvider(
		testutil.WithImages(map[string]string{"hyperkube": "cp-registry/hyperkube:latest"}),
		testutil.WithComponentVersions(map[string]string{"kubernetes": "1.30.4"}),
	)
}

func userImageProvider() imageprovider.ReleaseImageProvider {
	return testutil.FakeImageProvider(
		testutil.WithImages(map[string]string{"hyperkube": "user-registry/hyperkube:latest"}),
		testutil.WithComponentVersions(map[string]string{"kubernetes": "1.30.1"}),
	)
}

func TestKasVersionInfo(t *testing.T) {
	created := metav1.NewTime(metav1.Now().Time)

	t.Run("When build labels are complete, it should build the version info", func(t *testing.T) {
		metadata := kasImageMetadata(map[string]string{
			buildCommitIDLabel:   "9a1f2c3d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b",
			buildCommitDateLabel: "2026-08-12T10:14:33Z",
			buildGoVersionLabel:  "go1.23.4",
		}, created)

		info, err := kasVersionInfo("1.34.2", metadata)
		require.NoError(t, err)
		require.Equal(t, &version.Info{
			Major:        "1",
			Minor:        "34",
			GitVersion:   "v1.34.2",
			GitCommit:    "9a1f2c3d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b",
			GitTreeState: "clean",
			BuildDate:    "2026-08-12T10:14:33Z",
			GoVersion:    "go1.23.4",
			Compiler:     "gc",
			Platform:     "linux/amd64",
		}, info)
	})

	t.Run("When the commit date label is missing, it should fall back to the image creation timestamp", func(t *testing.T) {
		metadata := kasImageMetadata(map[string]string{buildCommitIDLabel: "deadbeef"}, created)

		info, err := kasVersionInfo("1.34.2", metadata)
		require.NoError(t, err)
		require.Equal(t, created.UTC().Format("2006-01-02T15:04:05Z"), info.BuildDate)
	})

	t.Run("When the kubernetes version is invalid, it should fail closed", func(t *testing.T) {
		_, err := kasVersionInfo("not-a-version", kasImageMetadata(map[string]string{buildCommitIDLabel: "deadbeef"}, created))
		require.Error(t, err)
	})

	t.Run("When the commit label is missing, it should fail closed", func(t *testing.T) {
		_, err := kasVersionInfo("1.34.2", kasImageMetadata(map[string]string{}, created))
		require.Error(t, err)
	})

	t.Run("When the build labels only live on ContainerConfig, it should build the version info", func(t *testing.T) {
		metadata := kasImageMetadataOnContainerConfig(map[string]string{
			buildCommitIDLabel:   "9a1f2c3d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b",
			buildCommitDateLabel: "2026-08-12T10:14:33Z",
			buildGoVersionLabel:  "go1.23.4",
		}, created)

		info, err := kasVersionInfo("1.34.2", metadata)
		require.NoError(t, err)
		require.Equal(t, "9a1f2c3d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b", info.GitCommit)
		require.Equal(t, "2026-08-12T10:14:33Z", info.BuildDate)
		require.Equal(t, "go1.23.4", info.GoVersion)
	})
}

func TestKasBuildLabels(t *testing.T) {
	testCases := []struct {
		name     string
		metadata *dockerv1client.DockerImageConfig
		expected map[string]string
	}{
		{
			name: "When Config carries labels, it should return the Config labels",
			metadata: &dockerv1client.DockerImageConfig{
				Config:          &docker10.DockerConfig{Labels: map[string]string{"from": "config"}},
				ContainerConfig: docker10.DockerConfig{Labels: map[string]string{"from": "container-config"}},
			},
			expected: map[string]string{"from": "config"},
		},
		{
			name: "When Config is nil, it should fall back to the ContainerConfig labels",
			metadata: &dockerv1client.DockerImageConfig{
				ContainerConfig: docker10.DockerConfig{Labels: map[string]string{"from": "container-config"}},
			},
			expected: map[string]string{"from": "container-config"},
		},
		{
			// Some kube-apiserver images carry their build labels only on
			// ContainerConfig while still having a Config without labels.
			// Returning Config.Labels unconditionally would drop them and fail
			// the version mask closed.
			name: "When Config has no labels, it should fall back to the ContainerConfig labels",
			metadata: &dockerv1client.DockerImageConfig{
				Config:          &docker10.DockerConfig{},
				ContainerConfig: docker10.DockerConfig{Labels: map[string]string{"from": "container-config"}},
			},
			expected: map[string]string{"from": "container-config"},
		},
		{
			name: "When Config has an empty labels map, it should fall back to the ContainerConfig labels",
			metadata: &dockerv1client.DockerImageConfig{
				Config:          &docker10.DockerConfig{Labels: map[string]string{}},
				ContainerConfig: docker10.DockerConfig{Labels: map[string]string{"from": "container-config"}},
			},
			expected: map[string]string{"from": "container-config"},
		},
		{
			name:     "When neither Config nor ContainerConfig carry labels, it should return nil",
			metadata: &dockerv1client.DockerImageConfig{},
			expected: nil,
		},
		{
			name:     "When there is no metadata, it should return nil",
			metadata: nil,
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, kasBuildLabels(tc.metadata))
		})
	}
}

func TestReportedVersionInfo(t *testing.T) {
	created := metav1.Now()
	userLabels := map[string]string{
		buildCommitIDLabel:   "9a1f2c3d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b",
		buildCommitDateLabel: "2026-08-12T10:14:33Z",
	}
	metadataProvider := &fakeImageMetadataProvider{configs: map[string]*dockerv1client.DockerImageConfig{
		"user-registry/hyperkube:latest": kasImageMetadata(userLabels, created),
	}}

	t.Run("When there is no payload skew, it should not engage", func(t *testing.T) {
		hcp := newSkewedHCP()
		hcp.Spec.ControlPlaneReleaseImage = nil
		_, enabled, err := ReportedVersionInfo(context.Background(), reportedVersionTestClient(t, hcp), hcp, controlPlaneImageProvider(), userImageProvider(), metadataProvider)
		require.NoError(t, err)
		require.False(t, enabled)
	})

	t.Run("When the payloads are identical, it should not engage", func(t *testing.T) {
		hcp := newSkewedHCP()
		hcp.Spec.ControlPlaneReleaseImage = ptr.To(hcp.Spec.ReleaseImage)
		_, enabled, err := ReportedVersionInfo(context.Background(), reportedVersionTestClient(t, hcp), hcp, controlPlaneImageProvider(), userImageProvider(), metadataProvider)
		require.NoError(t, err)
		require.False(t, enabled)
	})

	t.Run("When skewed, it should report the user payload version", func(t *testing.T) {
		hcp := newSkewedHCP()
		info, enabled, err := ReportedVersionInfo(context.Background(), reportedVersionTestClient(t, hcp), hcp, controlPlaneImageProvider(), userImageProvider(), metadataProvider)
		require.NoError(t, err)
		require.True(t, enabled)
		require.Equal(t, "v1.30.1", info.GitVersion)
		require.Equal(t, "9a1f2c3d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b", info.GitCommit)
		require.Equal(t, "2026-08-12T10:14:33Z", info.BuildDate)
	})

	t.Run("When the skew crosses a Kubernetes minor, it should not engage", func(t *testing.T) {
		hcp := newSkewedHCP()
		controlPlane := testutil.FakeImageProvider(
			testutil.WithImages(map[string]string{"hyperkube": "cp-registry/hyperkube:latest"}),
			testutil.WithComponentVersions(map[string]string{"kubernetes": "1.31.0"}),
		)
		_, enabled, err := ReportedVersionInfo(context.Background(), reportedVersionTestClient(t, hcp), hcp, controlPlane, userImageProvider(), metadataProvider)
		require.NoError(t, err)
		require.False(t, enabled)
	})

	t.Run("When the user payload image records its build labels on ContainerConfig, it should report the user payload version", func(t *testing.T) {
		hcp := newSkewedHCP()
		provider := &fakeImageMetadataProvider{configs: map[string]*dockerv1client.DockerImageConfig{
			"user-registry/hyperkube:latest": kasImageMetadataOnContainerConfig(userLabels, created),
		}}
		info, enabled, err := ReportedVersionInfo(context.Background(), reportedVersionTestClient(t, hcp), hcp, controlPlaneImageProvider(), userImageProvider(), provider)
		require.NoError(t, err)
		require.True(t, enabled)
		require.Equal(t, "v1.30.1", info.GitVersion)
		require.Equal(t, "9a1f2c3d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b", info.GitCommit)
	})

	t.Run("When the user payload image metadata is missing, it should fail closed", func(t *testing.T) {
		hcp := newSkewedHCP()
		empty := &fakeImageMetadataProvider{configs: map[string]*dockerv1client.DockerImageConfig{}}
		_, enabled, err := ReportedVersionInfo(context.Background(), reportedVersionTestClient(t, hcp), hcp, controlPlaneImageProvider(), userImageProvider(), empty)
		require.Error(t, err)
		require.False(t, enabled)
	})
}

func TestReconcileReportedVersionConfigMap(t *testing.T) {
	cm := manifests.KASReportedVersionConfigMap("clusters-example")
	require.NoError(t, ReconcileReportedVersionConfigMap(cm, config.OwnerRef{}, &version.Info{GitVersion: "v1.30.1"}))
	require.Contains(t, cm.Data, ReportedVersionFileKey)
	require.Contains(t, cm.Data[ReportedVersionFileKey], `"gitVersion":"v1.30.1"`)
}

func TestApplyReportedVersionConfig(t *testing.T) {
	t.Run("When the main container is present, it should mount the ConfigMap and set the env var", func(t *testing.T) {
		podTemplate := &corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: kasContainerMain().Name}}},
		}
		require.NoError(t, applyReportedVersionConfig(podTemplate, &version.Info{GitVersion: "v1.30.1"}))

		podSpec := podTemplate.Spec
		require.Contains(t, podTemplate.Annotations, reportedVersionHashAnnotation)
		require.Len(t, podSpec.Volumes, 1)
		require.Equal(t, reportedVersionVolumeName, podSpec.Volumes[0].Name)
		require.NotNil(t, podSpec.Volumes[0].ConfigMap)
		require.Equal(t, manifests.KASReportedVersionConfigMap("").Name, podSpec.Volumes[0].ConfigMap.Name)

		container := podSpec.Containers[0]
		require.Len(t, container.VolumeMounts, 1)
		require.Equal(t, reportedVersionMountPath, container.VolumeMounts[0].MountPath)
		require.Len(t, container.Env, 1)
		require.Equal(t, ReportedVersionFileEnvVar, container.Env[0].Name)
		require.Equal(t, reportedVersionMountPath+"/"+ReportedVersionFileKey, container.Env[0].Value)
	})

	t.Run("When the main container is absent, it should fail rather than render a half-masked pod", func(t *testing.T) {
		require.Error(t, applyReportedVersionConfig(&corev1.PodTemplateSpec{}, &version.Info{}))
	})

	t.Run("When the reported version changes, the hash annotation should change so the kube-apiserver rolls out", func(t *testing.T) {
		render := func(info *version.Info) string {
			podTemplate := &corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: kasContainerMain().Name}}},
			}
			require.NoError(t, applyReportedVersionConfig(podTemplate, info))
			return podTemplate.Annotations[reportedVersionHashAnnotation]
		}
		require.NotEqual(t, render(&version.Info{GitVersion: "v1.30.1"}), render(&version.Info{GitVersion: "v1.30.2"}))
		require.Equal(t, render(&version.Info{GitVersion: "v1.30.1"}), render(&version.Info{GitVersion: "v1.30.1"}))
	})
}

func TestCleanupReportedVersionConfigMap(t *testing.T) {
	hcp := newSkewedHCP()

	reportedVersionConfigMap := func() *corev1.ConfigMap {
		cm := manifests.KASReportedVersionConfigMap(hcp.Namespace)
		cm.Data = map[string]string{ReportedVersionFileKey: `{"gitVersion":"v1.30.1"}`}
		return cm
	}

	kasDeployment := func(mounts bool, rolledOut bool) *appsv1.Deployment {
		deployment := manifests.KASDeployment(hcp.Namespace)
		deployment.Generation = 2
		deployment.Spec.Replicas = ptr.To[int32](3)
		if mounts {
			deployment.Spec.Template.Spec.Volumes = []corev1.Volume{{Name: reportedVersionVolumeName}}
		}
		if rolledOut {
			deployment.Status.ObservedGeneration = 2
			deployment.Status.Replicas = 3
			deployment.Status.UpdatedReplicas = 3
			deployment.Status.AvailableReplicas = 3
		}
		return deployment
	}

	rollingDeployment := func(replicas, total, updated, available int32) *appsv1.Deployment {
		deployment := manifests.KASDeployment(hcp.Namespace)
		deployment.Generation = 2
		deployment.Spec.Replicas = ptr.To(replicas)
		deployment.Status.ObservedGeneration = 2
		deployment.Status.Replicas = total
		deployment.Status.UpdatedReplicas = updated
		deployment.Status.AvailableReplicas = available
		return deployment
	}

	configMapExists := func(t *testing.T, c client.Client) bool {
		t.Helper()
		cm := manifests.KASReportedVersionConfigMap(hcp.Namespace)
		err := c.Get(context.Background(), client.ObjectKeyFromObject(cm), cm)
		if apierrors.IsNotFound(err) {
			return false
		}
		require.NoError(t, err)
		return true
	}

	t.Run("When the ConfigMap does not exist, it should do nothing", func(t *testing.T) {
		c := reportedVersionTestClient(t, hcp)
		require.NoError(t, CleanupReportedVersionConfigMap(context.Background(), c, hcp))
	})

	t.Run("When the deployment still mounts it, it should keep the ConfigMap", func(t *testing.T) {
		c := reportedVersionTestClient(t, hcp, reportedVersionConfigMap(), kasDeployment(true, true))
		require.NoError(t, CleanupReportedVersionConfigMap(context.Background(), c, hcp))
		require.True(t, configMapExists(t, c))
	})

	t.Run("When the mount is gone but the rollout is incomplete, it should keep the ConfigMap", func(t *testing.T) {
		c := reportedVersionTestClient(t, hcp, reportedVersionConfigMap(), kasDeployment(false, false))
		require.NoError(t, CleanupReportedVersionConfigMap(context.Background(), c, hcp))
		require.True(t, configMapExists(t, c))
	})

	t.Run("When the mount is gone and the rollout is complete, it should delete the ConfigMap", func(t *testing.T) {
		c := reportedVersionTestClient(t, hcp, reportedVersionConfigMap(), kasDeployment(false, true))
		require.NoError(t, CleanupReportedVersionConfigMap(context.Background(), c, hcp))
		require.False(t, configMapExists(t, c))
	})

	// Regression test: updatedReplicas counts unready pods, so during a surge
	// rollout it reaches the desired count while the old ReplicaSet is still
	// scaled up and can recreate a pod that mounts the ConfigMap. Deleting the
	// ConfigMap here would leave that replacement pod unable to start.
	t.Run("When the mount is gone but a surge pod is not available yet, it should keep the ConfigMap", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			deployment *appsv1.Deployment
		}{
			{
				// Single replica: no strategy is set, so Kubernetes' default
				// 25% maxSurge rounds up to one surge pod.
				name:       "single replica with an unready surge pod",
				deployment: rollingDeployment(1, 2, 1, 1),
			},
			{
				// Two replica request serving topology: maxSurge is one.
				name:       "two replicas with an unready surge pod",
				deployment: rollingDeployment(2, 3, 2, 2),
			},
			{
				name:       "updated pods are not available yet",
				deployment: rollingDeployment(3, 3, 3, 2),
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				c := reportedVersionTestClient(t, hcp, reportedVersionConfigMap(), tc.deployment)
				require.NoError(t, CleanupReportedVersionConfigMap(context.Background(), c, hcp))
				require.True(t, configMapExists(t, c), "ConfigMap must be kept until the old ReplicaSets have scaled to zero")
			})
		}
	})

	t.Run("When there is no deployment, it should delete the ConfigMap", func(t *testing.T) {
		c := reportedVersionTestClient(t, hcp, reportedVersionConfigMap())
		require.NoError(t, CleanupReportedVersionConfigMap(context.Background(), c, hcp))
		require.False(t, configMapExists(t, c))
	})
}
