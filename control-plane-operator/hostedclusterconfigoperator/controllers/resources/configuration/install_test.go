package configuration

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/releaseinfo"

	imagev1 "github.com/openshift/api/image/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestReconcileInstallMetadata(t *testing.T) {
	t.Run("When component version parsing fails, it should retain the install error context", func(t *testing.T) {
		assert := NewWithT(t)
		guest, _ := configurationClients()
		root := &configurationTestClients{client: guest, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
		release := &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{Spec: imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{Name: "component", Annotations: map[string]string{"io.openshift.build.versions": "invalid"}}}}}}
		assert.Expect(ReconcileInstallMetadata(t.Context(), root.hosted(), release.ComponentVersions)).To(MatchError(ContainSubstring("failed to reconcile install configmap: failed to look up component versions")))
	})
	t.Run("When install metadata is reconciled, it should retain the original version and unrelated data", func(t *testing.T) {
		assert := NewWithT(t)
		guest, _ := configurationClients()
		root := &configurationTestClients{client: guest, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
		release := &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.21.10"}}}
		assert.Expect(ReconcileInstallMetadata(t.Context(), root.hosted(), release.ComponentVersions)).To(Succeed())
		configMap := manifests.InstallConfigMap()
		assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(configMap), configMap)).To(Succeed())
		assert.Expect(configMap.Data["version"]).To(Equal("4.21.10"))
		configMap.Data["unowned"] = "preserved"
		assert.Expect(guest.Update(t.Context(), configMap)).To(Succeed())
		assert.Expect(ReconcileInstallMetadata(t.Context(), root.hosted(), nil)).To(Succeed())
		assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(configMap), configMap)).To(Succeed())
		assert.Expect(configMap.Data).To(Equal(map[string]string{"version": "4.21.10", "invoker": "hypershift", "unowned": "preserved"}))
	})
}
