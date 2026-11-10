//go:build e2ev2 && backuprestore

package backuprestore

import (
	"fmt"
	"sort"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// MachineIdentity captures the provider-level identity of a CAPI Machine.
type MachineIdentity struct {
	// Name is the CAPI Machine resource name.
	Name string
	// ProviderID is the cloud instance identifier (e.g. aws:///us-east-1a/i-0abc123).
	ProviderID string
	// NodeRefName is the hosted cluster Node name linked by the Machine, if any.
	NodeRefName string
}

// CaptureMachineIdentities lists CAPI Machines in the control plane namespace
// and returns their provider-level identities sorted by name.
func CaptureMachineIdentities(testCtx *internal.TestContext) ([]MachineIdentity, error) {
	list := &capiv1.MachineList{}
	if err := testCtx.MgmtClient.List(testCtx.Context, list,
		crclient.InNamespace(testCtx.ControlPlaneNamespace)); err != nil {
		return nil, fmt.Errorf("failed to list Machines in namespace %s: %w", testCtx.ControlPlaneNamespace, err)
	}

	var ids []MachineIdentity
	for i := range list.Items {
		m := &list.Items[i]
		id := MachineIdentity{Name: m.Name}
		if m.Spec.ProviderID != "" {
			id.ProviderID = m.Spec.ProviderID
		}
		if m.Status.NodeRef.IsDefined() {
			id.NodeRefName = m.Status.NodeRef.Name
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Name < ids[j].Name })
	return ids, nil
}

// ProviderIDs extracts a sorted list of provider IDs from a slice of MachineIdentity.
func ProviderIDs(machines []MachineIdentity) []string {
	var ids []string
	for _, m := range machines {
		if m.ProviderID != "" {
			ids = append(ids, m.ProviderID)
		}
	}
	sort.Strings(ids)
	return ids
}

// NodeIdentity captures the fields that identify a hosted cluster Node.
type NodeIdentity struct {
	// Name is the Node resource name.
	Name string
	// UID is the Kubernetes UID of the Node.
	UID string
	// ProviderID is the cloud instance identifier set on the Node spec.
	ProviderID string
}

// CaptureNodeIdentities lists Nodes in the hosted cluster and returns their
// identities sorted by name.
func CaptureNodeIdentities(testCtx *internal.TestContext) ([]NodeIdentity, error) {
	hc := &hyperv1.HostedCluster{}
	if err := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
		Name:      testCtx.ClusterName,
		Namespace: testCtx.ClusterNamespace,
	}, hc); err != nil {
		return nil, fmt.Errorf("failed to get HostedCluster: %w", err)
	}

	guestClient, err := testCtx.GetHostedClusterClient(hc)
	if err != nil {
		return nil, fmt.Errorf("failed to create hosted cluster client: %w", err)
	}

	nodeList := &corev1.NodeList{}
	if err := guestClient.List(testCtx.Context, nodeList); err != nil {
		return nil, fmt.Errorf("failed to list Nodes in hosted cluster: %w", err)
	}

	var ids []NodeIdentity
	for i := range nodeList.Items {
		n := &nodeList.Items[i]
		ids = append(ids, NodeIdentity{
			Name:       n.Name,
			UID:        string(n.UID),
			ProviderID: n.Spec.ProviderID,
		})
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Name < ids[j].Name })
	return ids, nil
}

// NodeNames extracts a sorted list of node names from a slice of NodeIdentity.
func NodeNames(nodes []NodeIdentity) []string {
	var names []string
	for _, n := range nodes {
		names = append(names, n.Name)
	}
	sort.Strings(names)
	return names
}

// NodeUIDs extracts a sorted list of node UIDs from a slice of NodeIdentity.
func NodeUIDs(nodes []NodeIdentity) []string {
	var uids []string
	for _, n := range nodes {
		if n.UID != "" {
			uids = append(uids, n.UID)
		}
	}
	sort.Strings(uids)
	return uids
}
