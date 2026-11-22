package kas

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	dockerv1client "github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"

	"github.com/openshift/api/image/docker10"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/docker/distribution"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// fakeImageMetadataProvider implements imageregistry.ImageMetadataProvider by
// serving canned image configs keyed by pull spec.
type fakeImageMetadataProvider struct {
	configs map[string]*dockerv1client.DockerImageConfig
}

func (f *fakeImageMetadataProvider) ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {
	if config, ok := f.configs[imageRef]; ok {
		return config, nil
	}
	return nil, NotFoundError{}
}

type NotFoundError struct{}

func (NotFoundError) Error() string { return "not found" }

func (f *fakeImageMetadataProvider) GetManifest(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Manifest, error) {
	return nil, NotFoundError{}
}

func (f *fakeImageMetadataProvider) GetDigest(ctx context.Context, imageRef string, pullSecret []byte) (digest.Digest, *reference.DockerImageReference, error) {
	return "", nil, NotFoundError{}
}

func (f *fakeImageMetadataProvider) GetMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, []distribution.Descriptor, distribution.BlobStore, error) {
	config, err := f.ImageMetadata(ctx, imageRef, pullSecret)
	if err != nil {
		return nil, nil, nil, err
	}
	return config, nil, nil, nil
}

func (f *fakeImageMetadataProvider) GetOverride(ctx context.Context, imageRef string, pullSecret []byte) (*reference.DockerImageReference, error) {
	return nil, NotFoundError{}
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

func newReportedVersionTestContext(t *testing.T, hcp *hyperv1.HostedControlPlane, configs map[string]*dockerv1client.DockerImageConfig) controlplanecomponent.WorkloadContext {
	return newReportedVersionTestContextWithObjects(t, hcp, configs)
}

func newReportedVersionTestContextWithObjects(t *testing.T, hcp *hyperv1.HostedControlPlane, configs map[string]*dockerv1client.DockerImageConfig, objects ...client.Object) controlplanecomponent.WorkloadContext {
	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: hcp.Namespace},
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte("{}")},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(append([]client.Object{pullSecret}, objects...)...).
		Build()

	return controlplanecomponent.WorkloadContext{
		Context:              context.Background(),
		Client:               fakeClient,
		HCP:                  hcp,
		ReleaseImageProvider: testutil.FakeImageProvider(testutil.WithImages(map[string]string{"hyperkube": "cp-registry/hyperkube:latest"})),
		UserReleaseImageProvider: testutil.FakeImageProvider(
			testutil.WithImages(map[string]string{"hyperkube": "user-registry/hyperkube:latest"}),
			testutil.WithComponentVersions(map[string]string{"kubernetes": "1.30.1"}),
		),
		ImageMetadataProvider:     &fakeImageMetadataProvider{configs: configs},
		SetDefaultSecurityContext: true,
	}
}

func newSkewedHCP() *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "hcp", Namespace: "clusters-example"},
		Spec: hyperv1.HostedControlPlaneSpec{
			ReleaseImage:             "quay.io/openshift/release:user",
			ControlPlaneReleaseImage: ptrTo("quay.io/openshift/release:control-plane"),
		},
	}
}

func ptrTo(s string) *string { return &s }

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
		metadata := kasImageMetadata(map[string]string{
			buildCommitIDLabel: "deadbeef",
		}, created)

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
}

func TestKasReportedVersionInfo(t *testing.T) {
	created := metav1.Now()
	userLabels := map[string]string{
		buildCommitIDLabel:   "9a1f2c3d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b",
		buildCommitDateLabel: "2026-08-12T10:14:33Z",
	}
	configs := map[string]*dockerv1client.DockerImageConfig{
		"user-registry/hyperkube:latest": kasImageMetadata(userLabels, created),
	}

	t.Run("When there is no payload skew, it should not engage", func(t *testing.T) {
		hcp := newSkewedHCP()
		hcp.Spec.ControlPlaneReleaseImage = nil
		cpContext := newReportedVersionTestContext(t, hcp, configs)
		_, enabled, err := kasReportedVersionInfo(cpContext)
		require.NoError(t, err)
		require.False(t, enabled)
	})

	t.Run("When the payloads are identical, it should not engage", func(t *testing.T) {
		hcp := newSkewedHCP()
		hcp.Spec.ControlPlaneReleaseImage = ptrTo(hcp.Spec.ReleaseImage)
		cpContext := newReportedVersionTestContext(t, hcp, configs)
		_, enabled, err := kasReportedVersionInfo(cpContext)
		require.NoError(t, err)
		require.False(t, enabled)
	})

	t.Run("When skewed, it should report the user payload version", func(t *testing.T) {
		cpContext := newReportedVersionTestContext(t, newSkewedHCP(), configs)
		info, enabled, err := kasReportedVersionInfo(cpContext)
		require.NoError(t, err)
		require.True(t, enabled)
		require.Equal(t, "v1.30.1", info.GitVersion)
		require.Equal(t, "9a1f2c3d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b", info.GitCommit)
		require.Equal(t, "2026-08-12T10:14:33Z", info.BuildDate)
	})

	t.Run("When the skew crosses a Kubernetes minor, it should not engage", func(t *testing.T) {
		cpContext := newReportedVersionTestContext(t, newSkewedHCP(), configs)
		cpContext.ReleaseImageProvider = testutil.FakeImageProvider(
			testutil.WithComponentVersions(map[string]string{"kubernetes": "1.31.0"}),
		)
		_, enabled, err := kasReportedVersionInfo(cpContext)
		require.NoError(t, err)
		require.False(t, enabled)
	})

	t.Run("When the user payload image metadata is missing, it should fail closed", func(t *testing.T) {
		cpContext := newReportedVersionTestContext(t, newSkewedHCP(), map[string]*dockerv1client.DockerImageConfig{})
		_, enabled, err := kasReportedVersionInfo(cpContext)
		require.Error(t, err)
		require.False(t, enabled)
	})
}

func TestAdaptReportedVersionConfigMap(t *testing.T) {
	created := metav1.Now()
	configs := map[string]*dockerv1client.DockerImageConfig{
		"user-registry/hyperkube:latest": kasImageMetadata(map[string]string{
			buildCommitIDLabel:   "9a1f2c3d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b",
			buildCommitDateLabel: "2026-08-12T10:14:33Z",
		}, created),
	}

	t.Run("When engaged, it should fill the ConfigMap with the version info", func(t *testing.T) {
		cpContext := newReportedVersionTestContext(t, newSkewedHCP(), configs)
		cm := &corev1.ConfigMap{}
		require.NoError(t, adaptReportedVersionConfigMap(cpContext, cm))
		require.Contains(t, cm.Data, ReportedVersionFileKey)
		require.Contains(t, cm.Data[ReportedVersionFileKey], `"gitVersion":"v1.30.1"`)
	})

	t.Run("When not engaged, it should leave the ConfigMap untouched", func(t *testing.T) {
		hcp := newSkewedHCP()
		hcp.Spec.ControlPlaneReleaseImage = nil
		cpContext := newReportedVersionTestContext(t, hcp, configs)
		cm := &corev1.ConfigMap{Data: map[string]string{ReportedVersionFileKey: "{\"gitVersion\":\"v1.30.1\"}"}}
		require.NoError(t, adaptReportedVersionConfigMap(cpContext, cm))
		require.Equal(t, "{\"gitVersion\":\"v1.30.1\"}", cm.Data[ReportedVersionFileKey])
	})

	// Regression test: a kube-apiserver deployment with masking engaged mounts
	// the kas-reported-version ConfigMap non-optionally. When extraction fails
	// (e.g. a transient registry error), the manifest adapter predicate must
	// keep the ConfigMap in place so replacement pods can still mount it, and
	// the adapt function must surface the error and leave the existing content
	// untouched.
	t.Run("When extraction fails, it should preserve an existing ConfigMap", func(t *testing.T) {
		cpContext := newReportedVersionTestContext(t, newSkewedHCP(), map[string]*dockerv1client.DockerImageConfig{})
		require.True(t, reportedVersionEnabled(cpContext), "predicate must keep the ConfigMap on extraction errors")

		cm := &corev1.ConfigMap{Data: map[string]string{ReportedVersionFileKey: "{\"gitVersion\":\"v1.30.1\"}"}}
		require.Error(t, adaptReportedVersionConfigMap(cpContext, cm))
		require.Equal(t, "{\"gitVersion\":\"v1.30.1\"}", cm.Data[ReportedVersionFileKey], "existing content must be preserved")
	})

	// Regression test: when masking is disabled, the ConfigMap cleanup must be
	// deferred until the kube-apiserver deployment no longer mounts it, since
	// manifest adapters run before the workload reconcile updates the
	// deployment.
	t.Run("When masking is disabled but the deployment still mounts the ConfigMap, it should keep it", func(t *testing.T) {
		hcp := newSkewedHCP()
		hcp.Spec.ControlPlaneReleaseImage = nil
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: hcp.Namespace},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Volumes: []corev1.Volume{{Name: reportedVersionVolumeName}},
			}}},
		}
		cpContext := newReportedVersionTestContextWithObjects(t, hcp, configs, deployment, existingReportedVersionConfigMap(hcp))
		require.True(t, reportedVersionEnabled(cpContext), "predicate must keep the ConfigMap while the deployment mounts it")

		cm := &corev1.ConfigMap{Data: map[string]string{ReportedVersionFileKey: "{\"gitVersion\":\"v1.30.1\"}"}}
		require.NoError(t, adaptReportedVersionConfigMap(cpContext, cm))
		require.Equal(t, "{\"gitVersion\":\"v1.30.1\"}", cm.Data[ReportedVersionFileKey], "existing content must be preserved")
	})

	t.Run("When masking is disabled and the deployment no longer mounts the ConfigMap, it should allow cleanup", func(t *testing.T) {
		hcp := newSkewedHCP()
		hcp.Spec.ControlPlaneReleaseImage = nil
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: hcp.Namespace, Generation: 1},
			Status: appsv1.DeploymentStatus{
				ObservedGeneration: 1,
				Replicas:           1,
				UpdatedReplicas:    1,
				AvailableReplicas:  1,
			},
		}
		cpContext := newReportedVersionTestContextWithObjects(t, hcp, configs, deployment)
		require.False(t, reportedVersionEnabled(cpContext))

		// No deployment at all must also allow cleanup.
		cpContext = newReportedVersionTestContext(t, hcp, configs)
		require.False(t, reportedVersionEnabled(cpContext))
	})

	// Regression test: updatedReplicas counts unready pods, so during a surge
	// rollout it reaches the desired count while the old ReplicaSet is still
	// scaled up and can recreate a pod that mounts the ConfigMap. Deleting the
	// ConfigMap here would leave that replacement pod unable to start.
	t.Run("When masking is disabled and a surge pod is not available yet, it should keep the ConfigMap", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			replicas int32
			status   appsv1.DeploymentStatus
		}{
			{
				// Single replica: maxSurge is 25%, which permits one extra pod.
				name:     "single replica with an unready surge pod",
				replicas: 1,
				status:   appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 2, UpdatedReplicas: 1, AvailableReplicas: 1, ReadyReplicas: 1, UnavailableReplicas: 1},
			},
			{
				// Two replica request serving topology: maxSurge is one.
				name:     "two replicas with an unready surge pod",
				replicas: 2,
				status:   appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 3, UpdatedReplicas: 2, AvailableReplicas: 2, ReadyReplicas: 2, UnavailableReplicas: 1},
			},
			{
				name:     "updated pods are not available yet",
				replicas: 3,
				status:   appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 3, UpdatedReplicas: 3, AvailableReplicas: 2, ReadyReplicas: 2, UnavailableReplicas: 1},
			},
			{
				// The old pod is already gone, so every count has reached the
				// desired replica count, but the old ReplicaSet is still scaled
				// up and about to recreate a pod that mounts the ConfigMap.
				// unavailableReplicas is the only field that still reflects it.
				name:     "single replica whose old pod was evicted mid-rollout",
				replicas: 1,
				status:   appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1, ReadyReplicas: 1, UnavailableReplicas: 1},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				hcp := newSkewedHCP()
				hcp.Spec.ControlPlaneReleaseImage = nil
				deployment := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: hcp.Namespace, Generation: 2},
					Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(tc.replicas)},
					Status:     tc.status,
				}
				cpContext := newReportedVersionTestContextWithObjects(t, hcp, configs, deployment, existingReportedVersionConfigMap(hcp))
				require.True(t, reportedVersionEnabled(cpContext), "predicate must keep the ConfigMap until the old ReplicaSets have scaled to zero")
			})
		}
	})

	// The counterpart of the surge cases: once the rollout has settled the
	// ConfigMap must actually be cleaned up.
	t.Run("When masking is disabled and the rollout has settled, it should allow cleanup", func(t *testing.T) {
		hcp := newSkewedHCP()
		hcp.Spec.ControlPlaneReleaseImage = nil
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: hcp.Namespace, Generation: 2},
			Spec:       appsv1.DeploymentSpec{Replicas: ptr.To[int32](3)},
			Status: appsv1.DeploymentStatus{
				ObservedGeneration: 2,
				Replicas:           3,
				UpdatedReplicas:    3,
				AvailableReplicas:  3,
				ReadyReplicas:      3,
			},
		}
		cpContext := newReportedVersionTestContextWithObjects(t, hcp, configs, deployment, existingReportedVersionConfigMap(hcp))
		require.False(t, reportedVersionEnabled(cpContext))
	})

	// Regression test: after the deployment template drops the ConfigMap
	// mount, the rollout away from the old template can still be in progress
	// and the old ReplicaSet can recreate pods that mount the ConfigMap. The
	// ConfigMap must be retained until the rollout completes.
	t.Run("When masking is disabled and the rollout is still in progress, it should keep the ConfigMap", func(t *testing.T) {
		hcp := newSkewedHCP()
		hcp.Spec.ControlPlaneReleaseImage = nil
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: hcp.Namespace, Generation: 2},
			Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 0},
		}
		cpContext := newReportedVersionTestContextWithObjects(t, hcp, configs, deployment, existingReportedVersionConfigMap(hcp))
		require.True(t, reportedVersionEnabled(cpContext), "predicate must keep the ConfigMap while the rollout is in progress")
	})

	// Regression test: deferred cleanup must only retain an existing
	// ConfigMap. A control plane that never masked must not get an empty
	// kas-reported-version ConfigMap created whenever a kube-apiserver
	// rollout is in progress.
	t.Run("When masking is disabled and the ConfigMap does not exist, it should not create it during a rollout", func(t *testing.T) {
		hcp := newSkewedHCP()
		hcp.Spec.ControlPlaneReleaseImage = nil
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: ComponentName, Namespace: hcp.Namespace, Generation: 2},
			Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, UpdatedReplicas: 0},
		}
		cpContext := newReportedVersionTestContextWithObjects(t, hcp, configs, deployment)
		require.False(t, reportedVersionEnabled(cpContext), "predicate must not create the ConfigMap for a control plane that is not masking")
	})

	// Regression test: when the existing ConfigMap could not be loaded, the
	// generic adapter passes the bare manifest. Retaining it must fail rather
	// than overwrite the retained version.Info with an empty ConfigMap.
	t.Run("When masking is disabled and the retained ConfigMap content was not loaded, it should fail", func(t *testing.T) {
		hcp := newSkewedHCP()
		hcp.Spec.ControlPlaneReleaseImage = nil
		cpContext := newReportedVersionTestContext(t, hcp, configs)
		cm := &corev1.ConfigMap{}
		require.Error(t, adaptReportedVersionConfigMap(cpContext, cm))
		require.Empty(t, cm.Data)
	})
}

func existingReportedVersionConfigMap(hcp *hyperv1.HostedControlPlane) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: ReportedVersionConfigMapName, Namespace: hcp.Namespace},
		Data:       map[string]string{ReportedVersionFileKey: "{\"gitVersion\":\"v1.30.1\"}"},
	}
}

func TestApplyReportedVersionConfig(t *testing.T) {
	created := metav1.Now()
	configs := map[string]*dockerv1client.DockerImageConfig{
		"user-registry/hyperkube:latest": kasImageMetadata(map[string]string{
			buildCommitIDLabel:   "9a1f2c3d4e5b6a7f8c9d0e1f2a3b4c5d6e7f8a9b",
			buildCommitDateLabel: "2026-08-12T10:14:33Z",
		}, created),
	}
	newPodSpec := func() *corev1.PodSpec {
		return &corev1.PodSpec{Containers: []corev1.Container{{Name: ComponentName}}}
	}

	t.Run("When engaged, it should mount the ConfigMap and set the env var", func(t *testing.T) {
		cpContext := newReportedVersionTestContext(t, newSkewedHCP(), configs)
		podSpec := newPodSpec()
		require.NoError(t, applyReportedVersionConfig(cpContext, podSpec))

		require.Len(t, podSpec.Volumes, 1)
		require.Equal(t, reportedVersionVolumeName, podSpec.Volumes[0].Name)
		require.Equal(t, ReportedVersionConfigMapName, podSpec.Volumes[0].ConfigMap.Name)

		require.Len(t, podSpec.Containers[0].VolumeMounts, 1)
		require.Equal(t, reportedVersionVolumeName, podSpec.Containers[0].VolumeMounts[0].Name)
		require.Equal(t, reportedVersionMountPath, podSpec.Containers[0].VolumeMounts[0].MountPath)

		require.Contains(t, podSpec.Containers[0].Env, corev1.EnvVar{
			Name:  ReportedVersionFileEnvVar,
			Value: "/etc/kubernetes/kas-reported-version/version.json",
		})
	})

	t.Run("When not engaged, it should not touch the pod spec", func(t *testing.T) {
		hcp := newSkewedHCP()
		hcp.Spec.ControlPlaneReleaseImage = nil
		cpContext := newReportedVersionTestContext(t, hcp, configs)
		podSpec := newPodSpec()
		require.NoError(t, applyReportedVersionConfig(cpContext, podSpec))
		require.Empty(t, podSpec.Volumes)
		require.Empty(t, podSpec.Containers[0].VolumeMounts)
		require.Empty(t, podSpec.Containers[0].Args)
		require.Empty(t, podSpec.Containers[0].Env)
	})
}
