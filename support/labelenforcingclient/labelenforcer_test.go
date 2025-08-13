package labelenforcingclient

import (
	"context"
	"reflect"
	"testing"

	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/google/go-cmp/cmp"
)

func TestLabelEnforcingClientEnforcesLabel(t *testing.T) {
	testCases := []struct {
		name string
		in   crclient.Object
	}{
		{
			name: "Label unset, gets added",
			in:   &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "foo"}},
		},
		{
			name: "Label set with wrong value gets overridden",
			in: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name:   "foo",
				Labels: map[string]string{CacheLabelSelectorKey: "invalid"},
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			client := &LabelEnforcingClient{
				Client: fake.NewClientBuilder().Build(),
				labels: map[string]string{CacheLabelSelectorKey: CacheLabelSelectorValue},
			}

			if err := client.Create(ctx, tc.in.DeepCopyObject().(crclient.Object)); err != nil {
				t.Fatalf("Create failed: %v", err)
			}

			retrieved := tc.in.DeepCopyObject().(crclient.Object)
			if err := client.Get(ctx, crclient.ObjectKeyFromObject(retrieved), retrieved); err != nil {
				t.Fatalf("failed to get object after creating it: %v", err)
			}
			if val := retrieved.GetLabels()[CacheLabelSelectorKey]; val != CacheLabelSelectorValue {
				t.Errorf("expected label %s to have value %s, found %q", CacheLabelSelectorKey, CacheLabelSelectorValue, val)
			}

			if err := client.Update(ctx, tc.in.DeepCopyObject().(crclient.Object)); err != nil {
				t.Fatalf("Update failed: %v", err)
			}

			retrieved = tc.in.DeepCopyObject().(crclient.Object)
			if err := client.Get(ctx, crclient.ObjectKeyFromObject(retrieved), retrieved); err != nil {
				t.Fatalf("failed to get object after creating it: %v", err)
			}
			if val := retrieved.GetLabels()[CacheLabelSelectorKey]; val != CacheLabelSelectorValue {
				t.Errorf("expected label %s to have value %s, found %q", CacheLabelSelectorKey, CacheLabelSelectorValue, val)
			}
		})
	}
}

// labelselectingReadClient fakes the behavior of a cache with a labelselector
// by never returning objects from Get that do not match the configured selector.
type labelselectingReadClient struct {
	crclient.Client
	selector labels.Selector
}

func (l *labelselectingReadClient) Get(ctx context.Context, key crclient.ObjectKey, obj crclient.Object, opt ...crclient.GetOption) error {
	initialObj := obj.DeepCopyObject()
	if err := l.Client.Get(ctx, key, obj); err != nil {
		return err
	}

	if !l.selector.Matches(labels.Set(obj.GetLabels())) {
		reflect.Indirect(reflect.ValueOf(obj)).Set(reflect.Indirect(reflect.ValueOf(initialObj)))
		return apierrors.NewNotFound(schema.GroupResource{}, obj.GetName())
	}

	return nil
}

func TestLabelEnforcingUpsertProvider(t *testing.T) {
	obj := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "foo"},
	}

	client := fake.NewClientBuilder().WithObjects(obj).Build()
	clientWithSelector := labelselectingReadClient{
		Client:   client,
		selector: labels.SelectorFromSet(labels.Set{CacheLabelSelectorKey: CacheLabelSelectorValue}),
	}

	// Adding it to the fakeclient sets the ResourceVersion which will
	// make a pre-flight check in the fakeclient fail which is not what
	// we want to test, so clear it.
	obj.ResourceVersion = ""

	provider := &LabelEnforcingUpsertProvider{
		Upstream:  upsert.New(false),
		APIClient: client,
	}

	ctx := t.Context()
	result, err := provider.CreateOrUpdate(ctx, &clientWithSelector, obj, func() error {
		obj.Data = map[string][]byte{"some-key": []byte("some-value")}
		return nil
	})
	if err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	if result != controllerutil.OperationResultUpdated {
		t.Fatalf("expected result to be %s, was %s", controllerutil.OperationResultUpdated, result)
	}

	if err := client.Get(ctx, crclient.ObjectKeyFromObject(obj), obj); err != nil {
		t.Fatalf("failed to retrieve object: %v", err)
	}

	expectedData := map[string][]byte{"some-key": []byte("some-value")}
	if diff := cmp.Diff(obj.Data, expectedData); diff != "" {
		t.Errorf("actual data differs from expected: %s", diff)
	}
}
