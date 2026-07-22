package storage

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileOperandTolerations(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)

	newDeployment := func(name, namespace string, tolerations ...corev1.Toleration) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": name},
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": name},
					},
					Spec: corev1.PodSpec{
						Containers:  []corev1.Container{{Name: "test", Image: "test:latest"}},
						Tolerations: tolerations,
					},
				},
			},
		}
	}

	testCases := []struct {
		name                string
		platform            hyperv1.PlatformType
		hcpNamespace        string
		userTolerations     []corev1.Toleration
		existingDeployments []client.Object
		expectedTolerations map[string][]corev1.Toleration
		expectNoChange      map[string]bool
	}{
		{
			name:         "When AWS platform with user tolerations, it should patch operand deployments",
			platform:     hyperv1.AWSPlatform,
			hcpNamespace: "clusters-test",
			userTolerations: []corev1.Toleration{
				{Key: "taint-test", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
			},
			existingDeployments: []client.Object{
				newDeployment("aws-ebs-csi-driver-operator", "clusters-test"),
				newDeployment("aws-ebs-csi-driver-controller", "clusters-test"),
			},
			expectedTolerations: map[string][]corev1.Toleration{
				"aws-ebs-csi-driver-operator": {
					{Key: "hypershift.openshift.io/control-plane", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
					{Key: "hypershift.openshift.io/cluster", Operator: corev1.TolerationOpEqual, Value: "clusters-test", Effect: corev1.TaintEffectNoSchedule},
					{Key: "taint-test", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				},
				"aws-ebs-csi-driver-controller": {
					{Key: "hypershift.openshift.io/control-plane", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
					{Key: "hypershift.openshift.io/cluster", Operator: corev1.TolerationOpEqual, Value: "clusters-test", Effect: corev1.TaintEffectNoSchedule},
					{Key: "taint-test", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				},
			},
		},
		{
			name:         "When Azure platform with user tolerations, it should patch all Azure operand deployments",
			platform:     hyperv1.AzurePlatform,
			hcpNamespace: "clusters-azure",
			userTolerations: []corev1.Toleration{
				{Key: "custom-taint", Operator: corev1.TolerationOpEqual, Value: "val", Effect: corev1.TaintEffectNoExecute},
			},
			existingDeployments: []client.Object{
				newDeployment("azure-disk-csi-driver-operator", "clusters-azure"),
				newDeployment("azure-disk-csi-driver-controller", "clusters-azure"),
				newDeployment("azure-file-csi-driver-operator", "clusters-azure"),
				newDeployment("azure-file-csi-driver-controller", "clusters-azure"),
			},
			expectedTolerations: map[string][]corev1.Toleration{
				"azure-disk-csi-driver-operator": {
					{Key: "hypershift.openshift.io/control-plane", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
					{Key: "hypershift.openshift.io/cluster", Operator: corev1.TolerationOpEqual, Value: "clusters-azure", Effect: corev1.TaintEffectNoSchedule},
					{Key: "custom-taint", Operator: corev1.TolerationOpEqual, Value: "val", Effect: corev1.TaintEffectNoExecute},
				},
				"azure-disk-csi-driver-controller": {
					{Key: "hypershift.openshift.io/control-plane", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
					{Key: "hypershift.openshift.io/cluster", Operator: corev1.TolerationOpEqual, Value: "clusters-azure", Effect: corev1.TaintEffectNoSchedule},
					{Key: "custom-taint", Operator: corev1.TolerationOpEqual, Value: "val", Effect: corev1.TaintEffectNoExecute},
				},
				"azure-file-csi-driver-operator": {
					{Key: "hypershift.openshift.io/control-plane", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
					{Key: "hypershift.openshift.io/cluster", Operator: corev1.TolerationOpEqual, Value: "clusters-azure", Effect: corev1.TaintEffectNoSchedule},
					{Key: "custom-taint", Operator: corev1.TolerationOpEqual, Value: "val", Effect: corev1.TaintEffectNoExecute},
				},
				"azure-file-csi-driver-controller": {
					{Key: "hypershift.openshift.io/control-plane", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
					{Key: "hypershift.openshift.io/cluster", Operator: corev1.TolerationOpEqual, Value: "clusters-azure", Effect: corev1.TaintEffectNoSchedule},
					{Key: "custom-taint", Operator: corev1.TolerationOpEqual, Value: "val", Effect: corev1.TaintEffectNoExecute},
				},
			},
		},
		{
			name:            "When operand deployments do not exist yet, it should skip without error",
			platform:        hyperv1.AWSPlatform,
			hcpNamespace:    "clusters-new",
			userTolerations: []corev1.Toleration{{Key: "test", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}},
		},
		{
			name:         "When platform is not AWS or Azure, it should skip without error",
			platform:     hyperv1.KubevirtPlatform,
			hcpNamespace: "clusters-kv",
			existingDeployments: []client.Object{
				newDeployment("some-deployment", "clusters-kv"),
			},
		},
		{
			name:         "When tolerations already match, it should not patch",
			platform:     hyperv1.AWSPlatform,
			hcpNamespace: "clusters-existing",
			userTolerations: []corev1.Toleration{
				{Key: "my-taint", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
			},
			existingDeployments: []client.Object{
				newDeployment("aws-ebs-csi-driver-controller", "clusters-existing",
					corev1.Toleration{Key: "hypershift.openshift.io/control-plane", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
					corev1.Toleration{Key: "hypershift.openshift.io/cluster", Operator: corev1.TolerationOpEqual, Value: "clusters-existing", Effect: corev1.TaintEffectNoSchedule},
					corev1.Toleration{Key: "my-taint", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				),
				newDeployment("aws-ebs-csi-driver-operator", "clusters-existing",
					corev1.Toleration{Key: "hypershift.openshift.io/control-plane", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
					corev1.Toleration{Key: "hypershift.openshift.io/cluster", Operator: corev1.TolerationOpEqual, Value: "clusters-existing", Effect: corev1.TaintEffectNoSchedule},
					corev1.Toleration{Key: "my-taint", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				),
			},
			expectedTolerations: map[string][]corev1.Toleration{
				"aws-ebs-csi-driver-controller": {
					{Key: "hypershift.openshift.io/control-plane", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
					{Key: "hypershift.openshift.io/cluster", Operator: corev1.TolerationOpEqual, Value: "clusters-existing", Effect: corev1.TaintEffectNoSchedule},
					{Key: "my-taint", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				},
				"aws-ebs-csi-driver-operator": {
					{Key: "hypershift.openshift.io/control-plane", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
					{Key: "hypershift.openshift.io/cluster", Operator: corev1.TolerationOpEqual, Value: "clusters-existing", Effect: corev1.TaintEffectNoSchedule},
					{Key: "my-taint", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				},
			},
		},
		{
			name:         "When no user tolerations specified, it should still set control plane tolerations",
			platform:     hyperv1.AWSPlatform,
			hcpNamespace: "clusters-no-user-tol",
			existingDeployments: []client.Object{
				newDeployment("aws-ebs-csi-driver-controller", "clusters-no-user-tol"),
			},
			expectedTolerations: map[string][]corev1.Toleration{
				"aws-ebs-csi-driver-controller": {
					{Key: "hypershift.openshift.io/control-plane", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
					{Key: "hypershift.openshift.io/cluster", Operator: corev1.TolerationOpEqual, Value: "clusters-no-user-tol", Effect: corev1.TaintEffectNoSchedule},
				},
			},
		},
		{
			name:         "When multiple user tolerations specified, it should include all on operand deployments",
			platform:     hyperv1.AWSPlatform,
			hcpNamespace: "clusters-multi-tol",
			userTolerations: []corev1.Toleration{
				{Key: "taint-a", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "taint-b", Operator: corev1.TolerationOpEqual, Value: "b-val", Effect: corev1.TaintEffectNoExecute},
			},
			existingDeployments: []client.Object{
				newDeployment("aws-ebs-csi-driver-controller", "clusters-multi-tol"),
			},
			expectedTolerations: map[string][]corev1.Toleration{
				"aws-ebs-csi-driver-controller": {
					{Key: "hypershift.openshift.io/control-plane", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
					{Key: "hypershift.openshift.io/cluster", Operator: corev1.TolerationOpEqual, Value: "clusters-multi-tol", Effect: corev1.TaintEffectNoSchedule},
					{Key: "taint-a", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
					{Key: "taint-b", Operator: corev1.TolerationOpEqual, Value: "b-val", Effect: corev1.TaintEffectNoExecute},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			if len(tc.existingDeployments) > 0 {
				clientBuilder = clientBuilder.WithObjects(tc.existingDeployments...)
			}
			fakeClient := clientBuilder.Build()

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: tc.hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tc.platform,
					},
					Tolerations: tc.userTolerations,
				},
			}

			cpContext := component.ControlPlaneContext{
				Context: context.Background(),
				Client:  fakeClient,
				HCP:     hcp,
			}

			err := reconcileOperandTolerations(cpContext)
			g.Expect(err).ToNot(HaveOccurred())

			for deploymentName, expected := range tc.expectedTolerations {
				deployment := &appsv1.Deployment{}
				err := fakeClient.Get(context.Background(), client.ObjectKey{
					Namespace: tc.hcpNamespace,
					Name:      deploymentName,
				}, deployment)
				g.Expect(err).ToNot(HaveOccurred(), "deployment %s should exist", deploymentName)
				g.Expect(deployment.Spec.Template.Spec.Tolerations).To(Equal(expected),
					"deployment %s should have expected tolerations", deploymentName)
			}
		})
	}
}
