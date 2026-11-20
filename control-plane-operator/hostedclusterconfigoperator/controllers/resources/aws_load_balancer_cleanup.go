package resources

import (
	"context"
	"errors"
	"sort"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/support/awsutil"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	awsLoadBalancerProgressDataKey         = "progress.json"
	kubernetesLoadBalancerCleanupFinalizer = "service.kubernetes.io/load-balancer-cleanup"
)

type awsLoadBalancerCleanupProgress struct {
	HCPUID   types.UID                              `json:"hcpUID"`
	InfraID  string                                 `json:"infraID"`
	Region   string                                 `json:"region,omitempty"`
	VPCID    string                                 `json:"vpcID,omitempty"`
	Services map[string]awsLoadBalancerServiceProof `json:"services"`
}

type awsLoadBalancerServiceProof struct {
	Namespace  string                         `json:"namespace"`
	Name       string                         `json:"name"`
	Candidates []awsLoadBalancerCandidate     `json:"candidates"`
	Identities []awsutil.LoadBalancerIdentity `json:"identities,omitempty"`
	// Records whether HCCO observed the cloud-provider finalizer before requesting Service deletion.
	LoadBalancerCleanupFinalizerObserved bool `json:"loadBalancerCleanupFinalizerObserved,omitempty"`
}

type awsLoadBalancerCandidate struct {
	Hostname string `json:"hostname,omitempty"`
	Name     string `json:"name,omitempty"`
	Region   string `json:"region,omitempty"`
}

type awsLoadBalancerCleanupState struct {
	services                      []corev1.Service
	currentServices               map[string]*corev1.Service
	names                         []string
	servicesWithNames             sets.Set[client.ObjectKey]
	servicesWithLoadBalancerClass sets.Set[client.ObjectKey]
	servicesWithoutNames          sets.Set[client.ObjectKey]
	progress                      *awsLoadBalancerCleanupProgress
	progressConfigMap             *corev1.ConfigMap
	progressExists                bool
	progressChanged               bool
	awsRegion                     string
	selector                      awsutil.LoadBalancerSelector
	classedServiceUIDs            sets.Set[string]
	classedCandidateNames         sets.Set[string]
	awsProgress                   *awsLoadBalancerCleanupProgress
	candidateNames                []string
	observations                  map[string]awsutil.NamedLoadBalancerObservation
	idsByName                     map[string][]awsutil.LoadBalancerIdentity
}

type awsLoadBalancerCandidateCleanup struct {
	eligibleNames        sets.Set[string]
	unverifiedNames      sets.Set[string]
	resolvedAbsentNames  sets.Set[string]
	unverifiedErrs       []error
	describeTagsDenied   bool
	orphanScanUnverified bool
	orphanScanErr        error
}

func (r *reconciler) reconcileAWSLoadBalancerCleanup(ctx context.Context, hcp *hyperv1.HostedControlPlane) (bool, error) {
	services := &corev1.ServiceList{}
	if err := r.client.List(ctx, services); err != nil {
		return false, newSafeLoadBalancerCleanupError("failed to list load balancer Services", err)
	}

	if r.awsLoadBalancerCleanup != nil {
		return r.ensureAWSLoadBalancersRemovedWithHook(ctx, hcp, services.Items)
	}
	state, err := r.prepareAWSLoadBalancerCleanupState(ctx, hcp, services.Items)
	if err != nil {
		return false, err
	}
	if len(state.names) == 0 && len(state.awsProgress.Services) == 0 {
		return r.cleanupAWSLoadBalancersWithoutCandidates(ctx, state)
	}
	if state.selector.VPCID == "" {
		return r.cleanupAWSLoadBalancersWithoutVPC(ctx, state)
	}
	if err := r.ensureAWSLoadBalancerClients(ctx, hcp); err != nil {
		return false, err
	}
	if err := r.inspectAndPersistAWSLoadBalancerCandidates(ctx, state); err != nil {
		return false, err
	}
	candidateCleanup, err := classifyAWSLoadBalancerCandidates(state)
	if err != nil {
		return false, err
	}
	cleanupResult, err := r.deleteEligibleAWSLoadBalancers(ctx, state, candidateCleanup)
	if err != nil {
		return false, err
	}
	if err := applyAWSLoadBalancerCleanupResult(&candidateCleanup, cleanupResult); err != nil {
		return false, err
	}
	serviceCleanupComplete, allCandidatesGone := evaluateAWSLoadBalancerCleanup(state, candidateCleanup, cleanupResult)
	deleteKeys, waitKeys, fallbackNeeded := loadBalancerServiceDeletionPlan(state, candidateCleanup, serviceCleanupComplete)
	kubernetesPending, err := r.requestLoadBalancerServiceDeletion(ctx, deleteKeys, waitKeys)
	if err != nil {
		return false, newSafeLoadBalancerCleanupError("failed to request load balancer Service cleanup", err)
	}
	if candidateCleanup.orphanScanErr != nil {
		return false, newSafeLoadBalancerCleanupError("AWS orphan target-group cleanup failed; resources may remain", candidateCleanup.orphanScanErr)
	}
	if err := r.retireCompletedAWSLoadBalancerCleanupProgress(ctx, state, cleanupResult, serviceCleanupComplete); err != nil {
		return false, err
	}
	if fallbackNeeded || !allCandidatesGone {
		return false, newSafeLoadBalancerCleanupError(awsLoadBalancerCleanupPendingMessage(candidateCleanup, cleanupResult), awsLoadBalancerCleanupPendingCause(candidateCleanup))
	}
	if kubernetesPending {
		return false, nil
	}
	return true, nil
}

func (r *reconciler) prepareAWSLoadBalancerCleanupState(ctx context.Context, hcp *hyperv1.HostedControlPlane, services []corev1.Service) (*awsLoadBalancerCleanupState, error) {
	names, servicesWithNames, servicesWithLoadBalancerClass, servicesWithoutNames := loadBalancerNamesFromServices(services)
	state := &awsLoadBalancerCleanupState{
		services:                      services,
		currentServices:               make(map[string]*corev1.Service, len(services)),
		names:                         names,
		servicesWithNames:             servicesWithNames,
		servicesWithLoadBalancerClass: servicesWithLoadBalancerClass,
		servicesWithoutNames:          servicesWithoutNames,
	}
	for i := range services {
		service := &services[i]
		state.currentServices[string(service.UID)] = service
	}
	progress, configMap, exists, err := r.loadAWSLoadBalancerCleanupProgress(ctx, hcp)
	if err != nil {
		return nil, err
	}
	state.progress = progress
	state.progressConfigMap = configMap
	state.progressExists = exists
	ownerReferenceChanged, err := ensureAWSLoadBalancerProgressOwnerReference(configMap, hcp)
	if err != nil {
		return nil, err
	}
	state.progressChanged = ownerReferenceChanged
	if hcp.Spec.Platform.AWS != nil {
		state.awsRegion = hcp.Spec.Platform.AWS.Region
		if hcp.Spec.Platform.AWS.CloudProviderConfig != nil {
			state.selector.VPCID = hcp.Spec.Platform.AWS.CloudProviderConfig.VPC
		}
	}
	state.selector.InfraID = hcp.Spec.InfraID
	if progress.Region == "" && state.awsRegion != "" {
		progress.Region = state.awsRegion
		state.progressChanged = true
	}
	if err := recordAWSLoadBalancerServiceCandidates(state); err != nil {
		return nil, err
	}
	if progress.VPCID == "" && state.selector.VPCID != "" {
		progress.VPCID = state.selector.VPCID
		state.progressChanged = true
	}
	if state.progressChanged && (len(names) > 0 || len(progress.Services) > 0) {
		if err := r.saveAWSLoadBalancerCleanupProgress(ctx, configMap, exists, progress); err != nil {
			return nil, newSafeLoadBalancerCleanupError("failed to persist AWS load balancer cleanup candidates; no AWS deletion was attempted", err)
		}
		state.progressExists = true
		state.progressChanged = false
	}
	populateAWSLoadBalancerProgress(state)
	return state, nil
}

func recordAWSLoadBalancerServiceCandidates(state *awsLoadBalancerCleanupState) error {
	for i := range state.services {
		service := &state.services[i]
		if service.Spec.Type != corev1.ServiceTypeLoadBalancer || hasCustomLoadBalancerClass(*service) {
			continue
		}
		candidates := loadBalancerCandidatesForService(*service)
		if len(candidates) == 0 {
			continue
		}
		if service.UID == "" {
			return newSafeLoadBalancerCleanupError("cannot persist AWS load balancer cleanup proof for a Service without a UID", nil)
		}
		uid := string(service.UID)
		proof, found := state.progress.Services[uid]
		if !found {
			proof = awsLoadBalancerServiceProof{Namespace: service.Namespace, Name: service.Name}
			state.progress.Services[uid] = proof
			state.progressChanged = true
		}
		if proof.Namespace != service.Namespace || proof.Name != service.Name {
			return newSafeLoadBalancerCleanupError("AWS load balancer cleanup proof does not match the current Service identity", nil)
		}
		if !proof.LoadBalancerCleanupFinalizerObserved && hasKubernetesLoadBalancerCleanupFinalizer(*service) {
			proof.LoadBalancerCleanupFinalizerObserved = true
			state.progressChanged = true
		}
		for _, candidate := range candidates {
			if mergeLoadBalancerCandidate(&proof, candidate) {
				state.progressChanged = true
			}
		}
		state.progress.Services[uid] = proof
	}
	for uid, proof := range state.progress.Services {
		if len(proof.Candidates) == 0 {
			delete(state.progress.Services, uid)
			state.progressChanged = true
		}
	}
	return nil
}

func populateAWSLoadBalancerProgress(state *awsLoadBalancerCleanupState) {
	progress := state.progress
	state.classedServiceUIDs = sets.New[string]()
	state.classedCandidateNames = sets.New[string]()
	for uid, service := range state.currentServices {
		if !hasCustomLoadBalancerClass(*service) {
			continue
		}
		state.classedServiceUIDs.Insert(uid)
		for _, candidate := range loadBalancerCandidatesForService(*service) {
			if candidate.Name != "" {
				state.classedCandidateNames.Insert(candidate.Name)
			}
		}
		if proof, found := progress.Services[uid]; found {
			for _, candidate := range proof.Candidates {
				if candidate.Name != "" {
					state.classedCandidateNames.Insert(candidate.Name)
				}
			}
		}
	}
	state.awsProgress = &awsLoadBalancerCleanupProgress{Services: make(map[string]awsLoadBalancerServiceProof, len(progress.Services))}
	for uid, proof := range progress.Services {
		if !state.classedServiceUIDs.Has(uid) {
			state.awsProgress.Services[uid] = proof
		}
	}
}

func (r *reconciler) cleanupAWSLoadBalancersWithoutCandidates(ctx context.Context, state *awsLoadBalancerCleanupState) (bool, error) {
	if state.progressExists && len(state.progress.Services) == 0 {
		if err := r.cpClient.Delete(ctx, state.progressConfigMap); err != nil && !apierrors.IsNotFound(err) {
			return false, newSafeLoadBalancerCleanupError("failed to retire empty AWS load balancer cleanup progress", err)
		}
	}
	keys := serviceKeysForLoadBalancerServices(state.services, nil, state.servicesWithLoadBalancerClass, state.servicesWithoutNames)
	pending, err := r.requestLoadBalancerServiceDeletion(ctx, keys, keys)
	if err != nil {
		return false, newSafeLoadBalancerCleanupError("failed to request Kubernetes load balancer Service cleanup", err)
	}
	return !pending, nil
}

func (r *reconciler) cleanupAWSLoadBalancersWithoutVPC(ctx context.Context, state *awsLoadBalancerCleanupState) (bool, error) {
	fallbackKeys := serviceKeysForLoadBalancerServices(state.services, state.servicesWithNames, state.servicesWithLoadBalancerClass, state.servicesWithoutNames)
	_, serviceCleanupErr := r.requestLoadBalancerServiceDeletion(ctx, fallbackKeys, fallbackKeys)
	vpcMissingMessage := "AWS VPC configuration is missing; Kubernetes cleanup was requested, but AWS load balancer ownership cannot be verified and AWS resources may remain"
	if serviceCleanupErr != nil {
		serviceCleanupMessage := "AWS VPC configuration is missing; Kubernetes Service cleanup also failed, AWS ownership cannot be verified, and AWS resources may remain"
		cause := errors.Join(errors.New(vpcMissingMessage), serviceCleanupErr)
		return false, newSafeLoadBalancerCleanupError(serviceCleanupMessage, cause)
	}
	return false, newSafeLoadBalancerCleanupError(vpcMissingMessage, nil)
}

func (r *reconciler) ensureAWSLoadBalancerClients(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	// Clients retain their initial region, role, and endpoint configuration, which
	// must remain stable during teardown. The SDK refreshes credentials and rereads
	// the projected token file when credentials expire.
	if r.awsLoadBalancerClients.ELB != nil && r.awsLoadBalancerClients.ELBV2 != nil {
		return nil
	}
	clientFactory := r.awsLoadBalancerClientFactory
	if clientFactory == nil {
		clientFactory = newAWSLoadBalancerClients
	}
	clients, err := clientFactory(ctx, hcp)
	if err != nil {
		return newSafeLoadBalancerCleanupError("failed to configure AWS load balancer clients", err)
	}
	r.awsLoadBalancerClients = clients
	return nil
}

func (r *reconciler) inspectAndPersistAWSLoadBalancerCandidates(ctx context.Context, state *awsLoadBalancerCleanupState) error {
	candidateNames := loadBalancerCandidateNames(state.awsProgress, state.awsRegion)
	state.candidateNames = make([]string, 0, len(candidateNames))
	for _, name := range candidateNames {
		if !state.classedCandidateNames.Has(name) {
			state.candidateNames = append(state.candidateNames, name)
		}
	}
	state.observations = map[string]awsutil.NamedLoadBalancerObservation{}
	if len(state.candidateNames) > 0 {
		observations, err := awsutil.InspectLoadBalancersByName(ctx, r.awsLoadBalancerClients, state.selector, state.candidateNames)
		if err != nil {
			return newSafeLoadBalancerCleanupError("failed to inspect AWS load balancer ownership", err)
		}
		state.observations = observations
	}
	for name, observation := range state.observations {
		for _, identity := range observation.Owned {
			for uid, proof := range state.awsProgress.Services {
				if proofHasLoadBalancerCandidate(proof, name, state.awsRegion) && mergeLoadBalancerIdentity(&proof, identity) {
					state.progressChanged = true
					state.progress.Services[uid] = proof
					state.awsProgress.Services[uid] = proof
				}
			}
		}
	}
	if state.progressChanged {
		if err := r.saveAWSLoadBalancerCleanupProgress(ctx, state.progressConfigMap, state.progressExists, state.progress); err != nil {
			return newSafeLoadBalancerCleanupError("failed to persist verified AWS identities; no AWS deletion was attempted", err)
		}
		state.progressExists = true
		state.progressChanged = false
	}
	state.idsByName = loadBalancerIdentitiesByName(state.awsProgress)
	return nil
}

func classifyAWSLoadBalancerCandidates(state *awsLoadBalancerCleanupState) (awsLoadBalancerCandidateCleanup, error) {
	cleanup := awsLoadBalancerCandidateCleanup{
		eligibleNames:       sets.New[string](),
		unverifiedNames:     sets.New[string](),
		resolvedAbsentNames: sets.New[string](),
	}
	cleanup.unverifiedNames.Insert(state.classedCandidateNames.UnsortedList()...)
	var inspectErrs []error
	for _, name := range state.candidateNames {
		observation := state.observations[name]
		if observation.Err != nil {
			if awsutil.IsOnlyDescribeTagsAccessDenied(observation.Err) {
				cleanup.describeTagsDenied = true
				cleanup.unverifiedNames.Insert(name)
				cleanup.unverifiedErrs = append(cleanup.unverifiedErrs, observation.Err)
			} else {
				inspectErrs = append(inspectErrs, observation.Err)
			}
			continue
		}
		if observation.Unverified || (observation.Present && len(observation.Owned) == 0) {
			cleanup.unverifiedNames.Insert(name)
			continue
		}
		if observation.Present || len(state.idsByName[name]) > 0 {
			cleanup.eligibleNames.Insert(name)
			continue
		}
		if loadBalancerNameReferencedByCurrentService(state.services, name, state.awsRegion) {
			cleanup.unverifiedNames.Insert(name)
			continue
		}
		cleanup.resolvedAbsentNames.Insert(name)
	}
	if len(inspectErrs) > 0 {
		return cleanup, newSafeLoadBalancerCleanupError("AWS load balancer ownership inspection failed; cleanup remains incomplete", errors.Join(inspectErrs...))
	}
	return cleanup, nil
}

func (r *reconciler) deleteEligibleAWSLoadBalancers(ctx context.Context, state *awsLoadBalancerCleanupState, cleanup awsLoadBalancerCandidateCleanup) (awsutil.LoadBalancerCleanupResult, error) {
	var identities []awsutil.LoadBalancerIdentity
	for name := range cleanup.eligibleNames {
		identities = append(identities, state.idsByName[name]...)
	}
	sortLoadBalancerIdentities(identities)
	result, err := awsutil.CleanupRecordedLoadBalancers(ctx, r.awsLoadBalancerClients, state.selector, identities, true, ctrl.LoggerFrom(ctx))
	if err != nil {
		return awsutil.LoadBalancerCleanupResult{}, newSafeLoadBalancerCleanupError("AWS load balancer cleanup could not be safely scoped", err)
	}
	return result, nil
}

func applyAWSLoadBalancerCleanupResult(cleanup *awsLoadBalancerCandidateCleanup, result awsutil.LoadBalancerCleanupResult) error {
	for name, nameResult := range result.ByName {
		if nameResult.Err == nil {
			continue
		}
		if awsutil.IsOnlyDescribeTagsAccessDenied(nameResult.Err) || errors.Is(nameResult.Err, awsutil.ErrLoadBalancerOwnershipUnverified) {
			cleanup.unverifiedNames.Insert(name)
			cleanup.eligibleNames.Delete(name)
			if awsutil.IsOnlyDescribeTagsAccessDenied(nameResult.Err) {
				cleanup.describeTagsDenied = true
			}
			cleanup.unverifiedErrs = append(cleanup.unverifiedErrs, nameResult.Err)
			continue
		}
		return newSafeLoadBalancerCleanupError("AWS load balancer deletion failed; resources may remain", nameResult.Err)
	}
	if result.OrphanTargetGroupErr != nil {
		if awsutil.IsOnlyDescribeTagsAccessDenied(result.OrphanTargetGroupErr) {
			cleanup.describeTagsDenied = true
			cleanup.orphanScanUnverified = true
			cleanup.unverifiedErrs = append(cleanup.unverifiedErrs, result.OrphanTargetGroupErr)
		} else {
			cleanup.orphanScanErr = result.OrphanTargetGroupErr
		}
	}
	return nil
}

func evaluateAWSLoadBalancerCleanup(state *awsLoadBalancerCleanupState, cleanup awsLoadBalancerCandidateCleanup, result awsutil.LoadBalancerCleanupResult) (map[string]bool, bool) {
	serviceCleanupComplete := make(map[string]bool, len(state.progress.Services))
	allCandidatesGone := result.OrphanTargetGroupScanSuccess
	for uid, proof := range state.progress.Services {
		complete := len(proof.Candidates) > 0 && !state.classedServiceUIDs.Has(uid)
		for _, candidate := range proof.Candidates {
			if candidate.Name == "" {
				_, servicePresent := state.currentServices[uid]
				if servicePresent || !proof.LoadBalancerCleanupFinalizerObserved {
					complete = false
				}
				continue
			}
			if candidate.Region != state.awsRegion || state.classedCandidateNames.Has(candidate.Name) {
				complete = false
				continue
			}
			if cleanup.resolvedAbsentNames.Has(candidate.Name) {
				if _, servicePresent := state.currentServices[uid]; servicePresent {
					complete = false
				}
				continue
			}
			if !cleanup.eligibleNames.Has(candidate.Name) || cleanup.unverifiedNames.Has(candidate.Name) || len(state.idsByName[candidate.Name]) == 0 {
				complete = false
				continue
			}
			nameResult, found := result.ByName[candidate.Name]
			if !found || nameResult.Err != nil || !nameResult.LoadBalancersRemoved {
				complete = false
			}
		}
		serviceCleanupComplete[uid] = complete
		allCandidatesGone = allCandidatesGone && complete
	}
	return serviceCleanupComplete, allCandidatesGone
}

func loadBalancerServiceDeletionPlan(state *awsLoadBalancerCleanupState, cleanup awsLoadBalancerCandidateCleanup, serviceCleanupComplete map[string]bool) (sets.Set[client.ObjectKey], sets.Set[client.ObjectKey], bool) {
	directDeleteKeys := sets.New[client.ObjectKey]()
	fallbackKeys := serviceKeysForLoadBalancerServices(state.services, nil, state.servicesWithLoadBalancerClass, state.servicesWithoutNames)
	fallbackNeeded := false
	for uid := range state.progress.Services {
		service, present := state.currentServices[uid]
		if !present || service.Spec.Type != corev1.ServiceTypeLoadBalancer {
			continue
		}
		if hasCustomLoadBalancerClass(*service) {
			fallbackKeys.Insert(client.ObjectKeyFromObject(service))
			fallbackNeeded = true
			continue
		}
		if serviceCleanupComplete[uid] && !hasUnresolvedCurrentLoadBalancerIngress(*service) {
			directDeleteKeys.Insert(client.ObjectKeyFromObject(service))
			fallbackKeys.Delete(client.ObjectKeyFromObject(service))
		} else {
			fallbackKeys.Insert(client.ObjectKeyFromObject(service))
			fallbackNeeded = true
		}
	}
	for name := range cleanup.unverifiedNames {
		for uid, proof := range state.progress.Services {
			if !proofHasLoadBalancerCandidate(proof, name, state.awsRegion) {
				continue
			}
			if service, present := state.currentServices[uid]; present {
				fallbackKeys.Insert(client.ObjectKeyFromObject(service))
				fallbackNeeded = true
			}
		}
	}
	if len(directDeleteKeys) > 0 {
		fallbackKeys = fallbackKeys.Difference(directDeleteKeys)
	}
	return fallbackKeys.Union(directDeleteKeys), fallbackKeys, fallbackNeeded
}

func awsLoadBalancerCleanupPendingMessage(cleanup awsLoadBalancerCandidateCleanup, result awsutil.LoadBalancerCleanupResult) string {
	message := "AWS load balancer ownership or deletion could not be verified; Kubernetes cleanup was requested and AWS resources may remain"
	if cleanup.orphanScanUnverified && cleanup.describeTagsDenied {
		return "AWS orphan target-group ownership verification was denied; the delegated role needs elasticloadbalancing:DescribeTags, and AWS resources may remain"
	}
	if cleanup.describeTagsDenied {
		return "AWS load balancer ownership verification was denied; the delegated role needs elasticloadbalancing:DescribeTags, Kubernetes cleanup was requested, and AWS resources may remain"
	}
	if cleanup.orphanScanUnverified {
		return "AWS orphan target-group ownership could not be verified; AWS resources may remain"
	}
	if !result.OrphanTargetGroupScanSuccess && len(cleanup.unverifiedNames) == 0 {
		return "AWS orphan target-group cleanup is not yet verified; AWS resources may remain"
	}
	if len(cleanup.unverifiedNames) > 0 {
		message = "AWS load balancer ownership could not be verified; Kubernetes cleanup was requested and AWS resources may remain"
	}
	return message
}

func awsLoadBalancerCleanupPendingCause(cleanup awsLoadBalancerCandidateCleanup) error {
	if len(cleanup.unverifiedNames) > 0 {
		cleanup.unverifiedErrs = append(cleanup.unverifiedErrs, awsutil.ErrLoadBalancerOwnershipUnverified)
	}
	return errors.Join(cleanup.unverifiedErrs...)
}

func loadBalancerNameReferencedByCurrentService(services []corev1.Service, name, region string) bool {
	for _, service := range services {
		for _, candidate := range loadBalancerCandidatesForService(service) {
			if candidate.Name == name && candidate.Region == region {
				return true
			}
		}
	}
	return false
}

func (r *reconciler) ensureAWSLoadBalancersRemovedWithHook(ctx context.Context, hcp *hyperv1.HostedControlPlane, services []corev1.Service) (bool, error) {
	_, servicesWithNames, servicesWithLoadBalancerClass, servicesWithoutNames := loadBalancerNamesFromServices(services)
	removed, cleanupErr := r.awsLoadBalancerCleanup(ctx, hcp)
	waitKeys := serviceKeysForLoadBalancerServices(services, nil, servicesWithLoadBalancerClass, servicesWithoutNames)
	deleteKeys := waitKeys
	if removed {
		deleteKeys = serviceKeysForLoadBalancerServices(services, servicesWithNames, servicesWithLoadBalancerClass, servicesWithoutNames)
	}
	pending, serviceErr := r.requestLoadBalancerServiceDeletion(ctx, deleteKeys, waitKeys)
	if err := errors.Join(cleanupErr, serviceErr); err != nil {
		return false, newSafeLoadBalancerCleanupError(safeCloudResourcesDestroyedErrorMessage(err), err)
	}
	return removed && !pending, nil
}

func loadBalancerCandidatesForService(service corev1.Service) []awsLoadBalancerCandidate {
	candidates := make([]awsLoadBalancerCandidate, 0, len(service.Status.LoadBalancer.Ingress))
	for _, ingress := range service.Status.LoadBalancer.Ingress {
		candidates = append(candidates, awsLoadBalancerCandidate{
			Hostname: ingress.Hostname,
			Name:     awsutil.LoadBalancerNameFromHostname(ingress.Hostname),
			Region:   awsutil.LoadBalancerRegionFromHostname(ingress.Hostname),
		})
	}
	return candidates
}

func hasKubernetesLoadBalancerCleanupFinalizer(service corev1.Service) bool {
	for _, finalizer := range service.Finalizers {
		if finalizer == kubernetesLoadBalancerCleanupFinalizer {
			return true
		}
	}
	return false
}

func hasCustomLoadBalancerClass(service corev1.Service) bool {
	return service.Spec.LoadBalancerClass != nil && *service.Spec.LoadBalancerClass != ""
}

func hasUnresolvedCurrentLoadBalancerIngress(service corev1.Service) bool {
	for _, ingress := range service.Status.LoadBalancer.Ingress {
		if awsutil.LoadBalancerNameFromHostname(ingress.Hostname) == "" {
			return true
		}
	}
	return false
}

func mergeLoadBalancerCandidate(proof *awsLoadBalancerServiceProof, candidate awsLoadBalancerCandidate) bool {
	for _, existing := range proof.Candidates {
		if existing == candidate {
			return false
		}
	}
	proof.Candidates = append(proof.Candidates, candidate)
	sort.Slice(proof.Candidates, func(i, j int) bool {
		if proof.Candidates[i].Hostname == proof.Candidates[j].Hostname {
			return proof.Candidates[i].Name < proof.Candidates[j].Name
		}
		return proof.Candidates[i].Hostname < proof.Candidates[j].Hostname
	})
	return true
}

func mergeLoadBalancerIdentity(proof *awsLoadBalancerServiceProof, identity awsutil.LoadBalancerIdentity) bool {
	for _, existing := range proof.Identities {
		if existing == identity {
			return false
		}
	}
	proof.Identities = append(proof.Identities, identity)
	sortLoadBalancerIdentities(proof.Identities)
	return true
}

func sortLoadBalancerIdentities(identities []awsutil.LoadBalancerIdentity) {
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].Name != identities[j].Name {
			return identities[i].Name < identities[j].Name
		}
		if identities[i].Type != identities[j].Type {
			return identities[i].Type < identities[j].Type
		}
		return identities[i].ARN < identities[j].ARN
	})
}

func loadBalancerCandidateNames(progress *awsLoadBalancerCleanupProgress, region string) []string {
	seen := sets.New[string]()
	for _, proof := range progress.Services {
		for _, candidate := range proof.Candidates {
			if candidate.Name != "" && candidate.Region == region {
				seen.Insert(candidate.Name)
			}
		}
	}
	names := seen.UnsortedList()
	sort.Strings(names)
	return names
}

func loadBalancerIdentitiesByName(progress *awsLoadBalancerCleanupProgress) map[string][]awsutil.LoadBalancerIdentity {
	byName := map[string][]awsutil.LoadBalancerIdentity{}
	for _, proof := range progress.Services {
		for _, identity := range proof.Identities {
			byName[identity.Name] = append(byName[identity.Name], identity)
		}
	}
	for name := range byName {
		sortLoadBalancerIdentities(byName[name])
	}
	return byName
}

func proofHasLoadBalancerCandidate(proof awsLoadBalancerServiceProof, name, region string) bool {
	for _, candidate := range proof.Candidates {
		if candidate.Name == name && candidate.Region == region {
			return true
		}
	}
	return false
}

func serviceKeysForLoadBalancerServices(services []corev1.Service, named, classed, unnamed sets.Set[client.ObjectKey]) sets.Set[client.ObjectKey] {
	keys := sets.New[client.ObjectKey]()
	for _, service := range services {
		key := client.ObjectKeyFromObject(&service)
		if named.Has(key) {
			keys.Insert(key)
		}
		if classed.Has(key) {
			keys.Insert(key)
		}
		if unnamed.Has(key) {
			keys.Insert(key)
		}
	}
	return keys
}

func (r *reconciler) requestLoadBalancerServiceDeletion(ctx context.Context, deleteKeys, waitKeys sets.Set[client.ObjectKey]) (bool, error) {
	if len(deleteKeys) > 0 {
		if _, err := cleanupResources(ctx, r.client, &corev1.ServiceList{}, func(obj client.Object) bool {
			return deleteKeys.Has(client.ObjectKeyFromObject(obj))
		}, false); err != nil {
			return false, err
		}
	}
	if len(waitKeys) == 0 {
		return false, nil
	}
	remaining := &corev1.ServiceList{}
	if err := r.client.List(ctx, remaining); err != nil {
		return false, err
	}
	pendingServiceCount := 0
	for i := range remaining.Items {
		if waitKeys.Has(client.ObjectKeyFromObject(&remaining.Items[i])) {
			pendingServiceCount++
		}
	}
	if pendingServiceCount > 0 {
		ctrl.LoggerFrom(ctx).Info("Waiting for Kubernetes load balancer Service deletion", "pendingServiceCount", pendingServiceCount)
	}
	return pendingServiceCount > 0, nil
}
