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
	"fmt"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-11-01/compute"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ValidateAzureMachineSpec check for validation errors of azuremachine.spec.
func ValidateAzureMachineSpec(spec AzureMachineSpec) field.ErrorList {
	var allErrs field.ErrorList

	if errs := ValidateImage(spec.Image, field.NewPath("image")); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	if errs := ValidateOSDisk(spec.OSDisk, field.NewPath("osDisk")); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	if errs := ValidateSSHKey(spec.SSHPublicKey, field.NewPath("sshPublicKey")); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	if errs := ValidateUserAssignedIdentity(spec.Identity, spec.UserAssignedIdentities, field.NewPath("userAssignedIdentities")); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	if errs := ValidateDataDisks(spec.DataDisks, field.NewPath("dataDisks")); len(errs) > 0 {
		allErrs = append(allErrs, errs...)
	}

	return allErrs
}

// ValidateSSHKey validates an SSHKey.
func ValidateSSHKey(sshKey string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	decoded, err := base64.StdEncoding.DecodeString(sshKey)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath, sshKey, "the SSH public key is not properly base64 encoded"))
		return allErrs
	}

	if _, _, _, _, err := ssh.ParseAuthorizedKey(decoded); err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath, sshKey, "the SSH public key is not valid"))
		return allErrs
	}

	return allErrs
}

// ValidateSystemAssignedIdentity validates the system-assigned identities list.
func ValidateSystemAssignedIdentity(identityType VMIdentity, oldIdentity, newIdentity string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if identityType == VMIdentitySystemAssigned {
		if _, err := uuid.Parse(newIdentity); err != nil {
			allErrs = append(allErrs, field.Invalid(fldPath, newIdentity, "Role assignment name must be a valid GUID. It is optional and will be auto-generated when not specified."))
		}
		if oldIdentity != "" && oldIdentity != newIdentity {
			allErrs = append(allErrs, field.Invalid(fldPath, newIdentity, "Role assignment name should not be modified after AzureMachine creation."))
		}
	} else if newIdentity != "" {
		allErrs = append(allErrs, field.Forbidden(fldPath, "Role assignment name should only be set when using system assigned identity."))
	}

	return allErrs
}

// ValidateUserAssignedIdentity validates the user-assigned identities list.
func ValidateUserAssignedIdentity(identityType VMIdentity, userAssignedIdenteties []UserAssignedIdentity, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if identityType == VMIdentityUserAssigned && len(userAssignedIdenteties) == 0 {
		allErrs = append(allErrs, field.Required(fldPath, "must be specified for the 'UserAssigned' identity type"))
	}
	return allErrs
}

// ValidateDataDisks validates a list of data disks.
func ValidateDataDisks(dataDisks []DataDisk, fieldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	lunSet := make(map[int32]struct{})
	nameSet := make(map[string]struct{})
	for _, disk := range dataDisks {
		// validate that the disk size is between 4 and 32767.
		if disk.DiskSizeGB < 4 || disk.DiskSizeGB > 32767 {
			allErrs = append(allErrs, field.Invalid(fieldPath.Child("DiskSizeGB"), "", "the disk size should be a value between 4 and 32767"))
		}

		// validate that all names are unique
		if disk.NameSuffix == "" {
			allErrs = append(allErrs, field.Required(fieldPath.Child("NameSuffix"), "the name suffix cannot be empty"))
		}
		if _, ok := nameSet[disk.NameSuffix]; ok {
			allErrs = append(allErrs, field.Duplicate(fieldPath, disk.NameSuffix))
		} else {
			nameSet[disk.NameSuffix] = struct{}{}
		}

		// validate optional managed disk option
		if disk.ManagedDisk != nil {
			if errs := validateManagedDisk(disk.ManagedDisk, fieldPath.Child("managedDisk"), false); len(errs) > 0 {
				allErrs = append(allErrs, errs...)
			}
		}

		// validate that all LUNs are unique and between 0 and 63.
		if disk.Lun == nil {
			allErrs = append(allErrs, field.Required(fieldPath, "LUN should not be nil"))
		} else if *disk.Lun < 0 || *disk.Lun > 63 {
			allErrs = append(allErrs, field.Invalid(fieldPath, disk.Lun, "logical unit number must be between 0 and 63"))
		} else if _, ok := lunSet[*disk.Lun]; ok {
			allErrs = append(allErrs, field.Duplicate(fieldPath, disk.Lun))
		} else {
			lunSet[*disk.Lun] = struct{}{}
		}

		// validate cachingType
		allErrs = append(allErrs, validateCachingType(disk.CachingType, fieldPath, disk.ManagedDisk)...)
	}
	return allErrs
}

// ValidateOSDisk validates the OSDisk spec.
func ValidateOSDisk(osDisk OSDisk, fieldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if osDisk.DiskSizeGB != nil {
		if *osDisk.DiskSizeGB <= 0 || *osDisk.DiskSizeGB > 2048 {
			allErrs = append(allErrs, field.Invalid(fieldPath.Child("DiskSizeGB"), "", "the Disk size should be a value between 1 and 2048"))
		}
	}

	if osDisk.OSType == "" {
		allErrs = append(allErrs, field.Required(fieldPath.Child("OSType"), "the OS type cannot be empty"))
	}

	allErrs = append(allErrs, validateCachingType(osDisk.CachingType, fieldPath, osDisk.ManagedDisk)...)

	if osDisk.ManagedDisk != nil {
		if errs := validateManagedDisk(osDisk.ManagedDisk, fieldPath.Child("managedDisk"), true); len(errs) > 0 {
			allErrs = append(allErrs, errs...)
		}
	}

	if osDisk.DiffDiskSettings != nil && osDisk.ManagedDisk != nil && osDisk.ManagedDisk.DiskEncryptionSet != nil {
		allErrs = append(allErrs, field.Invalid(
			fieldPath.Child("managedDisks").Child("diskEncryptionSet"),
			osDisk.ManagedDisk.DiskEncryptionSet.ID,
			"diskEncryptionSet is not supported when diffDiskSettings.option is 'Local'",
		))
	}

	return allErrs
}

// validateManagedDisk validates updates to the ManagedDiskParameters field.
func validateManagedDisk(m *ManagedDiskParameters, fieldPath *field.Path, isOSDisk bool) field.ErrorList {
	allErrs := field.ErrorList{}

	if m != nil {
		allErrs = append(allErrs, validateStorageAccountType(m.StorageAccountType, fieldPath.Child("StorageAccountType"), isOSDisk)...)
	}

	return allErrs
}

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

func validateStorageAccountType(storageAccountType string, fieldPath *field.Path, isOSDisk bool) field.ErrorList {
	allErrs := field.ErrorList{}

	if isOSDisk && storageAccountType == string(compute.StorageAccountTypesUltraSSDLRS) {
		allErrs = append(allErrs, field.Invalid(fieldPath.Child("managedDisks").Child("storageAccountType"), storageAccountType, "UltraSSD_LRS can only be used with data disks, it cannot be used with OS Disks"))
	}

	if storageAccountType == "" {
		allErrs = append(allErrs, field.Required(fieldPath, "the Storage Account Type for Managed Disk cannot be empty"))
		return allErrs
	}

	for _, possibleStorageAccountType := range compute.PossibleDiskStorageAccountTypesValues() {
		if string(possibleStorageAccountType) == storageAccountType {
			return allErrs
		}
	}
	allErrs = append(allErrs, field.Invalid(fieldPath, "", fmt.Sprintf("allowed values are %v", compute.PossibleDiskStorageAccountTypesValues())))
	return allErrs
}

func validateCachingType(cachingType string, fieldPath *field.Path, managedDisk *ManagedDiskParameters) field.ErrorList {
	allErrs := field.ErrorList{}
	cachingTypeChildPath := fieldPath.Child("CachingType")

	if managedDisk != nil && managedDisk.StorageAccountType == string(compute.StorageAccountTypesUltraSSDLRS) {
		if cachingType != string(compute.CachingTypesNone) {
			allErrs = append(allErrs, field.Invalid(cachingTypeChildPath, cachingType, fmt.Sprintf("cachingType '%s' is not supported when storageAccountType is '%s'. Allowed values are: '%s'", cachingType, compute.StorageAccountTypesUltraSSDLRS, compute.CachingTypesNone)))
		}
	}

	for _, possibleCachingType := range compute.PossibleCachingTypesValues() {
		if string(possibleCachingType) == cachingType {
			return allErrs
		}
	}

	allErrs = append(allErrs, field.Invalid(cachingTypeChildPath, cachingType, fmt.Sprintf("allowed values are %v", compute.PossibleCachingTypesValues())))
	return allErrs
}
