package kcm

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestKCMArgs(t *testing.T) {
	testCases := []struct {
		name     string
		p        *KubeControllerManagerParams
		expected []string
	}{
		{
			name: "Leader elect args get set correctly",
			p:    &KubeControllerManagerParams{},
			expected: []string{
				"--leader-elect-resource-lock=configmapsleases",
				"--leader-elect=true",
				// Contrary to everything else, KCM should not have an increased lease duration, see
				// https://github.com/openshift/cluster-kube-controller-manager-operator/pull/557#issuecomment-904648807
				"--leader-elect-retry-period=3s",
			},
		},
	}

	allowedDuplicateArgs := sets.NewString(
		"--controllers",
		"--feature-gates",
	)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			args := kcmArgs(tc.p)

			seen := sets.String{}
			for _, arg := range args {
				key := strings.Split(arg, "=")[0]
				if allowedDuplicateArgs.Has(key) {
					continue
				}
				if seen.Has(key) {
					t.Errorf("duplicate arg %s found", key)
				}
				seen.Insert(key)
			}

			argSet := sets.NewString(args...)
			for _, arg := range tc.expected {
				if !argSet.Has(arg) {
					t.Errorf("expected arg %s not found", arg)
				}
			}
		})
	}
}

func TestKubeControllerManagerDeployment(t *testing.T) {

	// Setup hypershift hosted control plane.
	targetNamespace := "test"
	kcmDeployment := manifests.KCMDeployment(targetNamespace)
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: targetNamespace,
		},
	}
	hcp.Name = "name"
	hcp.Namespace = "namespace"

	testCases := []struct {
		cm               corev1.ConfigMap
		params           KubeControllerManagerParams
		deploymentConfig config.DeploymentConfig
	}{
		// empty deployment config and params
		{
			cm: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-kcm-config",
					Namespace: targetNamespace,
				},
				Data: map[string]string{"config.json": "test-data"},
			},
			deploymentConfig: config.DeploymentConfig{},
			params:           KubeControllerManagerParams{},
		},
	}
	for _, tc := range testCases {
		g := NewGomegaWithT(t)
		kcmDeployment.Spec.MinReadySeconds = 60
		expectedMinReadySeconds := kcmDeployment.Spec.MinReadySeconds
		err := ReconcileDeployment(kcmDeployment, &tc.cm, nil, &tc.params, pointer.Int32(1234))
		g.Expect(err).To(BeNil())
		g.Expect(expectedMinReadySeconds).To(Equal(kcmDeployment.Spec.MinReadySeconds))
	}
}
