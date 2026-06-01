package assets

import (
	"testing"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
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
}

func TestLoadStatefulSetManifest(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		componentName string
		validate      func(g Gomega, sts *appsv1.StatefulSet, err error)
	}{
		{
			name:          "When loading a valid statefulset manifest, it should decode successfully",
			componentName: "etcd",
			validate: func(g Gomega, sts *appsv1.StatefulSet, err error) {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(sts).ToNot(BeNil())
				g.Expect(sts.Kind).To(Equal("StatefulSet"))
				g.Expect(sts.Name).To(Equal("etcd"))
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

			sts, err := LoadStatefulSetManifest(tc.componentName)
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
