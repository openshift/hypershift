//go:build e2ev2

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

package util

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestEventuallyObjectRetriesGetterErrors(t *testing.T) {
	object := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "example"}}
	attempts := 0

	err := EventuallyObject(t.Context(), "ConfigMap to become available", func(context.Context) (*corev1.ConfigMap, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("object is not available yet")
		}
		return object, nil
	}, []e2eutil.Predicate[*corev1.ConfigMap]{func(obj *corev1.ConfigMap) (bool, string, error) {
		return true, "ConfigMap is available", nil
	}}, WithInterval(time.Millisecond), WithTimeout(time.Second))
	if err != nil {
		t.Fatalf("EventuallyObject returned an unexpected error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("EventuallyObject attempts = %d, want 2", attempts)
	}
}

func TestEventuallyObjectReturnsPredicateError(t *testing.T) {
	wantErr := errors.New("predicate failed")
	err := EventuallyObject(t.Context(), "ConfigMap predicate", func(context.Context) (*corev1.ConfigMap, error) {
		return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "example"}}, nil
	}, []e2eutil.Predicate[*corev1.ConfigMap]{func(obj *corev1.ConfigMap) (bool, string, error) {
		return false, "", wantErr
	}}, WithoutConditionDump(), WithInterval(time.Millisecond), WithTimeout(time.Second))
	if !errors.Is(err, wantErr) {
		t.Fatalf("EventuallyObject error = %v, want it to wrap %v", err, wantErr)
	}
}

func TestEventuallyObjectReturnsTimeoutError(t *testing.T) {
	err := EventuallyObject(t.Context(), "ConfigMap to become ready", func(context.Context) (*corev1.ConfigMap, error) {
		return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "example"}}, nil
	}, []e2eutil.Predicate[*corev1.ConfigMap]{func(obj *corev1.ConfigMap) (bool, string, error) {
		return false, "ConfigMap is not ready", nil
	}}, WithoutConditionDump(), WithInterval(time.Millisecond), WithTimeout(10*time.Millisecond))
	if err == nil {
		t.Fatal("EventuallyObject returned nil, want timeout error")
	}
	if !strings.Contains(err.Error(), "failed to wait for ConfigMap to become ready") {
		t.Fatalf("EventuallyObject error = %q, want objective in error", err)
	}
}

func TestEventuallyObjectsEvaluatesGroupAndObjectPredicates(t *testing.T) {
	objects := []*corev1.ConfigMap{{ObjectMeta: metav1.ObjectMeta{Name: "one"}}, {ObjectMeta: metav1.ObjectMeta{Name: "two"}}}
	attempts := 0

	err := EventuallyObjects(t.Context(), "ConfigMaps to converge", func(context.Context) ([]*corev1.ConfigMap, error) {
		attempts++
		if attempts == 1 {
			return objects[:1], nil
		}
		return objects, nil
	}, []e2eutil.Predicate[[]*corev1.ConfigMap]{func(items []*corev1.ConfigMap) (bool, string, error) {
		return len(items) == 2, "expected two ConfigMaps", nil
	}}, []e2eutil.Predicate[*corev1.ConfigMap]{func(obj *corev1.ConfigMap) (bool, string, error) {
		return obj.Name != "", "ConfigMap has a name", nil
	}}, WithInterval(time.Millisecond), WithTimeout(time.Second))
	if err != nil {
		t.Fatalf("EventuallyObjects returned an unexpected error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("EventuallyObjects attempts = %d, want 2", attempts)
	}
}

func TestEventuallyNotFoundReturnsSuccessForMissingObject(t *testing.T) {
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "example"}}
	client := eventuallyNotFoundClient{getErr: apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, obj.Name)}

	err := EventuallyNotFound(t.Context(), client, obj, WithInterval(time.Millisecond), WithTimeout(time.Second))
	if err != nil {
		t.Fatalf("EventuallyNotFound returned an unexpected error: %v", err)
	}
}

func TestEventuallyNotFoundReturnsUnexpectedClientError(t *testing.T) {
	wantErr := errors.New("client unavailable")
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "example"}}
	client := eventuallyNotFoundClient{getErr: wantErr}

	err := EventuallyNotFound(t.Context(), client, obj, WithInterval(time.Millisecond), WithTimeout(time.Second))
	if !errors.Is(err, wantErr) {
		t.Fatalf("EventuallyNotFound error = %v, want it to wrap %v", err, wantErr)
	}
}

type eventuallyNotFoundClient struct {
	crclient.Client
	getErr error
}

func (c eventuallyNotFoundClient) Get(context.Context, crclient.ObjectKey, crclient.Object, ...crclient.GetOption) error {
	return c.getErr
}
