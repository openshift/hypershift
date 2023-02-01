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

package webhook

import (
	"context"
	"errors"
	"net/http"
	"reflect"

	admissionv1 "k8s.io/api/admission/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	unsetMessage     = "field is immutable, unable to set an empty value if it was already set"
	setMessage       = "field is immutable, unable to assign a value if it was already empty"
	immutableMessage = "field is immutable"
)

// Validator defines functions for validating an operation.
type Validator interface {
	runtime.Object
	ValidateCreate(client client.Client) error
	ValidateUpdate(old runtime.Object, client client.Client) error
	ValidateDelete(client client.Client) error
}

// NewValidatingWebhook creates a new Webhook for validating the provided type.
func NewValidatingWebhook(validator Validator, client client.Client) *admission.Webhook {
	return &admission.Webhook{
		Handler: &validatingHandler{
			validator: validator,
			Client:    client,
		},
	}
}

type validatingHandler struct {
	validator Validator
	Client    client.Client
	decoder   *admission.Decoder
}

var _ admission.DecoderInjector = &validatingHandler{}

// InjectDecoder injects the decoder into a validatingHandler.
func (h *validatingHandler) InjectDecoder(d *admission.Decoder) error {
	h.decoder = d
	return nil
}

// Handle handles admission requests.
func (h *validatingHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	if h.validator == nil {
		panic("validator should never be nil")
	}

	// Get the object in the request
	obj := h.validator.DeepCopyObject().(Validator)
	if req.Operation == admissionv1.Create {
		err := h.decoder.Decode(req, obj)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}

		err = obj.ValidateCreate(h.Client)
		if err != nil {
			var apiStatus apierrors.APIStatus
			if errors.As(err, &apiStatus) {
				return validationResponseFromStatus(false, apiStatus.Status())
			}
			return admission.Denied(err.Error())
		}
	}

	if req.Operation == admissionv1.Update {
		oldObj := obj.DeepCopyObject()

		err := h.decoder.DecodeRaw(req.Object, obj)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		err = h.decoder.DecodeRaw(req.OldObject, oldObj)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}

		err = obj.ValidateUpdate(oldObj, h.Client)
		if err != nil {
			var apiStatus apierrors.APIStatus
			if errors.As(err, &apiStatus) {
				return validationResponseFromStatus(false, apiStatus.Status())
			}
			return admission.Denied(err.Error())
		}
	}

	if req.Operation == admissionv1.Delete {
		// In reference to PR: https://github.com/kubernetes/kubernetes/pull/76346
		// OldObject contains the object being deleted
		err := h.decoder.DecodeRaw(req.OldObject, obj)
		if err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}

		err = obj.ValidateDelete(h.Client)
		if err != nil {
			var apiStatus apierrors.APIStatus
			if errors.As(err, &apiStatus) {
				return validationResponseFromStatus(false, apiStatus.Status())
			}
			return admission.Denied(err.Error())
		}
	}

	return admission.Allowed("")
}

func validationResponseFromStatus(allowed bool, status metav1.Status) admission.Response {
	return admission.Response{
		AdmissionResponse: admissionv1.AdmissionResponse{
			Allowed: allowed,
			Result:  &status,
		},
	}
}

// ValidateImmutable validates equality across two values,
// and returns a meaningful error to indicate a changed value, a newly set value, or a newly unset value.
func ValidateImmutable(path *field.Path, oldVal, newVal any) *field.Error {
	if reflect.TypeOf(oldVal) != reflect.TypeOf(newVal) {
		return field.Invalid(path, newVal, "unexpected error")
	}
	if !reflect.ValueOf(oldVal).IsZero() {
		// Prevent modification if it was already set to some value
		if reflect.ValueOf(newVal).IsZero() {
			// unsetting the field is not allowed
			return field.Invalid(path, newVal, unsetMessage)
		}
		if !reflect.DeepEqual(oldVal, newVal) {
			// changing the field is not allowed
			return field.Invalid(path, newVal, immutableMessage)
		}
	} else if !reflect.ValueOf(newVal).IsZero() {
		return field.Invalid(path, newVal, setMessage)
	}

	return nil
}
