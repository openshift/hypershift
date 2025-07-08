package supportedversion

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"

	manifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests/supportedversion"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/support/upsert"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestEnsureSupportedVersionConfigMap(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	reconciler := &Reconciler{
		Client:                 c,
		CreateOrUpdateProvider: upsert.New(true),
		namespace:              "hypershift",
	}
	g := NewGomegaWithT(t)
	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{})
	g.Expect(err).To(BeNil())
	cfgMap := manifests.ConfigMap("hypershift")
	err = c.Get(context.Background(), client.ObjectKeyFromObject(cfgMap), cfgMap)
	g.Expect(err).To(BeNil())
	g.Expect(cfgMap.Data[ConfigMapVersionsKey]).ToNot(BeEmpty())
	data := &SupportedVersions{}
	err = json.Unmarshal([]byte(cfgMap.Data[ConfigMapVersionsKey]), data)
	g.Expect(err).To(BeNil())
	g.Expect(len(data.Versions)).To(Equal(len(supportedversion.Supported())))
}
