package routecm

import (
	"context"
	"testing"

	v1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	hyperapi "github.com/openshift/hypershift/support/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcile(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ClusterID: "cluster-id",
			Configuration: &hyperv1.ClusterConfiguration{
				APIServer: &v1.APIServerSpec{
					TLSSecurityProfile: &v1.TLSSecurityProfile{
						Type: v1.TLSProfileOldType,
					},
				},
			},
		},
	}

	client := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
	cpContext := controlplanecomponent.ControlPlaneContext{
		Context:                  context.Background(),
		Client:                   client,
		CreateOrUpdateProviderV2: upsert.NewV2(false),
		ReleaseImageProvider:     testutil.FakeImageProvider(),
		HCP:                      hcp,
	}

	compoent := NewComponent()
	if err := compoent.Reconcile(cpContext); err != nil {
		t.Fatalf("failed to reconcile routecm: %v", err)
	}

	var deployments appsv1.DeploymentList
	if err := client.List(context.Background(), &deployments); err != nil {
		t.Fatalf("failed to list deployments: %v", err)
	}

	if len(deployments.Items) == 0 {
		t.Fatalf("expected deployment to exist")
	}

	deploymentYaml, err := util.SerializeResource(&deployments.Items[0], hyperapi.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, deploymentYaml, testutil.WithSuffix("_deployment"))

	// check configMap is created
	var configMaps corev1.ConfigMapList
	if err := client.List(context.Background(), &configMaps); err != nil {
		t.Fatalf("failed to list configMaps: %v", err)
	}

	if len(configMaps.Items) == 0 {
		t.Fatalf("expected configMap to exist")
	}

	configMapYaml, err := util.SerializeResource(&configMaps.Items[0], hyperapi.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, configMapYaml, testutil.WithSuffix("_configMap"))

	// check serviceMonitor is created
	var serviceMonitors prometheusoperatorv1.ServiceMonitorList
	if err := client.List(context.Background(), &serviceMonitors); err != nil {
		t.Fatalf("failed to list configMaps: %v", err)
	}

	if len(serviceMonitors.Items) == 0 {
		t.Fatalf("expected serviceMonitor to exist")
	}

	serviceMonitorYaml, err := util.SerializeResource(serviceMonitors.Items[0], hyperapi.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, serviceMonitorYaml, testutil.WithSuffix("_servicemonitor"))
}
