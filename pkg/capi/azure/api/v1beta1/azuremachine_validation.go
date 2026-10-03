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
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
)

// ValidateNetwork validates the network configuration.
func ValidateNetwork(subnetName string, acceleratedNetworking *bool, networkInterfaces []NetworkInterface, fldPath *field.Path) field.ErrorList {
	if (networkInterfaces != nil) && len(networkInterfaces) > 0 && subnetName != "" {
		return field.ErrorList{field.Invalid(fldPath, networkInterfaces, "cannot set both networkInterfaces and machine subnetName")}
	}

	if (networkInterfaces != nil) && len(networkInterfaces) > 0 && acceleratedNetworking != nil {
		return field.ErrorList{field.Invalid(fldPath, networkInterfaces, "cannot set both networkInterfaces and machine acceleratedNetworking")}
	}

	for _, nic := range networkInterfaces {
		if nic.PrivateIPConfigs < 1 {
			return field.ErrorList{field.Invalid(fldPath, networkInterfaces, "number of privateIPConfigs per interface must be at least 1")}
		}
	}

	return field.ErrorList{}
}

// ValidateSystemAssignedIdentityRole validates the system-assigned identity role.
func ValidateSystemAssignedIdentityRole(identityType VMIdentity, roleAssignmentName string, role *SystemAssignedIdentityRole, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if roleAssignmentName != "" && role != nil && role.Name != "" {
		allErrs = append(allErrs, field.Invalid(fldPath, role.Name, "cannot set both roleAssignmentName and systemAssignedIdentityRole.name"))
	}
	if identityType == VMIdentitySystemAssigned && role != nil {
		if role.DefinitionID == "" {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "systemAssignedIdentityRole", "definitionID"), role.DefinitionID, "the definitionID field cannot be empty"))
		}
		if role.Scope == "" {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec", "systemAssignedIdentityRole", "scope"), role.Scope, "the scope field cannot be empty"))
		}
	}
	if identityType != VMIdentitySystemAssigned && role != nil {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "role"), "systemAssignedIdentityRole can only be set when identity is set to SystemAssigned"))
	}
	return allErrs
}

// validate that the disk size is between 4 and 32767.

// validate that all names are unique

// validate optional managed disk option

// validate that all LUNs are unique and between 0 and 63.

// validate cachingType

// DiskEncryptionSet can only be set when SecurityEncryptionType is set to DiskWithVMGuestState
// https://learn.microsoft.com/en-us/rest/api/compute/virtual-machines/create-or-update?tabs=HTTP#securityencryptiontypes

// ValidateDataDisksUpdate validates updates to Data disks.
func ValidateDataDisksUpdate(oldDataDisks, newDataDisks []DataDisk, fieldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	diskErrMsg := "adding/removing data disks after machine creation is not allowed"
	fieldErrMsg := "modifying data disk's fields after machine creation is not allowed"

	if len(oldDataDisks) != len(newDataDisks) {
		allErrs = append(allErrs, field.Invalid(fieldPath, newDataDisks, diskErrMsg))
		return allErrs
	}

	oldDisks := make(map[string]DataDisk)

	for _, disk := range oldDataDisks {
		oldDisks[disk.NameSuffix] = disk
	}

	for i, newDisk := range newDataDisks {
		if oldDisk, ok := oldDisks[newDisk.NameSuffix]; ok {
			if newDisk.DiskSizeGB != oldDisk.DiskSizeGB {
				allErrs = append(allErrs, field.Invalid(fieldPath.Index(i).Child("diskSizeGB"), newDataDisks, fieldErrMsg))
			}

			allErrs = append(allErrs, validateManagedDisksUpdate(oldDisk.ManagedDisk, newDisk.ManagedDisk, fieldPath.Index(i).Child("managedDisk"))...)

			if (newDisk.Lun != nil && oldDisk.Lun != nil) && (*newDisk.Lun != *oldDisk.Lun) {
				allErrs = append(allErrs, field.Invalid(fieldPath.Index(i).Child("lun"), newDataDisks, fieldErrMsg))
			} else if (newDisk.Lun != nil && oldDisk.Lun == nil) || (newDisk.Lun == nil && oldDisk.Lun != nil) {
				allErrs = append(allErrs, field.Invalid(fieldPath.Index(i).Child("lun"), newDataDisks, fieldErrMsg))
			}

			if newDisk.CachingType != oldDisk.CachingType {
				allErrs = append(allErrs, field.Invalid(fieldPath.Index(i).Child("cachingType"), newDataDisks, fieldErrMsg))
			}
		} else {
			allErrs = append(allErrs, field.Invalid(fieldPath.Index(i).Child("nameSuffix"), newDataDisks, diskErrMsg))
		}
	}

	return allErrs
}

func validateManagedDisksUpdate(oldDiskParams, newDiskParams *ManagedDiskParameters, fieldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	fieldErrMsg := "changing managed disk options after machine creation is not allowed"

	if newDiskParams != nil && oldDiskParams != nil {
		if newDiskParams.StorageAccountType != oldDiskParams.StorageAccountType {
			allErrs = append(allErrs, field.Invalid(fieldPath.Child("storageAccountType"), newDiskParams, fieldErrMsg))
		}
		if newDiskParams.DiskEncryptionSet != nil && oldDiskParams.DiskEncryptionSet != nil {
			if newDiskParams.DiskEncryptionSet.ID != oldDiskParams.DiskEncryptionSet.ID {
				allErrs = append(allErrs, field.Invalid(fieldPath.Child("diskEncryptionSet").Child("ID"), newDiskParams, fieldErrMsg))
			}
		} else if (newDiskParams.DiskEncryptionSet != nil && oldDiskParams.DiskEncryptionSet == nil) || (newDiskParams.DiskEncryptionSet == nil && oldDiskParams.DiskEncryptionSet != nil) {
			allErrs = append(allErrs, field.Invalid(fieldPath.Child("diskEncryptionSet"), newDiskParams, fieldErrMsg))
		}
	} else if (newDiskParams != nil && oldDiskParams == nil) || (newDiskParams == nil && oldDiskParams != nil) {
		allErrs = append(allErrs, field.Invalid(fieldPath, newDiskParams, fieldErrMsg))
	}

	return allErrs
}

// ValidateDiagnostics validates the Diagnostic spec.
func ValidateDiagnostics(diagnostics *Diagnostics, fieldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	if diagnostics != nil && diagnostics.Boot != nil {
		switch diagnostics.Boot.StorageAccountType {
		case UserManagedDiagnosticsStorage:
			if diagnostics.Boot.UserManaged == nil {
				allErrs = append(allErrs, field.Required(fieldPath.Child("UserManaged"),
					fmt.Sprintf("userManaged must be specified when storageAccountType is '%s'", UserManagedDiagnosticsStorage)))
			} else if diagnostics.Boot.UserManaged.StorageAccountURI == "" {
				allErrs = append(allErrs, field.Required(fieldPath.Child("StorageAccountURI"),
					fmt.Sprintf("StorageAccountURI cannot be empty when storageAccountType is '%s'", UserManagedDiagnosticsStorage)))
			}
		case ManagedDiagnosticsStorage:
			if diagnostics.Boot.UserManaged != nil &&
				diagnostics.Boot.UserManaged.StorageAccountURI != "" {
				allErrs = append(allErrs, field.Invalid(fieldPath.Child("StorageAccountURI"), diagnostics.Boot.UserManaged.StorageAccountURI,
					fmt.Sprintf("StorageAccountURI cannot be set when storageAccountType is '%s'",
						ManagedDiagnosticsStorage)))
			}
		case DisabledDiagnosticsStorage:
			if diagnostics.Boot.UserManaged != nil &&
				diagnostics.Boot.UserManaged.StorageAccountURI != "" {
				allErrs = append(allErrs, field.Invalid(fieldPath.Child("StorageAccountURI"), diagnostics.Boot.UserManaged.StorageAccountURI,
					fmt.Sprintf("StorageAccountURI cannot be set when storageAccountType is '%s'",
						ManagedDiagnosticsStorage)))
			}
		}
	}

	return allErrs
}

// ValidateConfidentialCompute validates the configuration options when the machine is a Confidential VM.
// https://learn.microsoft.com/en-us/rest/api/compute/virtual-machines/create-or-update?tabs=HTTP#vmdisksecurityprofile
// https://learn.microsoft.com/en-us/rest/api/compute/virtual-machines/create-or-update?tabs=HTTP#securityencryptiontypes
// https://learn.microsoft.com/en-us/rest/api/compute/virtual-machines/create-or-update?tabs=HTTP#uefisettings
func ValidateConfidentialCompute(managedDisk *ManagedDiskParameters, profile *SecurityProfile, fieldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	var securityEncryptionType SecurityEncryptionType

	if managedDisk != nil && managedDisk.SecurityProfile != nil {
		securityEncryptionType = managedDisk.SecurityProfile.SecurityEncryptionType
	}

	if profile != nil && securityEncryptionType != "" {
		// SecurityEncryptionType can only be set for Confindential VMs
		if profile.SecurityType != SecurityTypesConfidentialVM {
			allErrs = append(allErrs, field.Invalid(fieldPath.Child("SecurityType"), profile.SecurityType,
				fmt.Sprintf("SecurityType should be set to '%s' when securityEncryptionType is defined", SecurityTypesConfidentialVM)))
		}

		// Confidential VMs require vTPM to be enabled, irrespective of the SecurityEncryptionType used
		if profile.UefiSettings == nil {
			allErrs = append(allErrs, field.Invalid(fieldPath.Child("UefiSettings"), profile.UefiSettings,
				"UefiSettings should be set when securityEncryptionType is defined"))
		}

		if profile.UefiSettings != nil && (profile.UefiSettings.VTpmEnabled == nil || !*profile.UefiSettings.VTpmEnabled) {
			allErrs = append(allErrs, field.Invalid(fieldPath.Child("VTpmEnabled"), profile.UefiSettings.VTpmEnabled,
				"VTpmEnabled should be set to true when securityEncryptionType is defined"))
		}

		if securityEncryptionType == SecurityEncryptionTypeDiskWithVMGuestState {
			// DiskWithVMGuestState encryption type is not compatible with EncryptionAtHost
			if profile.EncryptionAtHost != nil && *profile.EncryptionAtHost {
				allErrs = append(allErrs, field.Invalid(fieldPath.Child("EncryptionAtHost"), profile.EncryptionAtHost,
					fmt.Sprintf("EncryptionAtHost cannot be set to 'true' when securityEncryptionType is set to '%s'", SecurityEncryptionTypeDiskWithVMGuestState)))
			}

			// DiskWithVMGuestState encryption type requires SecureBoot to be enabled
			if profile.UefiSettings != nil && (profile.UefiSettings.SecureBootEnabled == nil || !*profile.UefiSettings.SecureBootEnabled) {
				allErrs = append(allErrs, field.Invalid(fieldPath.Child("SecureBootEnabled"), profile.UefiSettings.SecureBootEnabled,
					fmt.Sprintf("SecureBootEnabled should be set to true when securityEncryptionType is set to '%s'", SecurityEncryptionTypeDiskWithVMGuestState)))
			}
		}
	}

	return allErrs
}

// ValidateVMExtensions validates the VMExtensions spec.
func ValidateVMExtensions(disableExtensionOperations *bool, vmExtensions []VMExtension, _ *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if ptr.Deref(disableExtensionOperations, false) && len(vmExtensions) > 0 {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("AzureMachineTemplate", "spec", "template", "spec", "vmExtensions"), "VMExtensions must be empty when DisableExtensionOperations is true"))
	}

	return allErrs
}
