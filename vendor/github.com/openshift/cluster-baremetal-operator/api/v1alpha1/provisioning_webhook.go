/*

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

package v1alpha1

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var provisioninglog = logf.Log.WithName("provisioning-resource")
var enabledFeatures EnabledFeatures

func (r *Provisioning) SetupWebhookWithManager(mgr ctrl.Manager, features EnabledFeatures) error {
	enabledFeatures = features
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// https://golangbyexample.com/go-check-if-type-implements-interface/
var _ webhook.Validator = &Provisioning{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *Provisioning) ValidateCreate() (admission.Warnings, error) {
	provisioninglog.Info("validate create", "name", r.Name)

	if r.Name != ProvisioningSingletonName {
		return nil, fmt.Errorf("Provisioning object is a singleton and must be named \"%s\"", ProvisioningSingletonName)
	}

	return nil, r.ValidateBaremetalProvisioningConfig(enabledFeatures)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *Provisioning) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	provisioninglog.Info("validate update", "name", r.Name)
	return nil, r.ValidateBaremetalProvisioningConfig(enabledFeatures)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *Provisioning) ValidateDelete() (admission.Warnings, error) {
	provisioninglog.Info("validate delete", "name", r.Name)
	return nil, nil
}
