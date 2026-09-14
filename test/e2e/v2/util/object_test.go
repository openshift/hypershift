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
	"fmt"
	"strings"
	"testing"
	"time"

	hyperapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestUpdateObject(t *testing.T) {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "example"},
		Data:       map[string]string{"state": "before"},
	}
	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(configMap).Build()

	err := UpdateObject(t.Context(), client, configMap, func(obj *corev1.ConfigMap) {
		obj.Data["state"] = "after"
	})
	if err != nil {
		t.Fatalf("UpdateObject returned an unexpected error: %v", err)
	}

	updated := &corev1.ConfigMap{}
	if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(configMap), updated); err != nil {
		t.Fatalf("failed to get updated ConfigMap: %v", err)
	}
	if updated.Data["state"] != "after" {
		t.Fatalf("ConfigMap state = %q, want %q", updated.Data["state"], "after")
	}
}

func TestUpdateObjectRetriesConflict(t *testing.T) {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "example"},
		Data:       map[string]string{"state": "before"},
	}
	baseClient := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(configMap).Build()
	client := &conflictOnceClient{Client: baseClient}

	err := UpdateObject(t.Context(), client, configMap, func(obj *corev1.ConfigMap) {
		obj.Data["state"] = "after"
	})
	if err != nil {
		t.Fatalf("UpdateObject returned an unexpected error: %v", err)
	}
	if client.patchAttempts != 2 {
		t.Fatalf("UpdateObject patch attempts = %d, want 2", client.patchAttempts)
	}
}

func TestUpdateObjectReturnsLastRetryError(t *testing.T) {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "example"},
	}
	getErr := errors.New("simulated get failure")
	baseClient := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(configMap).Build()
	client := &getErrorClient{Client: baseClient, err: getErr}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()

	err := UpdateObject(ctx, client, configMap, func(obj *corev1.ConfigMap) {
		obj.Data = map[string]string{"state": "after"}
	})
	if err == nil {
		t.Fatal("UpdateObject returned nil error")
	}
	if !errors.Is(err, getErr) {
		t.Fatalf("UpdateObject error = %v, want underlying Get error", err)
	}
	if !strings.Contains(err.Error(), "polling error") {
		t.Fatalf("UpdateObject error = %q, want polling context", err)
	}
}

type conflictOnceClient struct {
	crclient.Client
	patchAttempts int
}

func (c *conflictOnceClient) Patch(ctx context.Context, obj crclient.Object, patch crclient.Patch, opts ...crclient.PatchOption) error {
	c.patchAttempts++
	if c.patchAttempts == 1 {
		return apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, obj.GetName(), fmt.Errorf("simulated conflict"))
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

type getErrorClient struct {
	crclient.Client
	err error
}

func (c *getErrorClient) Get(context.Context, crclient.ObjectKey, crclient.Object, ...crclient.GetOption) error {
	return c.err
}
