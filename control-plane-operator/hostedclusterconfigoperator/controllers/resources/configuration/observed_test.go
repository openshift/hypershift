package configuration

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestReconcileObserved(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprintf("When observed destination writes fail with source present %t, it should retain both errors", present), func(t *testing.T) {
			assert := NewWithT(t)
			var objects []client.Object
			if present {
				objects = []client.Object{globalconfig.BuildConfig(), globalconfig.ProjectConfig()}
			}
			guest, _ := configurationClients(objects...)
			failure := errors.New("destination failure")
			controlPlane := fake.NewClientBuilder().WithScheme(api.Scheme).WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error { return failure },
			}).Build()
			root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: &errorCreateOrUpdater{err: failure}}
			errs := ReconcileObserved(t.Context(), root.hosted(), root.controlPlane(), ObservedConfigParams{Namespace: configurationHCP().Namespace, OwnerRef: config.OwnerRefFrom(configurationHCP())})
			assert.Expect(errs).To(HaveLen(2))
			for _, err := range errs {
				assert.Expect(errors.Is(err, failure)).To(BeTrue())
				if present {
					assert.Expect(err).To(Equal(failure))
				} else {
					assert.Expect(err).To(MatchError("cannot delete observed config: destination failure"))
				}
			}
		})
	}
	t.Run("When observed configuration is created updated and removed, it should project ownership and delete only control plane copies", func(t *testing.T) {
		assert := NewWithT(t)
		hcp := configurationHCP()
		build, project := globalconfig.BuildConfig(), globalconfig.ProjectConfig()
		guest, controlPlane := configurationClients(build, project)
		root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
		for _, phase := range []string{"created", "updated"} {
			assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(project), project)).To(Succeed())
			project.Spec.ProjectRequestMessage = phase
			assert.Expect(guest.Update(t.Context(), project)).To(Succeed())
			assert.Expect(ReconcileObserved(t.Context(), root.hosted(), root.controlPlane(), ObservedConfigParams{Namespace: hcp.Namespace, OwnerRef: config.OwnerRefFrom(hcp)})).To(BeEmpty())
			for _, pair := range []struct {
				source      client.Object
				destination *corev1.ConfigMap
			}{{build, globalconfig.ObservedBuildConfig(hcp.Namespace)}, {project, globalconfig.ObservedProjectConfig(hcp.Namespace)}} {
				assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(pair.source), pair.source)).To(Succeed())
				assert.Expect(controlPlane.Get(t.Context(), client.ObjectKeyFromObject(pair.destination), pair.destination)).To(Succeed())
				expected := pair.destination.DeepCopy()
				assert.Expect(globalconfig.ReconcileObservedConfig(expected, pair.source)).To(Succeed())
				assert.Expect(pair.destination.Data).To(Equal(expected.Data))
				assert.Expect(pair.destination.OwnerReferences).To(HaveLen(1))
				assert.Expect(pair.destination.OwnerReferences[0].UID).To(Equal(hcp.UID))
				assert.Expect(pair.destination.OwnerReferences[0].Name).To(Equal(hcp.Name))
				assert.Expect(apierrors.IsNotFound(guest.Get(t.Context(), client.ObjectKeyFromObject(pair.destination), &corev1.ConfigMap{}))).To(BeTrue())
			}
		}
		assert.Expect(guest.Delete(t.Context(), build)).To(Succeed())
		assert.Expect(guest.Delete(t.Context(), project)).To(Succeed())
		assert.Expect(ReconcileObserved(t.Context(), root.hosted(), root.controlPlane(), ObservedConfigParams{Namespace: hcp.Namespace, OwnerRef: config.OwnerRefFrom(hcp)})).To(BeEmpty())
		assert.Expect(ReconcileObserved(t.Context(), root.hosted(), root.controlPlane(), ObservedConfigParams{Namespace: hcp.Namespace, OwnerRef: config.OwnerRefFrom(hcp)})).To(BeEmpty())
		for _, destination := range []*corev1.ConfigMap{globalconfig.ObservedBuildConfig(hcp.Namespace), globalconfig.ObservedProjectConfig(hcp.Namespace)} {
			assert.Expect(apierrors.IsNotFound(controlPlane.Get(t.Context(), client.ObjectKeyFromObject(destination), destination))).To(BeTrue())
		}
	})
	t.Run("When observed reads fail, it should attempt both sources and preserve error order", func(t *testing.T) {
		assert := NewWithT(t)
		var attempts []string
		failure := errors.New("read failure")
		guest := fake.NewClientBuilder().WithScheme(api.Scheme).WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, target client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption) error {
			attempts = append(attempts, fmt.Sprintf("%T", object))
			return failure
		}}).Build()
		_, controlPlane := configurationClients()
		root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
		errs := ReconcileObserved(t.Context(), root.hosted(), root.controlPlane(), ObservedConfigParams{Namespace: configurationHCP().Namespace, OwnerRef: config.OwnerRefFrom(configurationHCP())})
		assert.Expect(errs).To(HaveLen(2))
		assert.Expect(attempts).To(Equal([]string{"*v1.Build", "*v1.Project"}))
		for _, err := range errs {
			assert.Expect(errors.Is(err, failure)).To(BeTrue())
			assert.Expect(err).To(MatchError("cannot get config (cluster): read failure"))
		}
	})
}
