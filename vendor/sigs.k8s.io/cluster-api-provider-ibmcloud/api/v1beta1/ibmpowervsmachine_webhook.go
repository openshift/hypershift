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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package.
var ibmpowervsmachinelog = logf.Log.WithName("ibmpowervsmachine-resource")

func (r *IBMPowerVSMachine) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

//+kubebuilder:webhook:path=/mutate-infrastructure-cluster-x-k8s-io-v1beta1-ibmpowervsmachine,mutating=true,failurePolicy=fail,groups=infrastructure.cluster.x-k8s.io,resources=ibmpowervsmachines,verbs=create;update,versions=v1beta1,name=mibmpowervsmachine.kb.io,sideEffects=None,admissionReviewVersions=v1;v1beta1

var _ webhook.Defaulter = &IBMPowerVSMachine{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *IBMPowerVSMachine) Default() {
	ibmpowervsmachinelog.Info("default", "name", r.Name)
	defaultIBMPowerVSMachineSpec(&r.Spec)
}

//+kubebuilder:webhook:verbs=create;update,path=/validate-infrastructure-cluster-x-k8s-io-v1beta1-ibmpowervsmachine,mutating=false,failurePolicy=fail,groups=infrastructure.cluster.x-k8s.io,resources=ibmpowervsmachines,versions=v1beta1,name=vibmpowervsmachine.kb.io,sideEffects=None,admissionReviewVersions=v1;v1beta1

var _ webhook.Validator = &IBMPowerVSMachine{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *IBMPowerVSMachine) ValidateCreate() error {
	ibmpowervsmachinelog.Info("validate create", "name", r.Name)
	return r.validateIBMPowerVSMachine()
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *IBMPowerVSMachine) ValidateUpdate(old runtime.Object) error {
	ibmpowervsmachinelog.Info("validate update", "name", r.Name)
	return r.validateIBMPowerVSMachine()
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *IBMPowerVSMachine) ValidateDelete() error {
	ibmpowervsmachinelog.Info("validate delete", "name", r.Name)
	return nil
}

func (r *IBMPowerVSMachine) validateIBMPowerVSMachine() error {
	var allErrs field.ErrorList
	if err := r.validateIBMPowerVSMachineSysType(); err != nil {
		allErrs = append(allErrs, err)
	}
	if err := r.validateIBMPowerVSMachineProcType(); err != nil {
		allErrs = append(allErrs, err)
	}
	if err := r.validateIBMPowerVSMachineNetwork(); err != nil {
		allErrs = append(allErrs, err)
	}
	if err := r.validateIBMPowerVSMachineImage(); err != nil {
		allErrs = append(allErrs, err)
	}
	if len(allErrs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(
		schema.GroupKind{Group: "infrastructure.cluster.x-k8s.io", Kind: "IBMPowerVSMachine"},
		r.Name, allErrs)
}

func (r *IBMPowerVSMachine) validateIBMPowerVSMachineSysType() *field.Error {
	if res, spec := validateIBMPowerVSSysType(r.Spec); !res {
		return field.Invalid(field.NewPath("spec", "sysType"), spec.SysType, "Invalid System Type")
	}
	return nil
}

func (r *IBMPowerVSMachine) validateIBMPowerVSMachineProcType() *field.Error {
	if res, spec := validateIBMPowerVSProcType(r.Spec); !res {
		return field.Invalid(field.NewPath("spec", "procType"), spec.ProcType, "Invalid Processor Type")
	}
	return nil
}

func (r *IBMPowerVSMachine) validateIBMPowerVSMachineNetwork() *field.Error {
	if res, err := validateIBMPowerVSResourceReference(r.Spec.Network, "Network"); !res {
		return err
	}
	return nil
}

func (r *IBMPowerVSMachine) validateIBMPowerVSMachineImage() *field.Error {
	if r.Spec.Image == nil && r.Spec.ImageRef == nil {
		return field.Invalid(field.NewPath(""), "", "One of - Image or ImageRef must be specified")
	}

	if r.Spec.Image != nil && r.Spec.ImageRef != nil {
		return field.Invalid(field.NewPath(""), "", "Only one of - Image or ImageRef maybe be specified")
	}

	if r.Spec.Image != nil {
		if res, err := validateIBMPowerVSResourceReference(*r.Spec.Image, "Image"); !res {
			return err
		}
	}
	return nil
}
