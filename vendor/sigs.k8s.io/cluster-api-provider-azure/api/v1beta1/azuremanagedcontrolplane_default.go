/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/ssh"
	"k8s.io/utils/pointer"
	utilSSH "sigs.k8s.io/cluster-api-provider-azure/util/ssh"
)

const (
	// defaultAKSVnetCIDR is the default Vnet CIDR.
	defaultAKSVnetCIDR = "10.0.0.0/8"
	// defaultAKSNodeSubnetCIDR is the default Node Subnet CIDR.
	defaultAKSNodeSubnetCIDR = "10.240.0.0/16"
)

// setDefaultSSHPublicKey sets the default SSHPublicKey for an AzureManagedControlPlane.
func (m *AzureManagedControlPlane) setDefaultSSHPublicKey() error {
	if sshKeyData := m.Spec.SSHPublicKey; sshKeyData == "" {
		_, publicRsaKey, err := utilSSH.GenerateSSHKey()
		if err != nil {
			return err
		}

		m.Spec.SSHPublicKey = base64.StdEncoding.EncodeToString(ssh.MarshalAuthorizedKey(publicRsaKey))
	}

	return nil
}

// setDefaultNodeResourceGroupName sets the default NodeResourceGroup for an AzureManagedControlPlane.
func (m *AzureManagedControlPlane) setDefaultNodeResourceGroupName() {
	if m.Spec.NodeResourceGroupName == "" {
		m.Spec.NodeResourceGroupName = fmt.Sprintf("MC_%s_%s_%s", m.Spec.ResourceGroupName, m.Name, m.Spec.Location)
	}
}

// setDefaultVirtualNetwork sets the default VirtualNetwork for an AzureManagedControlPlane.
func (m *AzureManagedControlPlane) setDefaultVirtualNetwork() {
	if m.Spec.VirtualNetwork.Name == "" {
		m.Spec.VirtualNetwork.Name = m.Name
	}
	if m.Spec.VirtualNetwork.CIDRBlock == "" {
		m.Spec.VirtualNetwork.CIDRBlock = defaultAKSVnetCIDR
	}
	if m.Spec.VirtualNetwork.ResourceGroup == "" {
		m.Spec.VirtualNetwork.ResourceGroup = m.Spec.ResourceGroupName
	}
}

// setDefaultSubnet sets the default Subnet for an AzureManagedControlPlane.
func (m *AzureManagedControlPlane) setDefaultSubnet() {
	if m.Spec.VirtualNetwork.Subnet.Name == "" {
		m.Spec.VirtualNetwork.Subnet.Name = m.Name
	}
	if m.Spec.VirtualNetwork.Subnet.CIDRBlock == "" {
		m.Spec.VirtualNetwork.Subnet.CIDRBlock = defaultAKSNodeSubnetCIDR
	}
}

func (m *AzureManagedControlPlane) setDefaultSku() {
	if m.Spec.SKU == nil {
		m.Spec.SKU = &AKSSku{
			Tier: FreeManagedControlPlaneTier,
		}
	}
}

func (m *AzureManagedControlPlane) setDefaultAutoScalerProfile() {
	if m.Spec.AutoScalerProfile == nil {
		return
	}

	// Default values are from https://learn.microsoft.com/en-us/azure/aks/cluster-autoscaler#using-the-autoscaler-profile
	// If any values are set, they all need to be set.
	if m.Spec.AutoScalerProfile.BalanceSimilarNodeGroups == nil {
		m.Spec.AutoScalerProfile.BalanceSimilarNodeGroups = (*BalanceSimilarNodeGroups)(pointer.String(string(BalanceSimilarNodeGroupsFalse)))
	}
	if m.Spec.AutoScalerProfile.Expander == nil {
		m.Spec.AutoScalerProfile.Expander = (*Expander)(pointer.String(string(ExpanderRandom)))
	}
	if m.Spec.AutoScalerProfile.MaxEmptyBulkDelete == nil {
		m.Spec.AutoScalerProfile.MaxEmptyBulkDelete = pointer.String("10")
	}
	if m.Spec.AutoScalerProfile.MaxGracefulTerminationSec == nil {
		m.Spec.AutoScalerProfile.MaxGracefulTerminationSec = pointer.String("600")
	}
	if m.Spec.AutoScalerProfile.MaxNodeProvisionTime == nil {
		m.Spec.AutoScalerProfile.MaxNodeProvisionTime = pointer.String("15m")
	}
	if m.Spec.AutoScalerProfile.MaxTotalUnreadyPercentage == nil {
		m.Spec.AutoScalerProfile.MaxTotalUnreadyPercentage = pointer.String("45")
	}
	if m.Spec.AutoScalerProfile.NewPodScaleUpDelay == nil {
		m.Spec.AutoScalerProfile.NewPodScaleUpDelay = pointer.String("0s")
	}
	if m.Spec.AutoScalerProfile.OkTotalUnreadyCount == nil {
		m.Spec.AutoScalerProfile.OkTotalUnreadyCount = pointer.String("3")
	}
	if m.Spec.AutoScalerProfile.ScanInterval == nil {
		m.Spec.AutoScalerProfile.ScanInterval = pointer.String("10s")
	}
	if m.Spec.AutoScalerProfile.ScaleDownDelayAfterAdd == nil {
		m.Spec.AutoScalerProfile.ScaleDownDelayAfterAdd = pointer.String("10m")
	}
	if m.Spec.AutoScalerProfile.ScaleDownDelayAfterDelete == nil {
		// Default is the same as the ScanInterval so default to that same value if it isn't set
		m.Spec.AutoScalerProfile.ScaleDownDelayAfterDelete = m.Spec.AutoScalerProfile.ScanInterval
	}
	if m.Spec.AutoScalerProfile.ScaleDownDelayAfterFailure == nil {
		m.Spec.AutoScalerProfile.ScaleDownDelayAfterFailure = pointer.String("3m")
	}
	if m.Spec.AutoScalerProfile.ScaleDownUnneededTime == nil {
		m.Spec.AutoScalerProfile.ScaleDownUnneededTime = pointer.String("10m")
	}
	if m.Spec.AutoScalerProfile.ScaleDownUnreadyTime == nil {
		m.Spec.AutoScalerProfile.ScaleDownUnreadyTime = pointer.String("20m")
	}
	if m.Spec.AutoScalerProfile.ScaleDownUtilizationThreshold == nil {
		m.Spec.AutoScalerProfile.ScaleDownUtilizationThreshold = pointer.String("0.5")
	}
	if m.Spec.AutoScalerProfile.SkipNodesWithLocalStorage == nil {
		m.Spec.AutoScalerProfile.SkipNodesWithLocalStorage = (*SkipNodesWithLocalStorage)(pointer.String(string(SkipNodesWithLocalStorageFalse)))
	}
	if m.Spec.AutoScalerProfile.SkipNodesWithSystemPods == nil {
		m.Spec.AutoScalerProfile.SkipNodesWithSystemPods = (*SkipNodesWithSystemPods)(pointer.String(string(SkipNodesWithSystemPodsTrue)))
	}
}
