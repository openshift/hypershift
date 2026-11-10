package integration

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/internal/testtrace"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNewHostedCluster(t *testing.T) {
	t.Run("When the adapter is used, it should read, upsert and delete through the given hosted client and upsert function", func(t *testing.T) {
		g := NewWithT(t)
		trace := &testtrace.Recorder{}
		hostedClient := testtrace.NewClient(trace, "hosted", testtrace.ClientOptions{})
		hosted := NewHostedCluster(hostedClient, testtrace.FailingUpserter{}.CreateOrUpdate)
		secret := manifests.KubeadminPasswordHashSecret()

		_, err := hosted.upsert(t.Context(), secret, func() error { return nil })
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(hosted.get(t.Context(), client.ObjectKeyFromObject(secret), secret)).To(Succeed())
		g.Expect(hosted.delete(t.Context(), secret)).To(Succeed())

		key := testtrace.ObjectKey(manifests.KubeadminPasswordHashSecret())
		g.Expect(trace.Operations()).To(Equal([]string{"get hosted " + key, "create hosted " + key, "get hosted " + key, "delete hosted " + key}))
	})
}

func TestNewControlPlane(t *testing.T) {
	t.Run("When the adapter is used, it should read and upsert through the given control-plane client and upsert function", func(t *testing.T) {
		g := NewWithT(t)
		trace := &testtrace.Recorder{}
		controlPlane := NewControlPlane(testtrace.NewClient(trace, "control-plane", testtrace.ClientOptions{Objects: []client.Object{kubeadminPasswordSecret()}}), testtrace.FailingUpserter{}.CreateOrUpdate)
		secret := kubeadminPasswordSecret()

		g.Expect(controlPlane.get(t.Context(), client.ObjectKeyFromObject(secret), secret)).To(Succeed())
		_, err := controlPlane.upsert(t.Context(), secret, func() error {
			secret.Annotations = map[string]string{"example": "value"}
			return nil
		})
		g.Expect(err).NotTo(HaveOccurred())

		key := testtrace.ObjectKey(kubeadminPasswordSecret())
		g.Expect(trace.Operations()).To(Equal([]string{"get control-plane " + key, "get control-plane " + key, "update control-plane " + key}))
	})
}
