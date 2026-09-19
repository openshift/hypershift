package gcp

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/go-logr/logr"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

const (
	testInfraID = "example-abcde"
	testProject = "my-project-123"
	testNetwork = "example-abcde-network"
)

// fakeFirewallClient is an in-memory firewallClient for tests.
type fakeFirewallClient struct {
	firewalls map[string]*compute.Firewall
	network   *compute.Network

	getFirewallErr error
	insertErr      error
	patchErr       error
	deleteErr      error
	getNetworkErr  error
	opError        *compute.OperationError
	insertOpStatus string // defaults to "DONE"
	insertCalled   bool
	patchCalled    bool
	deleteCalled   bool
	lastInserted   *compute.Firewall
	lastPatched    *compute.Firewall
}

func newFakeClient() *fakeFirewallClient {
	return &fakeFirewallClient{
		firewalls: map[string]*compute.Firewall{},
		network: &compute.Network{
			Name:     testNetwork,
			SelfLink: "projects/" + testProject + "/global/networks/" + testNetwork,
		},
	}
}

func notFound() error {
	return &googleapi.Error{Code: 404, Message: "not found"}
}

func (f *fakeFirewallClient) GetFirewall(_ context.Context, _, name string) (*compute.Firewall, error) {
	if f.getFirewallErr != nil {
		return nil, f.getFirewallErr
	}
	fw, ok := f.firewalls[name]
	if !ok {
		return nil, notFound()
	}
	return fw, nil
}

func (f *fakeFirewallClient) InsertFirewall(_ context.Context, _ string, firewall *compute.Firewall) (*compute.Operation, error) {
	f.insertCalled = true
	f.lastInserted = firewall
	if f.insertErr != nil {
		return nil, f.insertErr
	}
	f.firewalls[firewall.Name] = firewall
	status := f.insertOpStatus
	if status == "" {
		status = "DONE"
	}
	return &compute.Operation{Name: "op-insert", Status: status, Error: f.opError}, nil
}

func (f *fakeFirewallClient) PatchFirewall(_ context.Context, _, name string, firewall *compute.Firewall) (*compute.Operation, error) {
	f.patchCalled = true
	f.lastPatched = firewall
	if f.patchErr != nil {
		return nil, f.patchErr
	}
	firewall.Name = name
	f.firewalls[name] = firewall
	return &compute.Operation{Name: "op-patch", Status: "DONE", Error: f.opError}, nil
}

func (f *fakeFirewallClient) DeleteFirewall(_ context.Context, _, name string) (*compute.Operation, error) {
	f.deleteCalled = true
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	delete(f.firewalls, name)
	return &compute.Operation{Name: "op-delete", Status: "DONE", Error: f.opError}, nil
}

func (f *fakeFirewallClient) GetNetwork(_ context.Context, _, _ string) (*compute.Network, error) {
	if f.getNetworkErr != nil {
		return nil, f.getNetworkErr
	}
	return f.network, nil
}

func (f *fakeFirewallClient) WaitForGlobalOperation(_ context.Context, _, opName string) (*compute.Operation, error) {
	return &compute.Operation{Name: opName, Status: "DONE", Error: f.opError}, nil
}

func testManager(client firewallClient) *FirewallManager {
	return &FirewallManager{
		projectID:    testProject,
		network:      testNetwork,
		infraID:      testInfraID,
		networkType:  hyperv1.OVNKubernetes,
		logger:       logr.Discard(),
		newClient:    func(context.Context) (firewallClient, error) { return client, nil },
		wifAvailable: func() (bool, error) { return true, nil },
	}
}

func TestFirewallManagerReconcile(t *testing.T) {
	ctx := context.Background()

	t.Run("When the rule is missing, it should create it with the ownership marker", func(t *testing.T) {
		g := NewWithT(t)
		client := newFakeClient()
		res := testManager(client).Reconcile(ctx)

		g.Expect(res.Status).To(Equal(OutcomeConverged))
		g.Expect(client.insertCalled).To(BeTrue())
		g.Expect(client.lastInserted.Description).To(ContainSubstring("control-plane-operator"))
		g.Expect(client.lastInserted.Description).To(ContainSubstring(testInfraID))
		// SDK encoding: disabled:false and cleared source ranges must be forced on the wire.
		g.Expect(client.lastInserted.ForceSendFields).To(ContainElements("Disabled", "SourceRanges"))
		g.Expect(client.lastInserted.SourceRanges).To(BeEmpty())
	})

	t.Run("When the rule already matches desired, it should be a no-op", func(t *testing.T) {
		g := NewWithT(t)
		client := newFakeClient()
		name := firewallRuleName(testInfraID)
		existing := desiredFirewall(testInfraID, client.network.SelfLink, hyperv1.OVNKubernetes)
		existing.Network = client.network.SelfLink
		marker, _ := ownershipMarker(testInfraID)
		existing.Description = marker
		client.firewalls[name] = existing

		res := testManager(client).Reconcile(ctx)
		g.Expect(res.Status).To(Equal(OutcomeConverged))
		g.Expect(client.patchCalled).To(BeFalse())
		g.Expect(client.insertCalled).To(BeFalse())
	})

	t.Run("When the rule has drifted, it should patch it back to desired", func(t *testing.T) {
		g := NewWithT(t)
		client := newFakeClient()
		name := firewallRuleName(testInfraID)
		existing := desiredFirewall(testInfraID, client.network.SelfLink, hyperv1.OVNKubernetes)
		existing.Network = client.network.SelfLink
		marker, _ := ownershipMarker(testInfraID)
		existing.Description = marker
		existing.SourceRanges = []string{"10.0.0.0/8"} // injected drift
		client.firewalls[name] = existing

		res := testManager(client).Reconcile(ctx)
		g.Expect(res.Status).To(Equal(OutcomeConverged))
		g.Expect(client.patchCalled).To(BeTrue())
		g.Expect(client.lastPatched.SourceRanges).To(BeEmpty())
		g.Expect(client.lastPatched.ForceSendFields).To(ContainElement("SourceRanges"))
	})

	t.Run("When a same-named rule lacks the ownership marker, it should report a conflict and leave it untouched", func(t *testing.T) {
		g := NewWithT(t)
		client := newFakeClient()
		name := firewallRuleName(testInfraID)
		client.firewalls[name] = &compute.Firewall{
			Name:        name,
			Network:     client.network.SelfLink,
			Direction:   "INGRESS",
			Description: "someone else's rule",
		}

		res := testManager(client).Reconcile(ctx)
		g.Expect(res.Status).To(Equal(OutcomeDegraded))
		g.Expect(res.Reason).To(Equal(hyperv1.GCPFirewallOwnershipConflict))
		g.Expect(client.patchCalled).To(BeFalse())
		g.Expect(client.deleteCalled).To(BeFalse())
	})

	t.Run("When an owned rule is in the wrong VPC, it should report a conflict and leave it untouched", func(t *testing.T) {
		g := NewWithT(t)
		client := newFakeClient()
		name := firewallRuleName(testInfraID)
		marker, _ := ownershipMarker(testInfraID)
		client.firewalls[name] = &compute.Firewall{
			Name:        name,
			Network:     "projects/" + testProject + "/global/networks/some-other-vpc",
			Direction:   "INGRESS",
			Description: marker,
		}

		res := testManager(client).Reconcile(ctx)
		g.Expect(res.Status).To(Equal(OutcomeDegraded))
		g.Expect(res.Reason).To(Equal(hyperv1.GCPFirewallOwnershipConflict))
		g.Expect(client.patchCalled).To(BeFalse())
	})

	t.Run("When WIF credentials are unavailable, it should degrade without an aggregate error", func(t *testing.T) {
		g := NewWithT(t)
		client := newFakeClient()
		m := testManager(client)
		m.wifAvailable = func() (bool, error) { return false, nil }

		res := m.Reconcile(ctx)
		g.Expect(res.Status).To(Equal(OutcomeDegraded))
		g.Expect(res.Reason).To(Equal(hyperv1.GCPFirewallWaitingForCredentials))
		g.Expect(res.Err).To(BeNil())
		g.Expect(client.insertCalled).To(BeFalse())
	})

	t.Run("When checking WIF availability fails unexpectedly, it should be a hard error", func(t *testing.T) {
		g := NewWithT(t)
		client := newFakeClient()
		m := testManager(client)
		m.wifAvailable = func() (bool, error) { return false, errors.New("permission denied reading token") }

		res := m.Reconcile(ctx)
		g.Expect(res.Status).To(Equal(OutcomeError))
		g.Expect(res.Err).To(HaveOccurred())
		g.Expect(client.insertCalled).To(BeFalse())
	})

	t.Run("When the API returns 403, it should degrade with an insufficient-permissions reason and no error", func(t *testing.T) {
		g := NewWithT(t)
		client := newFakeClient()
		client.insertErr = &googleapi.Error{Code: 403, Message: "permission denied"}

		res := testManager(client).Reconcile(ctx)
		g.Expect(res.Status).To(Equal(OutcomeDegraded))
		g.Expect(res.Reason).To(Equal(hyperv1.GCPFirewallInsufficientPermissions))
		g.Expect(res.Err).To(BeNil())
	})

	t.Run("When the VPC cannot be resolved, it should degrade with a waiting-for-infra reason", func(t *testing.T) {
		g := NewWithT(t)
		client := newFakeClient()
		client.getNetworkErr = notFound()

		res := testManager(client).Reconcile(ctx)
		g.Expect(res.Status).To(Equal(OutcomeDegraded))
		g.Expect(res.Reason).To(Equal(hyperv1.GCPFirewallWaitingForInfra))
		g.Expect(res.Err).To(BeNil())
	})

	t.Run("When insert races and returns AlreadyExists, it should degrade and reconcile next pass", func(t *testing.T) {
		g := NewWithT(t)
		client := newFakeClient()
		client.insertErr = &googleapi.Error{Code: 409, Message: "already exists"}

		res := testManager(client).Reconcile(ctx)
		g.Expect(res.Status).To(Equal(OutcomeDegraded))
		g.Expect(res.Err).To(BeNil())
	})
}

func TestFirewallManagerDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("When the owned rule exists, it should delete it", func(t *testing.T) {
		g := NewWithT(t)
		client := newFakeClient()
		name := firewallRuleName(testInfraID)
		marker, _ := ownershipMarker(testInfraID)
		client.firewalls[name] = &compute.Firewall{Name: name, Description: marker, Network: client.network.SelfLink, Direction: "INGRESS"}

		err := testManager(client).Delete(ctx)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(client.deleteCalled).To(BeTrue())
		g.Expect(client.firewalls).ToNot(HaveKey(name))
	})

	t.Run("When the rule is already missing, it should succeed", func(t *testing.T) {
		g := NewWithT(t)
		client := newFakeClient()

		err := testManager(client).Delete(ctx)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(client.deleteCalled).To(BeFalse())
	})

	t.Run("When the rule is not owned by CPO, it should error and not delete", func(t *testing.T) {
		g := NewWithT(t)
		client := newFakeClient()
		name := firewallRuleName(testInfraID)
		client.firewalls[name] = &compute.Firewall{Name: name, Description: "not ours"}

		err := testManager(client).Delete(ctx)
		g.Expect(err).To(HaveOccurred())
		g.Expect(client.deleteCalled).To(BeFalse())
	})

	t.Run("When WIF credentials are unavailable, it should error so the finalizer is retained", func(t *testing.T) {
		g := NewWithT(t)
		client := newFakeClient()
		m := testManager(client)
		m.wifAvailable = func() (bool, error) { return false, nil }

		err := m.Delete(ctx)
		g.Expect(err).To(HaveOccurred())
		g.Expect(client.deleteCalled).To(BeFalse())
	})
}
