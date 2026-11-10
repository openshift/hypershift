package testtrace

import (
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func secret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
}

func TestRecorder_Operations(t *testing.T) {
	t.Run("When operations are recorded, it should return them in call order as a copy", func(t *testing.T) {
		g := NewWithT(t)
		recorder := &Recorder{}
		recorder.record("get", "hosted", secret("ns", "first"), types.NamespacedName{Namespace: "ns", Name: "first"})
		recorder.record("delete", "control-plane", secret("ns", "second"), types.NamespacedName{Namespace: "ns", Name: "second"})

		operations := recorder.Operations()
		operations[0] = "mutated"

		g.Expect(recorder.Operations()).To(Equal([]string{"get hosted *v1.Secret ns/first", "delete control-plane *v1.Secret ns/second"}))
	})
}

func TestRecorder_Cluster(t *testing.T) {
	t.Run("When operations span clusters, it should return only the named cluster's operations in order", func(t *testing.T) {
		g := NewWithT(t)
		recorder := &Recorder{}
		recorder.record("get", "hosted", secret("ns", "a"), types.NamespacedName{Namespace: "ns", Name: "a"})
		recorder.record("get", "control-plane", secret("ns", "b"), types.NamespacedName{Namespace: "ns", Name: "b"})
		recorder.record("create", "hosted", secret("ns", "a"), types.NamespacedName{Namespace: "ns", Name: "a"})

		g.Expect(recorder.Cluster("hosted")).To(Equal([]string{"get hosted *v1.Secret ns/a", "create hosted *v1.Secret ns/a"}))
		g.Expect(recorder.Cluster("control-plane")).To(Equal([]string{"get control-plane *v1.Secret ns/b"}))
		g.Expect(recorder.Cluster("unknown")).To(BeEmpty())
	})
}

func TestObjectKey(t *testing.T) {
	t.Run("When the object is namespaced or cluster-scoped, it should render its type, namespace and name", func(t *testing.T) {
		g := NewWithT(t)

		g.Expect(ObjectKey(secret("clusters-example", "pull-secret"))).To(Equal("*v1.Secret clusters-example/pull-secret"))
		g.Expect(ObjectKey(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}})).To(Equal("*v1.Namespace /kube-system"))
	})
}

func TestNewClient(t *testing.T) {
	t.Run("When each client operation runs, it should record get, create, update, patch and delete under the cluster", func(t *testing.T) {
		g := NewWithT(t)
		recorder := &Recorder{}
		c := NewClient(recorder, "hosted", ClientOptions{})
		object := secret("ns", "traced")

		g.Expect(c.Create(t.Context(), object)).To(Succeed())
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(object), object)).To(Succeed())
		object.Data = map[string][]byte{"key": []byte("value")}
		g.Expect(c.Update(t.Context(), object)).To(Succeed())
		original := object.DeepCopy()
		object.Labels = map[string]string{"patched": "true"}
		g.Expect(c.Patch(t.Context(), object, client.MergeFrom(original))).To(Succeed())
		g.Expect(c.Delete(t.Context(), object)).To(Succeed())

		g.Expect(recorder.Operations()).To(Equal([]string{
			"create hosted *v1.Secret ns/traced",
			"get hosted *v1.Secret ns/traced",
			"update hosted *v1.Secret ns/traced",
			"patch hosted *v1.Secret ns/traced",
			"delete hosted *v1.Secret ns/traced",
		}))
	})

	t.Run("When a get failure is configured, it should record the get and return the failure instead of the seeded object", func(t *testing.T) {
		g := NewWithT(t)
		recorder := &Recorder{}
		failure := errors.New("cp unavailable")
		seeded := secret("ns", "seeded")
		c := NewClient(recorder, "control-plane", ClientOptions{Objects: []client.Object{seeded}, GetFailures: map[string]error{ObjectKey(seeded): failure}})

		err := c.Get(t.Context(), client.ObjectKeyFromObject(seeded), &corev1.Secret{})

		g.Expect(err).To(BeIdenticalTo(failure))
		g.Expect(recorder.Operations()).To(Equal([]string{"get control-plane *v1.Secret ns/seeded"}))
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(seeded), &corev1.Namespace{})).NotTo(BeIdenticalTo(failure))
	})

	t.Run("When a status subresource is registered, it should keep main updates from persisting status", func(t *testing.T) {
		g := NewWithT(t)
		hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "hcp"}}
		c := NewClient(&Recorder{}, "control-plane", ClientOptions{Objects: []client.Object{hcp}, StatusSubresources: []client.Object{&hyperv1.HostedControlPlane{}}})

		current := &hyperv1.HostedControlPlane{}
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hcp), current)).To(Succeed())
		current.Status.Ready = true
		g.Expect(c.Update(t.Context(), current)).To(Succeed())

		persisted := &hyperv1.HostedControlPlane{}
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hcp), persisted)).To(Succeed())
		g.Expect(persisted.Status.Ready).To(BeFalse())
	})
}

func TestFailingUpserter_CreateOrUpdate(t *testing.T) {
	t.Run("When the object has a configured failure, it should fail before any client call", func(t *testing.T) {
		g := NewWithT(t)
		recorder := &Recorder{}
		failure := errors.New("write failure")
		object := secret("ns", "failing")
		upserter := FailingUpserter{Failures: map[string]error{ObjectKey(object): failure}}

		result, err := upserter.CreateOrUpdate(t.Context(), NewClient(recorder, "hosted", ClientOptions{}), object, func() error { return nil })

		g.Expect(err).To(BeIdenticalTo(failure))
		g.Expect(result).To(Equal(controllerutil.OperationResultNone))
		g.Expect(recorder.Operations()).To(BeEmpty())
	})

	t.Run("When the object has no configured failure, it should create it through the given client", func(t *testing.T) {
		g := NewWithT(t)
		recorder := &Recorder{}
		object := secret("ns", "created")

		result, err := FailingUpserter{}.CreateOrUpdate(t.Context(), NewClient(recorder, "hosted", ClientOptions{}), object, func() error {
			object.Data = map[string][]byte{"key": []byte("value")}
			return nil
		})

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(result).To(Equal(controllerutil.OperationResultCreated))
		g.Expect(recorder.Operations()).To(Equal([]string{"get hosted *v1.Secret ns/created", "create hosted *v1.Secret ns/created"}))
	})
}
