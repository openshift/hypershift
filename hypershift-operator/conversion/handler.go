// Copied (and modified locally) from controller-runtime/pkg/webhook/conversion/conversion.go

/*
Copyright 2019 The Kubernetes Authors.

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

/*
Package conversion provides implementation for CRD conversion webhook that implements handler for version conversion requests for types that are convertible.

See pkg/conversion for interface definitions required to ensure an API Type is convertible.
*/
package conversion

import (
	"encoding/json"
	"fmt"
	"net/http"

	hyperv1alpha1 "github.com/openshift/hypershift/api/hypershift/v1alpha1"
	hyperv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	apix "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	crconversion "sigs.k8s.io/controller-runtime/pkg/webhook/conversion"
)

var (
	log = logf.Log.WithName("conversion-webhook")
)

func NewWebhookHandler(scheme *runtime.Scheme) http.Handler {
	return &webhook{scheme: scheme, decoder: crconversion.NewDecoder(scheme)}
}

// webhook implements a CRD conversion webhook HTTP handler.
type webhook struct {
	scheme  *runtime.Scheme
	decoder *crconversion.Decoder
}

// ensure Webhook implements http.Handler
var _ http.Handler = &webhook{}

func (wh *webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	convertReview := &apix.ConversionReview{}
	err := json.NewDecoder(r.Body).Decode(convertReview)
	if err != nil {
		log.Error(err, "failed to read conversion request")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if convertReview.Request == nil {
		log.Error(nil, "conversion request is nil")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// TODO(droot): may be move the conversion logic to a separate module to
	// decouple it from the http layer ?
	resp, err := wh.handleConvertRequest(convertReview.Request)
	if err != nil {
		log.Error(err, "failed to convert", "request", convertReview.Request.UID)
		convertReview.Response = errored(err)
	} else {
		convertReview.Response = resp
	}
	convertReview.Response.UID = convertReview.Request.UID
	convertReview.Request = nil

	err = json.NewEncoder(w).Encode(convertReview)
	if err != nil {
		log.Error(err, "failed to write response")
		return
	}
}

// handles a version conversion request.
func (wh *webhook) handleConvertRequest(req *apix.ConversionRequest) (*apix.ConversionResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("conversion request is nil")
	}
	var objects []runtime.RawExtension

	for _, obj := range req.Objects {
		src, gvk, err := wh.decoder.Decode(obj.Raw)
		if err != nil {
			return nil, err
		}
		dst, err := wh.allocateDstObject(req.DesiredAPIVersion, gvk.Kind)
		if err != nil {
			return nil, err
		}
		err = wh.convertObject(src, dst)
		if err != nil {
			return nil, err
		}
		objects = append(objects, runtime.RawExtension{Object: dst})
	}
	return &apix.ConversionResponse{
		UID:              req.UID,
		ConvertedObjects: objects,
		Result: metav1.Status{
			Status: metav1.StatusSuccess,
		},
	}, nil
}

// convertObject will convert given a src object to dst object.
// Note(droot): couldn't find a way to reduce the cyclomatic complexity under 10
// without compromising readability, so disabling gocyclo linter
func (wh *webhook) convertObject(src, dst runtime.Object) error {
	srcGVK := src.GetObjectKind().GroupVersionKind()
	dstGVK := dst.GetObjectKind().GroupVersionKind()

	if srcGVK.GroupKind() != dstGVK.GroupKind() {
		return fmt.Errorf("src %T and dst %T does not belong to same API Group", src, dst)
	}

	if srcGVK == dstGVK {
		return fmt.Errorf("conversion is not allowed between same type %T", src)
	}

	srcIsHub, dstIsHub := isHub(src), isHub(dst)
	srcIsConvertible, dstIsConvertible := isConvertible(src), isConvertible(dst)

	switch {
	case srcIsHub && dstIsConvertible:
		return ConvertFrom(src, dst)
	case dstIsHub && srcIsConvertible:
		return ConvertTo(src, dst)
	default:
		return fmt.Errorf("%T is not convertible to %T", src, dst)
	}
}

// allocateDstObject returns an instance for a given GVK.
func (wh *webhook) allocateDstObject(apiVersion, kind string) (runtime.Object, error) {
	gvk := schema.FromAPIVersionAndKind(apiVersion, kind)

	obj, err := wh.scheme.New(gvk)
	if err != nil {
		return obj, err
	}

	t, err := meta.TypeAccessor(obj)
	if err != nil {
		return obj, err
	}

	t.SetAPIVersion(apiVersion)
	t.SetKind(kind)

	return obj, nil
}

// isHub determines if passed-in object is a Hub or not.
func isHub(obj runtime.Object) bool {
	switch obj.(type) {
	case *hyperv1beta1.HostedCluster, *hyperv1beta1.NodePool, *hyperv1beta1.AWSEndpointService, *hyperv1beta1.HostedControlPlane:
		return true
	}
	return false
}

// isConvertible determines if passed-in object is a convertible.
func isConvertible(obj runtime.Object) bool {
	switch obj.(type) {
	case *hyperv1alpha1.HostedCluster, *hyperv1alpha1.NodePool, *hyperv1alpha1.AWSEndpointService, *hyperv1alpha1.HostedControlPlane:
		return true
	}
	return false
}

// helper to construct error response.
func errored(err error) *apix.ConversionResponse {
	return &apix.ConversionResponse{
		Result: metav1.Status{
			Status:  metav1.StatusFailure,
			Message: err.Error(),
		},
	}
}
