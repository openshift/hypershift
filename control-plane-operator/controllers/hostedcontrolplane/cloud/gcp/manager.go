// Package gcp reconciles the managed GCP worker firewall rule
// (<infra-id>-internal-cluster) for a HostedControlPlane. The reconcile is
// name-based and idempotent, and is designed to "degrade, don't wedge": expected
// recoverable states (missing WIF credentials, insufficient IAM permissions,
// unresolvable project/VPC, ownership conflicts, in-flight operations) are
// reported through the GCPFirewallRulesReady condition without failing the
// overall HCP reconcile.
package gcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-logr/logr"
	"google.golang.org/api/compute/v1"
)

const (
	// gcpAPITimeout bounds individual GCP API calls to prevent a hung reconciler.
	// Matches the PSC controller.
	gcpAPITimeout = 30 * time.Second

	// operationWaitTimeout bounds waiting for a single global operation to reach
	// DONE. If it is exceeded the operation is treated as still pending
	// (recoverable) and converges on the next reconcile.
	operationWaitTimeout = 2 * time.Minute
)

// OutcomeStatus classifies the result of a firewall reconcile so the caller can
// decide how to react.
type OutcomeStatus int

const (
	// OutcomeConverged means the managed rule matches the desired state.
	OutcomeConverged OutcomeStatus = iota
	// OutcomeDegraded means an expected, recoverable state was hit (missing
	// creds, 403, unresolvable infra, conflict, in-flight op). The caller sets
	// GCPFirewallRulesReady=False with the given reason/message and returns NO
	// aggregate error.
	OutcomeDegraded
	// OutcomeError means an unexpected failure occurred. The caller sets
	// GCPFirewallRulesReady=False and returns the error into the reconcile
	// aggregate so it is retried and surfaced.
	OutcomeError
)

// Result is the classified outcome of Reconcile/Delete.
type Result struct {
	Status  OutcomeStatus
	Reason  string
	Message string
	// Err is set only when Status is OutcomeError.
	Err error
}

func convergedResult() Result {
	return Result{Status: OutcomeConverged, Reason: hyperv1.AsExpectedReason, Message: hyperv1.AllIsWellMessage}
}

func degradedResult(reason, message string) Result {
	return Result{Status: OutcomeDegraded, Reason: reason, Message: message}
}

func errorResult(reason string, err error) Result {
	return Result{Status: OutcomeError, Reason: reason, Message: err.Error(), Err: err}
}

// clientBuilder builds a firewallClient. It is a field so tests can inject a
// fake without touching WIF credentials.
type clientBuilder func(ctx context.Context) (firewallClient, error)

// FirewallManager reconciles the managed worker firewall rule for a single HCP.
type FirewallManager struct {
	projectID   string
	network     string
	infraID     string
	networkType hyperv1.NetworkType
	logger      logr.Logger

	newClient clientBuilder
	// wifAvailable reports whether WIF credentials are ready. Overridable in tests.
	wifAvailable func() bool
}

// NewFirewallManager builds a FirewallManager from the HCP's GCP configuration.
// It does not build the Compute client; that happens lazily on the first API
// call so a missing WIF token degrades gracefully rather than erroring.
func NewFirewallManager(hcp *hyperv1.HostedControlPlane, logger logr.Logger) (*FirewallManager, error) {
	if hcp.Spec.Platform.GCP == nil {
		return nil, fmt.Errorf("hostedcontrolplane has no GCP platform spec")
	}
	return &FirewallManager{
		projectID:    hcp.Spec.Platform.GCP.Project,
		network:      string(hcp.Spec.Platform.GCP.NetworkConfig.Network.Name),
		infraID:      hcp.Spec.InfraID,
		networkType:  hcp.Spec.Networking.NetworkType,
		logger:       logger,
		newClient:    newComputeFirewallClient,
		wifAvailable: isWIFTokenAccessible,
	}, nil
}

// validateInputs verifies the static configuration needed to build the desired
// rule is present.
func (m *FirewallManager) validateInputs() error {
	if m.projectID == "" {
		return fmt.Errorf("GCP project is not set")
	}
	if m.network == "" {
		return fmt.Errorf("GCP VPC network is not set")
	}
	if m.infraID == "" {
		return fmt.Errorf("infra ID is not set")
	}
	return nil
}

// getClient builds the firewallClient, gating on WIF availability.
func (m *FirewallManager) getClient(ctx context.Context) (firewallClient, error) {
	if !m.wifAvailable() {
		return nil, errWIFUnavailable
	}
	return m.newClient(ctx)
}

var errWIFUnavailable = errors.New("GCP Workload Identity Federation credentials are not yet available")

// resolveNetwork verifies the HCP's VPC exists and returns its self-link so
// full/partial references normalize consistently. It never falls back to
// Google's default VPC.
func (m *FirewallManager) resolveNetwork(ctx context.Context, client firewallClient) (string, error) {
	apiCtx, cancel := context.WithTimeout(ctx, gcpAPITimeout)
	defer cancel()
	network, err := client.GetNetwork(apiCtx, m.projectID, m.network)
	if err != nil {
		return "", err
	}
	if network.SelfLink != "" {
		return network.SelfLink, nil
	}
	return fmt.Sprintf("projects/%s/global/networks/%s", m.projectID, m.network), nil
}

// Reconcile drives the managed firewall rule to its desired state and returns a
// classified Result.
func (m *FirewallManager) Reconcile(ctx context.Context) Result {
	if err := m.validateInputs(); err != nil {
		return degradedResult(hyperv1.GCPFirewallWaitingForInfra, err.Error())
	}

	client, err := m.getClient(ctx)
	if err != nil {
		if errors.Is(err, errWIFUnavailable) {
			m.logger.Info("WARNING: GCP WIF credentials not yet available, deferring firewall reconcile")
			return degradedResult(hyperv1.GCPFirewallWaitingForCredentials, err.Error())
		}
		// Failing to build the client from present creds is unexpected.
		return errorResult(hyperv1.GCPFirewallWaitingForCredentials, fmt.Errorf("failed to build GCP compute client: %w", err))
	}

	networkSelfLink, err := m.resolveNetwork(ctx, client)
	if err != nil {
		return m.classify(err, "failed to resolve GCP VPC network")
	}

	name := firewallRuleName(m.infraID)
	getCtx, cancel := context.WithTimeout(ctx, gcpAPITimeout)
	existing, err := client.GetFirewall(getCtx, m.projectID, name)
	cancel()
	if err != nil {
		if isNotFoundError(err) {
			return m.create(ctx, client, networkSelfLink)
		}
		return m.classify(err, "failed to get firewall rule")
	}

	// Rule exists: verify ownership before touching it.
	if !isOwnedBy(existing, m.infraID) {
		msg := fmt.Sprintf("firewall rule %q exists but is missing a matching control-plane-operator ownership marker; leaving it untouched", name)
		m.logger.Info("WARNING: " + msg)
		return degradedResult(hyperv1.GCPFirewallOwnershipConflict, msg)
	}

	// Verify compatibility (VPC, direction, allow-vs-deny).
	if ok, reason := isCompatible(existing, networkSelfLink); !ok {
		msg := fmt.Sprintf("firewall rule %q is incompatible (%s); leaving it untouched", name, reason)
		m.logger.Info("WARNING: " + msg)
		return degradedResult(hyperv1.GCPFirewallOwnershipConflict, msg)
	}

	desired := desiredFirewall(m.infraID, networkSelfLink, m.networkType)
	if firewallMatchesDesired(existing, desired) {
		return convergedResult()
	}

	return m.update(ctx, client, name, desired)
}

// create inserts a new managed firewall rule (with the ownership marker in its
// description), waits for the operation, and verifies convergence.
func (m *FirewallManager) create(ctx context.Context, client firewallClient, networkSelfLink string) Result {
	desired := desiredFirewall(m.infraID, networkSelfLink, m.networkType)
	marker, err := ownershipMarker(m.infraID)
	if err != nil {
		return errorResult(hyperv1.GCPFirewallWaitingForInfra, err)
	}
	desired.Description = marker

	m.logger.Info("Creating managed worker firewall rule", "name", desired.Name)
	insertCtx, cancel := context.WithTimeout(ctx, gcpAPITimeout)
	op, err := client.InsertFirewall(insertCtx, m.projectID, desired)
	cancel()
	if err != nil {
		if isAlreadyExistsError(err) {
			// Raced with another create; re-read and reconcile on the next pass.
			m.logger.Info("Firewall rule already exists, will reconcile on next pass", "name", desired.Name)
			return degradedResult(hyperv1.GCPFirewallWaitingForInfra, "firewall rule already exists, reconciling")
		}
		return m.classify(err, "failed to create firewall rule")
	}

	if res, done := m.waitOp(ctx, client, op, "create"); !done {
		return res
	}
	return convergedResult()
}

// update patches the existing rule to the exact desired traffic policy,
// selectors, priority, and enabled state, clearing any injected source ranges.
func (m *FirewallManager) update(ctx context.Context, client firewallClient, name string, desired *compute.Firewall) Result {
	m.logger.Info("Updating managed worker firewall rule to desired state", "name", name)
	patchCtx, cancel := context.WithTimeout(ctx, gcpAPITimeout)
	op, err := client.PatchFirewall(patchCtx, m.projectID, name, desired)
	cancel()
	if err != nil {
		return m.classify(err, "failed to update firewall rule")
	}
	if res, done := m.waitOp(ctx, client, op, "update"); !done {
		return res
	}
	return convergedResult()
}

// waitOp waits for a global operation to reach DONE. It returns (Result, done).
// When done is false the returned Result is what the caller should return
// (degraded for pending/timeouts, error/degraded for classified failures).
func (m *FirewallManager) waitOp(ctx context.Context, client firewallClient, op *compute.Operation, action string) (Result, bool) {
	// A DONE operation returned inline still needs its Error checked.
	if op.Status == "DONE" {
		if op.Error != nil {
			return m.classifyOpError(op, action), false
		}
		return Result{}, true
	}

	waitCtx, cancel := context.WithTimeout(ctx, operationWaitTimeout)
	defer cancel()
	waited, err := client.WaitForGlobalOperation(waitCtx, m.projectID, op.Name)
	if err != nil {
		// Timeout or transient wait error: treat as pending, converge next pass.
		msg := fmt.Sprintf("waiting for firewall %s operation to complete: %v", action, err)
		m.logger.Info("WARNING: " + msg)
		return degradedResult(hyperv1.GCPFirewallWaitingForInfra, msg), false
	}
	if waited.Status != "DONE" {
		msg := fmt.Sprintf("firewall %s operation still in progress", action)
		m.logger.Info(msg, "operation", op.Name, "status", waited.Status)
		return degradedResult(hyperv1.GCPFirewallWaitingForInfra, msg), false
	}
	if waited.Error != nil {
		return m.classifyOpError(waited, action), false
	}
	return Result{}, true
}

// Delete removes the managed firewall rule, verifying ownership and
// compatibility first. A missing rule is success. Any other failure/conflict
// returns an error so the caller retains the finalizer and retries.
func (m *FirewallManager) Delete(ctx context.Context) error {
	if err := m.validateInputs(); err != nil {
		return fmt.Errorf("cannot delete firewall rule: %w", err)
	}

	client, err := m.getClient(ctx)
	if err != nil {
		return fmt.Errorf("cannot delete firewall rule: %w", err)
	}

	name := firewallRuleName(m.infraID)
	getCtx, cancel := context.WithTimeout(ctx, gcpAPITimeout)
	existing, err := client.GetFirewall(getCtx, m.projectID, name)
	cancel()
	if err != nil {
		if isNotFoundError(err) {
			m.logger.Info("Managed worker firewall rule already deleted", "name", name)
			return nil
		}
		return fmt.Errorf("failed to get firewall rule %q for deletion: %w", name, err)
	}

	if !isOwnedBy(existing, m.infraID) {
		return fmt.Errorf("firewall rule %q is not owned by control-plane-operator; refusing to delete", name)
	}

	m.logger.Info("Deleting managed worker firewall rule", "name", name)
	delCtx, cancelDel := context.WithTimeout(ctx, gcpAPITimeout)
	op, err := client.DeleteFirewall(delCtx, m.projectID, name)
	cancelDel()
	if err != nil {
		if isNotFoundError(err) {
			return nil
		}
		return fmt.Errorf("failed to delete firewall rule %q: %w", name, err)
	}

	if op.Status == "DONE" {
		if op.Error != nil {
			return fmt.Errorf("firewall rule deletion failed: %s", formatOperationErrors(op.Error.Errors))
		}
	} else {
		waitCtx, cancelWait := context.WithTimeout(ctx, operationWaitTimeout)
		waited, waitErr := client.WaitForGlobalOperation(waitCtx, m.projectID, op.Name)
		cancelWait()
		if waitErr != nil {
			return fmt.Errorf("failed waiting for firewall rule deletion: %w", waitErr)
		}
		if waited.Status != "DONE" {
			return fmt.Errorf("firewall rule deletion still in progress")
		}
		if waited.Error != nil {
			return fmt.Errorf("firewall rule deletion failed: %s", formatOperationErrors(waited.Error.Errors))
		}
	}

	// Confirm the rule is gone.
	confirmCtx, cancelConfirm := context.WithTimeout(ctx, gcpAPITimeout)
	_, err = client.GetFirewall(confirmCtx, m.projectID, name)
	cancelConfirm()
	if err == nil {
		return fmt.Errorf("firewall rule %q still exists after deletion", name)
	}
	if !isNotFoundError(err) {
		return fmt.Errorf("failed to confirm firewall rule deletion: %w", err)
	}
	m.logger.Info("Deleted managed worker firewall rule", "name", name)
	return nil
}

// Condition renders the Result as a GCPFirewallRulesReady condition observing
// the provided generation.
func (r Result) Condition(observedGeneration int64) metav1.Condition {
	status := metav1.ConditionFalse
	if r.Status == OutcomeConverged {
		status = metav1.ConditionTrue
	}
	return metav1.Condition{
		Type:               string(hyperv1.GCPFirewallRulesReady),
		Status:             status,
		Reason:             r.Reason,
		Message:            r.Message,
		ObservedGeneration: observedGeneration,
	}
}
