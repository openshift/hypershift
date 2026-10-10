package assets

import (
	"fmt"
	"slices"
	"testing"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestLoadDeploymentManifest(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		componentName string
		validate      func(g Gomega, deployment *appsv1.Deployment, err error)
	}{
		{
			name:          "When loading a valid deployment manifest, it should decode successfully",
			componentName: "aws-cloud-controller-manager",
			validate: func(g Gomega, deployment *appsv1.Deployment, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(deployment).ToNot(BeNil())
				g.Expect(deployment.Kind).To(Equal("Deployment"))
				g.Expect(deployment.Name).To(Equal("cloud-controller-manager"))
			},
		},
		{
			name:          "When component name does not exist, it should return an error",
			componentName: "nonexistent-component",
			validate: func(g Gomega, deployment *appsv1.Deployment, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(deployment).To(BeNil())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			deployment, err := LoadDeploymentManifest(tc.componentName)
			tc.validate(g, deployment, err)
		})
	}

	for _, catalog := range []struct {
		name   string
		memory string
	}{
		{name: "certified-operators-catalog", memory: "160Mi"},
		{name: "community-operators-catalog", memory: "160Mi"},
		{name: "redhat-operators-catalog", memory: "420Mi"},
	} {
		t.Run(fmt.Sprintf("When loading %s, it should budget bounded exec probes without changing resources", catalog.name), func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			deployment, err := LoadDeploymentManifest(catalog.name)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
			registry := deployment.Spec.Template.Spec.Containers[0]
			g.Expect(registry.Name).To(Equal("registry"))
			for _, probe := range []struct {
				name         string
				probe        *corev1.Probe
				initialDelay int32
				failures     int32
			}{
				{name: "liveness", probe: registry.LivenessProbe, initialDelay: 10, failures: 3},
				{name: "readiness", probe: registry.ReadinessProbe, initialDelay: 5, failures: 3},
				{name: "startup", probe: registry.StartupProbe, failures: 120},
			} {
				g.Expect(probe.probe).To(Equal(&corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{
						"grpc_health_probe", "-addr=:50051", "-connect-timeout=1s", "-rpc-timeout=2s",
					}}},
					TimeoutSeconds:      5,
					InitialDelaySeconds: probe.initialDelay,
					PeriodSeconds:       10,
					SuccessThreshold:    1,
					FailureThreshold:    probe.failures,
				}), "%s probe", probe.name)
			}
			startupAllowance := registry.StartupProbe.PeriodSeconds * registry.StartupProbe.FailureThreshold
			g.Expect(startupAllowance).To(Equal(int32(1200)), "allow twenty minutes of startup retries without weakening the healthy RPC requirement")
			g.Expect(deployment.Spec.ProgressDeadlineSeconds).To(HaveValue(Equal(int32(1800))))
			g.Expect(*deployment.Spec.ProgressDeadlineSeconds).To(BeNumerically(">", startupAllowance), "allow scheduling, image pulls, and init containers in addition to startup retries")
			g.Expect(registry.Resources).To(Equal(corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse(catalog.memory),
				},
			}))
			for _, container := range deployment.Spec.Template.Spec.InitContainers {
				g.Expect(container.Resources).To(Equal(corev1.ResourceRequirements{}), "%s resources", container.Name)
			}
		})
	}
}

func TestKonnectivityServerAuthenticatesAgents(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	deployment, err := LoadDeploymentManifest("kube-apiserver")
	if err != nil {
		t.Fatalf("failed to load kube-apiserver deployment manifest: %v", err)
	}
	if deployment == nil {
		t.Fatal("kube-apiserver deployment manifest is nil")
	}

	var konnectivityServer *corev1.Container
	for i := range deployment.Spec.Template.Spec.Containers {
		if deployment.Spec.Template.Spec.Containers[i].Name == "konnectivity-server" {
			konnectivityServer = &deployment.Spec.Template.Spec.Containers[i]
			break
		}
	}

	if konnectivityServer == nil {
		t.Fatal("konnectivity-server container not found")
	}

	clusterCAFlagIndex := slices.Index(konnectivityServer.Args, "--cluster-ca-cert")
	if clusterCAFlagIndex == -1 || clusterCAFlagIndex+1 >= len(konnectivityServer.Args) {
		t.Fatal("--cluster-ca-cert flag or its value not found")
	}
	g.Expect(konnectivityServer.Args[clusterCAFlagIndex+1]).To(Equal("/etc/konnectivity/ca/ca.crt"))
}

func TestLoadStatefulSetManifest(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		componentName string
		templateData  map[string]string
		validate      func(g Gomega, sts *appsv1.StatefulSet, err error)
	}{
		{
			name:          "When loading a templated statefulset manifest with Name=etcd, it should decode successfully",
			componentName: "etcd",
			templateData:  map[string]string{"Name": "etcd", "ClientServiceName": "etcd-client", "DiscoveryServiceName": "etcd-discovery"},
			validate: func(g Gomega, sts *appsv1.StatefulSet, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(sts).ToNot(BeNil())
				g.Expect(sts.Kind).To(Equal("StatefulSet"))
				g.Expect(sts.Name).To(Equal("etcd"))
				g.Expect(sts.Spec.Selector.MatchLabels["app"]).To(Equal("etcd"))
				g.Expect(sts.Spec.Template.Labels["app"]).To(Equal("etcd"))
				g.Expect(sts.Spec.ServiceName).To(Equal("etcd-discovery"))
			},
		},
		{
			name:          "When loading a templated statefulset manifest with shard name, it should produce shard-specific names",
			componentName: "etcd",
			templateData:  map[string]string{"Name": "etcd-events", "ClientServiceName": "etcd-client-events", "DiscoveryServiceName": "etcd-discovery-events"},
			validate: func(g Gomega, sts *appsv1.StatefulSet, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(sts).ToNot(BeNil())
				g.Expect(sts.Name).To(Equal("etcd-events"))
				g.Expect(sts.Spec.Selector.MatchLabels["app"]).To(Equal("etcd-events"))
				g.Expect(sts.Spec.Template.Labels["app"]).To(Equal("etcd-events"))
				g.Expect(sts.Spec.ServiceName).To(Equal("etcd-discovery-events"))
			},
		},
		{
			name:          "When component name does not exist, it should return an error",
			componentName: "nonexistent-component",
			validate: func(g Gomega, sts *appsv1.StatefulSet, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(sts).To(BeNil())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			sts, err := LoadStatefulSetManifestTemplated(tc.componentName, tc.templateData)
			tc.validate(g, sts, err)
		})
	}
}

func TestLoadCronJobManifest(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		componentName string
		validate      func(g Gomega, cronJob *batchv1.CronJob, err error)
	}{
		{
			name:          "When loading a valid cronjob manifest, it should decode successfully",
			componentName: "olm-collect-profiles",
			validate: func(g Gomega, cronJob *batchv1.CronJob, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cronJob).ToNot(BeNil())
				g.Expect(cronJob.Kind).To(Equal("CronJob"))
				g.Expect(cronJob.Name).To(Equal("olm-collect-profiles"))
			},
		},
		{
			name:          "When component name does not exist, it should return an error",
			componentName: "nonexistent-component",
			validate: func(g Gomega, cronJob *batchv1.CronJob, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(cronJob).To(BeNil())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cronJob, err := LoadCronJobManifest(tc.componentName)
			tc.validate(g, cronJob, err)
		})
	}
}

func TestLoadJobManifest(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		componentName string
		validate      func(g Gomega, job *batchv1.Job, err error)
	}{
		{
			name:          "When loading a valid job manifest, it should decode successfully",
			componentName: "featuregate-generator",
			validate: func(g Gomega, job *batchv1.Job, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(job).ToNot(BeNil())
				g.Expect(job.Kind).To(Equal("Job"))
				g.Expect(job.Name).To(Equal("featuregate-generator"))
			},
		},
		{
			name:          "When component name does not exist, it should return an error",
			componentName: "nonexistent-component",
			validate: func(g Gomega, job *batchv1.Job, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(job).To(BeNil())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			job, err := LoadJobManifest(tc.componentName)
			tc.validate(g, job, err)
		})
	}
}

func TestLoadManifest(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		componentName string
		fileName      string
		validate      func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error)
	}{
		{
			name:          "When loading a service manifest, it should decode successfully",
			componentName: "cluster-autoscaler",
			fileName:      "serviceaccount.yaml",
			validate: func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(obj).ToNot(BeNil())
				g.Expect(gvk).ToNot(BeNil())
				g.Expect(gvk.Kind).To(Equal("ServiceAccount"))
				sa, ok := obj.(*corev1.ServiceAccount)
				g.Expect(ok).To(BeTrue())
				g.Expect(sa.Name).To(Equal("cluster-autoscaler"))
			},
		},
		{
			name:          "When loading a role manifest, it should decode successfully",
			componentName: "cluster-autoscaler",
			fileName:      "role.yaml",
			validate: func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(obj).ToNot(BeNil())
				g.Expect(gvk).ToNot(BeNil())
				g.Expect(gvk.Kind).To(Equal("Role"))
			},
		},
		{
			name:          "When file does not exist, it should return an error",
			componentName: "cluster-autoscaler",
			fileName:      "nonexistent.yaml",
			validate: func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(obj).To(BeNil())
				g.Expect(gvk).To(BeNil())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			obj, gvk, err := LoadManifest(tc.componentName, tc.fileName)
			tc.validate(g, obj, gvk, err)
		})
	}
}

func TestLoadManifestInto(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		componentName string
		fileName      string
		into          client.Object
		validate      func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error)
	}{
		{
			name:          "When loading into a pre-allocated ServiceAccount, it should populate the object",
			componentName: "cluster-autoscaler",
			fileName:      "serviceaccount.yaml",
			into:          &corev1.ServiceAccount{},
			validate: func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(obj).ToNot(BeNil())
				g.Expect(gvk).ToNot(BeNil())
				g.Expect(gvk.Kind).To(Equal("ServiceAccount"))
				sa, ok := obj.(*corev1.ServiceAccount)
				g.Expect(ok).To(BeTrue())
				g.Expect(sa.Name).To(Equal("cluster-autoscaler"))
			},
		},
		{
			name:          "When loading into nil, it should create a new object",
			componentName: "cluster-autoscaler",
			fileName:      "serviceaccount.yaml",
			into:          nil,
			validate: func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(obj).ToNot(BeNil())
				g.Expect(gvk).ToNot(BeNil())
				g.Expect(gvk.Kind).To(Equal("ServiceAccount"))
			},
		},
		{
			name:          "When file does not exist, it should return an error",
			componentName: "cluster-autoscaler",
			fileName:      "nonexistent.yaml",
			into:          &corev1.ServiceAccount{},
			validate: func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error) {
				g.Expect(err).To(HaveOccurred())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			obj, gvk, err := LoadManifestInto(tc.componentName, tc.fileName, tc.into)
			tc.validate(g, obj, gvk, err)
		})
	}
}

func TestForEachManifest(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		componentName string
		validate      func(g Gomega, manifestNames []string, err error)
	}{
		{
			name:          "When iterating over cluster-autoscaler manifests, it should skip deployment and call action for others",
			componentName: "cluster-autoscaler",
			validate: func(g Gomega, manifestNames []string, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(manifestNames).To(ContainElement("serviceaccount.yaml"))
				g.Expect(manifestNames).To(ContainElement("role.yaml"))
				g.Expect(manifestNames).To(ContainElement("rolebinding.yaml"))
				g.Expect(manifestNames).To(ContainElement("podmonitor.yaml"))
				g.Expect(manifestNames).ToNot(ContainElement("deployment.yaml"))
			},
		},
		{
			name:          "When iterating over etcd manifests, it should skip statefulset and call action for others",
			componentName: "etcd",
			validate: func(g Gomega, manifestNames []string, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(manifestNames).To(ContainElement("service.yaml"))
				g.Expect(manifestNames).To(ContainElement("discovery-service.yaml"))
				g.Expect(manifestNames).ToNot(ContainElement("statefulset.yaml"))
			},
		},
		{
			name:          "When iterating over openshift-controller-manager manifests, it should skip deployment and call action for others",
			componentName: "openshift-controller-manager",
			validate: func(g Gomega, manifestNames []string, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(manifestNames).To(ContainElement("config.yaml"))
				g.Expect(manifestNames).To(ContainElement("service.yaml"))
				g.Expect(manifestNames).ToNot(ContainElement("deployment.yaml"))
			},
		},
		{
			name:          "When iterating over featuregate-generator manifests, it should skip job and call action for others",
			componentName: "featuregate-generator",
			validate: func(g Gomega, manifestNames []string, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				// featuregate-generator only has job.yaml, so no other manifests
				g.Expect(manifestNames).To(BeEmpty())
			},
		},
		{
			name:          "When component does not exist, it should return an error",
			componentName: "nonexistent-component",
			validate: func(g Gomega, manifestNames []string, err error) {
				g.Expect(err).To(HaveOccurred())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			var manifestNames []string
			err := ForEachManifest(tc.componentName, func(manifestName string) error {
				manifestNames = append(manifestNames, manifestName)
				return nil
			})

			tc.validate(g, manifestNames, err)
		})
	}
}

func TestForEachManifestWithActionError(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	expectedErr := &testError{msg: "action failed"}

	err := ForEachManifest("cluster-autoscaler", func(manifestName string) error {
		if manifestName == "role.yaml" {
			return expectedErr
		}
		return nil
	})

	g.Expect(err).To(HaveOccurred())
	g.Expect(err).To(Equal(expectedErr))
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

func TestLoadManifestTemplated(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		componentName string
		fileName      string
		templateData  map[string]string
		validate      func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error)
	}{
		{
			name:          "When Name=etcd, it should render service.yaml to original hardcoded values",
			componentName: "etcd",
			fileName:      "service.yaml",
			templateData:  map[string]string{"Name": "etcd", "ClientServiceName": "etcd-client", "DiscoveryServiceName": "etcd-discovery"},
			validate: func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(obj.GetName()).To(Equal("etcd-client"))
				svc := obj.(*corev1.Service)
				g.Expect(svc.Labels["app"]).To(Equal("etcd"))
				g.Expect(svc.Spec.Selector["app"]).To(Equal("etcd"))
			},
		},
		{
			name:          "When Name=etcd-events, it should render service.yaml with shard-specific names",
			componentName: "etcd",
			fileName:      "service.yaml",
			templateData:  map[string]string{"Name": "etcd-events", "ClientServiceName": "etcd-client-events", "DiscoveryServiceName": "etcd-discovery-events"},
			validate: func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(obj.GetName()).To(Equal("etcd-client-events"))
				svc := obj.(*corev1.Service)
				g.Expect(svc.Labels["app"]).To(Equal("etcd-events"))
				g.Expect(svc.Spec.Selector["app"]).To(Equal("etcd-events"))
			},
		},
		{
			name:          "When Name=etcd, it should render discovery-service.yaml to original values",
			componentName: "etcd",
			fileName:      "discovery-service.yaml",
			templateData:  map[string]string{"Name": "etcd", "ClientServiceName": "etcd-client", "DiscoveryServiceName": "etcd-discovery"},
			validate: func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(obj.GetName()).To(Equal("etcd-discovery"))
				svc := obj.(*corev1.Service)
				g.Expect(svc.Spec.Selector["app"]).To(Equal("etcd"))
			},
		},
		{
			name:          "When Name=etcd-events, it should render discovery-service.yaml with shard names",
			componentName: "etcd",
			fileName:      "discovery-service.yaml",
			templateData:  map[string]string{"Name": "etcd-events", "ClientServiceName": "etcd-client-events", "DiscoveryServiceName": "etcd-discovery-events"},
			validate: func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(obj.GetName()).To(Equal("etcd-discovery-events"))
				svc := obj.(*corev1.Service)
				g.Expect(svc.Spec.Selector["app"]).To(Equal("etcd-events"))
			},
		},
		{
			name:          "When Name=etcd, it should render pdb.yaml to original values",
			componentName: "etcd",
			fileName:      "pdb.yaml",
			templateData:  map[string]string{"Name": "etcd", "ClientServiceName": "etcd-client", "DiscoveryServiceName": "etcd-discovery"},
			validate: func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(obj.GetName()).To(Equal("etcd"))
			},
		},
		{
			name:          "When Name=etcd-events, it should render pdb.yaml with shard name",
			componentName: "etcd",
			fileName:      "pdb.yaml",
			templateData:  map[string]string{"Name": "etcd-events", "ClientServiceName": "etcd-client-events", "DiscoveryServiceName": "etcd-discovery-events"},
			validate: func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(obj.GetName()).To(Equal("etcd-events"))
			},
		},
		{
			name:          "When templateData is nil, it should fall through to raw decode",
			componentName: "cluster-autoscaler",
			fileName:      "serviceaccount.yaml",
			templateData:  nil,
			validate: func(g Gomega, obj client.Object, gvk *schema.GroupVersionKind, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(obj.GetName()).To(Equal("cluster-autoscaler"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			obj, gvk, err := LoadManifestTemplated(tc.componentName, tc.fileName, tc.templateData)
			tc.validate(g, obj, gvk, err)
		})
	}
}
