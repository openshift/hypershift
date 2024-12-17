package util

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"sigs.k8s.io/controller-runtime/pkg/client"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

type KubeVirtInfra struct {
	ctx           context.Context
	mgmtClient    crclient.Client
	hostedCluster *hyperv1.HostedCluster
	nadName       string
}

func NewKubeVirtInfra(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) KubeVirtInfra {
	return KubeVirtInfra{
		ctx:           ctx,
		mgmtClient:    mgmtClient,
		hostedCluster: hostedCluster,
		nadName:       SimpleNameGenerator.GenerateName("e2e-test"),
	}
}

func (k KubeVirtInfra) Namespace() string {
	creds := k.hostedCluster.Spec.Platform.Kubevirt.Credentials
	if creds != nil {
		return creds.InfraNamespace
	}
	return manifests.HostedControlPlaneNamespace(k.hostedCluster.Namespace, k.hostedCluster.Name)
}

func (k KubeVirtInfra) NADName() string {
	return k.nadName
}

func (k KubeVirtInfra) MGMTClient() crclient.Client {
	return k.mgmtClient
}

func (k KubeVirtInfra) Ctx() context.Context {
	return k.ctx
}

func (k KubeVirtInfra) HostedCluster() *hyperv1.HostedCluster {
	return k.hostedCluster
}

func (k KubeVirtInfra) DiscoverClient() (crclient.Client, error) {
	cm := kvinfra.NewKubevirtInfraClientMap()
	clusterClient, err := cm.DiscoverKubevirtClusterClient(k.ctx, k.mgmtClient, k.hostedCluster.Spec.InfraID, k.hostedCluster.Spec.Platform.Kubevirt.Credentials, k.Namespace(), k.hostedCluster.Namespace)
	if err != nil {
		return nil, err
	}
	return clusterClient.GetInfraClient(), nil
}

func (k KubeVirtInfra) ComposeOVNKLayer2NAD(namespace string) (client.Object, error) {
	nadYAML := fmt.Sprintf(`
apiVersion: "k8s.cni.cncf.io/v1"
kind: NetworkAttachmentDefinition
metadata:
  namespace: %[1]s
  name: %[2]s
spec:
  config: |2
    {
            "cniVersion": "0.3.1",
            "name": "%[2]s",
            "type": "ovn-k8s-cni-overlay",
            "topology":"layer2",
            "netAttachDefName": "%[1]s/%[2]s"
    }
`, namespace, k.nadName)
	nadJSON, err := yaml.YAMLToJSON([]byte(nadYAML))
	if err != nil {
		return nil, fmt.Errorf("failed converting net-attach-def yaml to json: %w", err)
	}
	nad := &unstructured.Unstructured{Object: map[string]interface{}{}}
	if err := nad.UnmarshalJSON(nadJSON); err != nil {
		return nil, fmt.Errorf("failed unmarshaling net-attach-def: %w", err)
	}
	return nad, nil
}
func (k KubeVirtInfra) CreateOVNKLayer2NAD(namespace string) error {
	nad, err := k.ComposeOVNKLayer2NAD(namespace)
	if err != nil {
		return err
	}
	infraClient, err := k.DiscoverClient()
	if err != nil {
		return err
	}

	if err := infraClient.Create(k.ctx, nad); err != nil {
		return fmt.Errorf("failed creating net-attach-def: %+v: %w", nad, err)
	}
	return nil
}
