/*
Copyright 2021 The Kubernetes Authors.

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

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-11-01/compute"
	"golang.org/x/crypto/ssh"
	"k8s.io/apimachinery/pkg/util/uuid"
	utilSSH "sigs.k8s.io/cluster-api-provider-azure/util/ssh"
	ctrl "sigs.k8s.io/controller-runtime"
)

// SetDefaultSSHPublicKey sets the default SSHPublicKey for an AzureMachine.
func (s *AzureMachineSpec) SetDefaultSSHPublicKey() error {
	if sshKeyData := s.SSHPublicKey; sshKeyData == "" {
		_, publicRsaKey, err := utilSSH.GenerateSSHKey()
		if err != nil {
			return err
		}

		s.SSHPublicKey = base64.StdEncoding.EncodeToString(ssh.MarshalAuthorizedKey(publicRsaKey))
	}
	return nil
}

// SetDefaultCachingType sets the default cache type for an AzureMachine.
func (s *AzureMachineSpec) SetDefaultCachingType() {
	if s.OSDisk.CachingType == "" {
		s.OSDisk.CachingType = "None"
	}
}

// SetDataDisksDefaults sets the data disk defaults for an AzureMachine.
func (s *AzureMachineSpec) SetDataDisksDefaults() {
	set := make(map[int32]struct{})
	// populate all the existing values in the set
	for _, disk := range s.DataDisks {
		if disk.Lun != nil {
			set[*disk.Lun] = struct{}{}
		}
	}
	// Look for unique values for unassigned LUNs
	for i, disk := range s.DataDisks {
		if disk.Lun == nil {
			for l := range s.DataDisks {
				lun := int32(l)
				if _, ok := set[lun]; !ok {
					s.DataDisks[i].Lun = &lun
					set[lun] = struct{}{}
					break
				}
			}
		}
		if disk.CachingType == "" {
			if s.DataDisks[i].ManagedDisk != nil &&
				s.DataDisks[i].ManagedDisk.StorageAccountType == string(compute.StorageAccountTypesUltraSSDLRS) {
				s.DataDisks[i].CachingType = string(compute.CachingTypesNone)
			} else {
				s.DataDisks[i].CachingType = string(compute.CachingTypesReadWrite)
			}
		}
	}
}

// SetIdentityDefaults sets the defaults for VM Identity.
func (s *AzureMachineSpec) SetIdentityDefaults() {
	if s.Identity == VMIdentitySystemAssigned {
		if s.RoleAssignmentName == "" {
			s.RoleAssignmentName = string(uuid.NewUUID())
		}
	}
}

// SetSpotEvictionPolicyDefaults sets the defaults for the spot VM eviction policy.
func (s *AzureMachineSpec) SetSpotEvictionPolicyDefaults() {
	if s.SpotVMOptions != nil && s.SpotVMOptions.EvictionPolicy == nil {
		defaultPolicy := SpotEvictionPolicyDeallocate
		if s.OSDisk.DiffDiskSettings != nil && s.OSDisk.DiffDiskSettings.Option == "Local" {
			defaultPolicy = SpotEvictionPolicyDelete
		}
		s.SpotVMOptions.EvictionPolicy = &defaultPolicy
	}
}

// SetDefaults sets to the defaults for the AzureMachineSpec.
func (s *AzureMachineSpec) SetDefaults() {
	if err := s.SetDefaultSSHPublicKey(); err != nil {
		ctrl.Log.WithName("SetDefault").Error(err, "SetDefaultSshPublicKey failed")
	}
	s.SetDefaultCachingType()
	s.SetDataDisksDefaults()
	s.SetIdentityDefaults()
	s.SetSpotEvictionPolicyDefaults()
}
