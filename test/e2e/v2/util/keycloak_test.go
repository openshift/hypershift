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
	"io"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestCreateOrUpdate(t *testing.T) {
	t.Run("When a create response is lost before commit, it should retry the fixed-name create", func(t *testing.T) {
		client := &createOrUpdateTestClient{
			createErrors: []error{io.EOF, nil},
			getErrors:    []error{apierrors.NewNotFound(schema.GroupResource{Resource: "testobjects"}, "test")},
		}

		if err := CreateOrUpdate(t.Context(), client, &testObject{}); err != nil {
			t.Fatalf("expected CreateOrUpdate to recover, got: %v", err)
		}
		if client.createCalls != 2 {
			t.Fatalf("expected two create calls, got %d", client.createCalls)
		}
		if client.getCalls != 1 {
			t.Fatalf("expected one get call, got %d", client.getCalls)
		}
	})

	t.Run("When an update response is lost, it should recompute a merge patch before retrying", func(t *testing.T) {
		client := &createOrUpdateTestClient{
			resourceVersions: []string{"41", "42", "43"},
			patchErrors:      []error{io.EOF, io.EOF, nil},
		}
		obj := &testObject{}

		if err := CreateOrUpdate(t.Context(), client, obj); err != nil {
			t.Fatalf("expected CreateOrUpdate to recover, got: %v", err)
		}
		if client.createCalls != 1 {
			t.Fatalf("expected one create call, got %d", client.createCalls)
		}
		if client.getCalls != 3 {
			t.Fatalf("expected three get calls, got %d", client.getCalls)
		}
		if client.patchCalls != 3 {
			t.Fatalf("expected three patch calls, got %d", client.patchCalls)
		}
		for _, body := range client.patchBodies {
			if strings.Contains(body, "resourceVersion") {
				t.Fatalf("expected merge patch %q not to contain resourceVersion", body)
			}
		}
		if obj.GetResourceVersion() != "43" {
			t.Fatalf("expected final resourceVersion 43, got %q", obj.GetResourceVersion())
		}
	})
}

type testObject struct {
	metav1.TypeMeta
	metav1.ObjectMeta
}

func (o *testObject) DeepCopyObject() runtime.Object {
	copy := *o
	return &copy
}

type createOrUpdateTestClient struct {
	crclient.Client
	resourceVersions []string
	createErrors     []error
	getErrors        []error
	patchErrors      []error
	patchBodies      []string
	createCalls      int
	getCalls         int
	patchCalls       int
}

func (c *createOrUpdateTestClient) Create(context.Context, crclient.Object, ...crclient.CreateOption) error {
	call := c.createCalls
	c.createCalls++
	if call < len(c.createErrors) {
		return c.createErrors[call]
	}
	return apierrors.NewAlreadyExists(schema.GroupResource{Resource: "testobjects"}, "test")
}

func (c *createOrUpdateTestClient) Get(_ context.Context, _ crclient.ObjectKey, obj crclient.Object, _ ...crclient.GetOption) error {
	call := c.getCalls
	c.getCalls++
	if call < len(c.getErrors) && c.getErrors[call] != nil {
		return c.getErrors[call]
	}
	if c.getCalls <= len(c.resourceVersions) {
		obj.SetResourceVersion(c.resourceVersions[c.getCalls-1])
	}
	return nil
}

func (c *createOrUpdateTestClient) Patch(_ context.Context, obj crclient.Object, patch crclient.Patch, _ ...crclient.PatchOption) error {
	data, err := patch.Data(obj)
	if err != nil {
		return err
	}
	c.patchBodies = append(c.patchBodies, string(data))

	err = nil
	if c.patchCalls < len(c.patchErrors) {
		err = c.patchErrors[c.patchCalls]
	}
	c.patchCalls++
	return err
}
