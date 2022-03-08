package nodepool

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"golang.org/x/crypto/ssh"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
)

func azureMachineTemplateSpec(hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool, existing capiazure.AzureMachineTemplateSpec) (*capiazure.AzureMachineTemplateSpec, error) {
	// The azure api requires to pass a public key. This key is randomly generated, the private portion is thrown away and the public key
	// gets written to the template.
	sshKey := existing.Template.Spec.SSHPublicKey
	if sshKey == "" {
		var err error
		sshKey, err = generateSSHPubkey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate a SSH key: %w", err)
		}
	}
	return &capiazure.AzureMachineTemplateSpec{Template: capiazure.AzureMachineTemplateResource{Spec: capiazure.AzureMachineSpec{
		VMSize:                 nodePool.Spec.Platform.Azure.VMSize,
		Image:                  &capiazure.Image{ID: &nodePool.Spec.Platform.Azure.ImageID},
		SubnetName:             hcluster.Spec.Platform.Azure.SubnetName,
		Identity:               capiazure.VMIdentityUserAssigned,
		UserAssignedIdentities: []capiazure.UserAssignedIdentity{{ProviderID: hcluster.Spec.Platform.Azure.MachineIdentityID}},
		SSHPublicKey:           sshKey,
	}}}, nil
}

func generateSSHPubkey() (string, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", fmt.Errorf("failed to generate private key: %w", err)
	}

	publicRsaKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", fmt.Errorf("failed to generate public key: %w", err)
	}

	return base64.StdEncoding.EncodeToString(ssh.MarshalAuthorizedKey(publicRsaKey)), nil
}
