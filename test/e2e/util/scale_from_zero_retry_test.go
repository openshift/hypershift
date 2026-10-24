package util

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/onsi/gomega"
)

func TestUpdateScaleFromZeroUpgrade(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: hyperv1.GroupVersion.Group, Resource: "nodepools"}, "workers", errors.New("forbidden"))
	for _, test := range []struct {
		name             string
		conflicts        int
		cancelBefore     bool
		cancelOnConflict bool
		invalidReplicas  bool
		getError         error
		updateError      error
		wantError        error
		wantConflict     bool
		wantErrorText    string
		wantGets         int
		wantUpdates      int
	}{
		{name: "When no concurrent writer intervenes, it should return the persisted autoscaling baseline", wantGets: 1, wantUpdates: 1},
		{name: "When one concurrent update makes the resourceVersion stale, it should refetch and preserve the latest metadata and replicas", conflicts: 1, wantGets: 2, wantUpdates: 2},
		{name: "When multiple concurrent updates conflict, it should prepare from each new resourceVersion until success", conflicts: 2, wantGets: 3, wantUpdates: 3},
		{name: "When every attempt conflicts, it should return the last conflict without a prepared result", conflicts: 3, wantGets: 3, wantUpdates: 3, wantConflict: true},
		{name: "When the context is already canceled, it should avoid client calls", cancelBefore: true, wantError: context.Canceled},
		{name: "When the context is canceled after a conflict, it should stop before another client call", conflicts: 1, cancelOnConflict: true, wantGets: 1, wantUpdates: 1, wantError: context.Canceled},
		{name: "When fetching fails permanently, it should fail without retrying or updating", getError: forbidden, wantGets: 1, wantError: forbidden},
		{name: "When updating fails permanently, it should fail without retrying", updateError: forbidden, wantGets: 1, wantUpdates: 1, wantError: forbidden},
		{name: "When fresh replicas cannot be prepared, it should fail without retrying or updating", invalidReplicas: true, wantGets: 1, wantErrorText: "fixed positive replica count"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertions := gomega.NewWithT(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.cancelBefore {
				cancel()
			}
			initial := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "clusters", Annotations: map[string]string{"unrelated": "preserved"}},
				Spec:       hyperv1.NodePoolSpec{Replicas: ptr.To[int32](2), Arch: hyperv1.ArchitectureARM64},
			}
			if test.invalidReplicas {
				initial.Spec.Replicas = nil
			}
			storedClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(initial).Build()
			key := client.ObjectKeyFromObject(initial)
			getCalls, updateCalls := 0, 0
			versions := map[string]bool{}
			conflictClient := interceptor.NewClient(storedClient, interceptor.Funcs{
				Get: func(ctx context.Context, underlying client.WithWatch, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
					getCalls++
					if test.getError != nil {
						return test.getError
					}
					return underlying.Get(ctx, key, object, options...)
				},
				Update: func(ctx context.Context, underlying client.WithWatch, object client.Object, options ...client.UpdateOption) error {
					updateCalls++
					if versions[object.GetResourceVersion()] {
						t.Fatal("update reused a stale resourceVersion instead of fetching again")
					}
					versions[object.GetResourceVersion()] = true
					if updateCalls <= test.conflicts {
						latest := &hyperv1.NodePool{}
						if err := underlying.Get(ctx, key, latest); err != nil {
							return err
						}
						latest.Annotations["concurrent-writer"] = fmt.Sprint(updateCalls)
						latest.Spec.Replicas = ptr.To[int32](3)
						if err := underlying.Update(ctx, latest); err != nil {
							return err
						}
						err := underlying.Update(ctx, object, options...)
						assertions.Expect(apierrors.IsConflict(err)).To(gomega.BeTrue(), "stale resourceVersion must produce an actual conflict")
						if test.cancelOnConflict {
							cancel()
						}
						return err
					}
					if test.updateError != nil {
						return test.updateError
					}
					return underlying.Update(ctx, object, options...)
				},
			})
			prepared, err := UpdateScaleFromZeroUpgrade(ctx, conflictClient, key, wait.Backoff{Steps: 3})
			switch {
			case test.wantError != nil:
				assertions.Expect(errors.Is(err, test.wantError)).To(gomega.BeTrue())
			case test.wantConflict:
				assertions.Expect(apierrors.IsConflict(err)).To(gomega.BeTrue())
			case test.wantErrorText != "":
				assertions.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(test.wantErrorText)))
			default:
				assertions.Expect(err).NotTo(gomega.HaveOccurred(), "expected successful conflict recovery")
				assertions.Expect(prepared).NotTo(gomega.BeNil())
				persisted := &hyperv1.NodePool{}
				assertions.Expect(storedClient.Get(t.Context(), key, persisted)).To(gomega.Succeed())
				assertions.Expect(reflect.DeepEqual(prepared, persisted)).To(gomega.BeTrue(), "returned NodePool must equal the successfully persisted object")
				minimum := int32(2)
				if test.conflicts > 0 {
					minimum = 3
					assertions.Expect(prepared.Annotations["concurrent-writer"]).To(gomega.Equal(fmt.Sprint(test.conflicts)))
				}
				assertions.Expect(prepared.Annotations["unrelated"]).To(gomega.Equal("preserved"))
				assertions.Expect(prepared.Spec.Arch).To(gomega.Equal(hyperv1.ArchitectureARM64))
				assertions.Expect(prepared.Spec.Replicas).To(gomega.BeNil())
				assertions.Expect(prepared.Spec.AutoScaling).NotTo(gomega.BeNil())
				assertions.Expect(prepared.Spec.AutoScaling.Min).To(gomega.Equal(&minimum))
				assertions.Expect(prepared.Spec.AutoScaling.Max).To(gomega.Equal(minimum + 1))
			}
			if err != nil {
				assertions.Expect(prepared).To(gomega.BeNil(), "a failed update must not return an unpersisted preparation")
				persisted := &hyperv1.NodePool{}
				assertions.Expect(storedClient.Get(t.Context(), key, persisted)).To(gomega.Succeed())
				assertions.Expect(persisted.Spec.AutoScaling).To(gomega.BeNil(), "failed attempts must not persist autoscaling preparation")
				assertions.Expect(persisted.Annotations["unrelated"]).To(gomega.Equal("preserved"))
			}
			assertions.Expect(getCalls).To(gomega.Equal(test.wantGets))
			assertions.Expect(updateCalls).To(gomega.Equal(test.wantUpdates))
		})
	}
}
