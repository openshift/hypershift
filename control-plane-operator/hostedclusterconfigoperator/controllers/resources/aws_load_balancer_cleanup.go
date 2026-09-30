package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/support/awsutil"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	awsLoadBalancerProgressDataKey         = "progress.json"
	kubernetesLoadBalancerCleanupFinalizer = "service.kubernetes.io/load-balancer-cleanup"
)

type cloudResourceCleanupComponent string

const (
	cloudResourceCleanupComponentEligibility      cloudResourceCleanupComponent = "cleanup eligibility check"
	cloudResourceCleanupComponentResourceCreation cloudResourceCleanupComponent = "resource creation blocking"
	cloudResourceCleanupComponentImageRegistry    cloudResourceCleanupComponent = "image registry cleanup"
	cloudResourceCleanupComponentIngress          cloudResourceCleanupComponent = "ingress controller cleanup"
	cloudResourceCleanupComponentAWSLoadBalancers cloudResourceCleanupComponent = "AWS load balancer cleanup"
	cloudResourceCleanupComponentLoadBalancers    cloudResourceCleanupComponent = "load balancer cleanup"
	cloudResourceCleanupComponentPersistentVolume cloudResourceCleanupComponent = "persistent volume cleanup"
	cloudResourceCleanupComponentVolumeSnapshot   cloudResourceCleanupComponent = "volume snapshot cleanup"
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

type safeLoadBalancerCleanupError struct {
	message string
	err     error
}

func (e *safeLoadBalancerCleanupError) Error() string { return e.message }
func (e *safeLoadBalancerCleanupError) Unwrap() error { return e.err }

func newSafeLoadBalancerCleanupError(message string, err error) error {
	return &safeLoadBalancerCleanupError{message: message, err: err}
}

type cloudResourceCleanupComponentError struct {
	component cloudResourceCleanupComponent
	err       error
}

func (e *cloudResourceCleanupComponentError) Error() string {
	return fmt.Sprintf("%s failed", e.component)
}

func (e *cloudResourceCleanupComponentError) Unwrap() error { return e.err }

func withCloudResourceCleanupComponent(component cloudResourceCleanupComponent, err error) error {
	if err == nil {
		return nil
	}
	return &cloudResourceCleanupComponentError{component: component, err: err}
}

func cloudResourcesDestroyedErrorMessage(err error) string {
	return fmt.Sprintf("Error: %s", safeCloudResourcesDestroyedErrorMessage(err))
}

func safeCloudResourcesDestroyedErrorMessage(err error) string {
	summary := cloudResourceCleanupErrorSummary{components: map[cloudResourceCleanupComponent]struct{}{}}
	collectCloudResourceCleanupError(err, "", &summary)

	if len(summary.components) == 0 {
		if message, ok := safeLoadBalancerCleanupErrorMessage(err); ok {
			return message
		}
		if summary.awsDescribeTagsDenials > 0 {
			if summary.awsOtherFailures == 0 && summary.otherFailures == 0 {
				return describeTagsAccessDeniedCleanupMessage
			}
			return mixedDescribeTagsCleanupMessage
		}
		return genericCloudResourceCleanupMessage
	}

	components := make([]cloudResourceCleanupComponent, 0, len(summary.components))
	for component := range summary.components {
		components = append(components, component)
	}
	sort.Slice(components, func(i, j int) bool { return components[i] < components[j] })
	messages := make([]string, 0, len(components)+len(summary.safeMessages)+2)
	for _, component := range components {
		messages = appendUniqueCloudCleanupMessage(messages, fmt.Sprintf("%s failed", string(component)))
	}

	if summary.awsDescribeTagsDenials > 0 {
		if summary.awsOtherFailures == 0 {
			messages = appendUniqueCloudCleanupMessage(messages, describeTagsAccessDeniedCleanupMessage)
		} else {
			messages = appendUniqueCloudCleanupMessage(messages, mixedDescribeTagsCleanupMessage)
		}
	} else {
		for _, message := range summary.safeMessages {
			messages = appendUniqueCloudCleanupMessage(messages, message)
		}
	}

	if summary.otherFailures > 0 || (summary.awsOtherFailures > 0 && len(summary.safeMessages) == 0) || len(messages) == len(components) {
		messages = appendUniqueCloudCleanupMessage(messages, genericCloudResourceCleanupMessage)
	}
	return strings.Join(messages, "; ")
}

const (
	describeTagsAccessDeniedCleanupMessage = "AWS load balancer ownership verification was denied; the delegated role needs elasticloadbalancing:DescribeTags, Kubernetes cleanup was requested, and AWS resources may remain"
	mixedDescribeTagsCleanupMessage        = "AWS cleanup remains incomplete; the delegated role needs elasticloadbalancing:DescribeTags, and other cleanup failures or ownership issues may also be present"
	genericCloudResourceCleanupMessage     = "cloud resource cleanup failed; error details were omitted to avoid exposing sensitive resource or endpoint information"
)

type cloudResourceCleanupErrorSummary struct {
	components             map[cloudResourceCleanupComponent]struct{}
	safeMessages           []string
	awsDescribeTagsDenials int
	awsOtherFailures       int
	otherFailures          int
}

func collectCloudResourceCleanupError(err error, component cloudResourceCleanupComponent, summary *cloudResourceCleanupErrorSummary) {
	if err == nil {
		return
	}
	if aggregate, ok := err.(interface{ Errors() []error }); ok {
		children := aggregate.Errors()
		if len(children) == 0 {
			recordCloudResourceCleanupFailure(err, component, summary)
			return
		}
		for _, child := range children {
			collectCloudResourceCleanupError(child, component, summary)
		}
		return
	}
	if aggregate, ok := err.(interface{ Unwrap() []error }); ok {
		children := aggregate.Unwrap()
		if len(children) == 0 {
			recordCloudResourceCleanupFailure(err, component, summary)
			return
		}
		for _, child := range children {
			collectCloudResourceCleanupError(child, component, summary)
		}
		return
	}
	var componentErr *cloudResourceCleanupComponentError
	if errors.As(err, &componentErr) {
		summary.components[componentErr.component] = struct{}{}
		collectCloudResourceCleanupError(componentErr.err, componentErr.component, summary)
		return
	}
	var safeErr *safeLoadBalancerCleanupError
	if errors.As(err, &safeErr) {
		if component == cloudResourceCleanupComponentAWSLoadBalancers {
			summary.safeMessages = appendUniqueCloudCleanupMessage(summary.safeMessages, safeErr.message)
		}
		collectCloudResourceCleanupError(safeErr.err, component, summary)
		return
	}
	if (component == cloudResourceCleanupComponentAWSLoadBalancers || component == "") && awsutil.IsOnlyDescribeTagsAccessDenied(err) {
		summary.awsDescribeTagsDenials++
		return
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok && wrapped.Unwrap() != nil {
		collectCloudResourceCleanupError(wrapped.Unwrap(), component, summary)
		return
	}
	recordCloudResourceCleanupFailure(err, component, summary)
}

func recordCloudResourceCleanupFailure(err error, component cloudResourceCleanupComponent, summary *cloudResourceCleanupErrorSummary) {
	if component == cloudResourceCleanupComponentAWSLoadBalancers && (awsutil.IsOnlyDescribeTagsAccessDenied(err) || errors.Is(err, awsutil.ErrDescribeTagsAccessDenied)) {
		summary.awsDescribeTagsDenials++
		return
	}
	if component == "" && (awsutil.IsOnlyDescribeTagsAccessDenied(err) || errors.Is(err, awsutil.ErrDescribeTagsAccessDenied)) {
		summary.awsDescribeTagsDenials++
		return
	}
	if component == cloudResourceCleanupComponentAWSLoadBalancers {
		if errors.Is(err, awsutil.ErrLoadBalancerOwnershipUnverified) {
			// Ownership failures are surfaced as pending cleanup by the candidate-verification path.
			return
		}
		summary.awsOtherFailures++
		return
	}
	summary.otherFailures++
}

func appendUniqueCloudCleanupMessage(messages []string, message string) []string {
	for _, existing := range messages {
		if existing == message {
			return messages
		}
	}
	return append(messages, message)
}

func safeLoadBalancerCleanupErrorMessage(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	if aggregate, ok := err.(interface{ Errors() []error }); ok {
		children := aggregate.Errors()
		if len(children) != 1 {
			return "", false
		}
		return safeLoadBalancerCleanupErrorMessage(children[0])
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		children := multi.Unwrap()
		if len(children) != 1 {
			return "", false
		}
		return safeLoadBalancerCleanupErrorMessage(children[0])
	}
	var safeErr *safeLoadBalancerCleanupError
	if errors.As(err, &safeErr) {
		return safeErr.message, true
	}
	if single, ok := err.(interface{ Unwrap() error }); ok {
		return safeLoadBalancerCleanupErrorMessage(single.Unwrap())
	}
	return "", false
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
		if !isNonIngressLoadBalancerService(*service) || hasCustomLoadBalancerClass(*service) {
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
		if !present || !isNonIngressLoadBalancerService(*service) {
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

func (r *reconciler) retireCompletedAWSLoadBalancerCleanupProgress(ctx context.Context, state *awsLoadBalancerCleanupState, result awsutil.LoadBalancerCleanupResult, serviceCleanupComplete map[string]bool) error {
	if !result.OrphanTargetGroupScanSuccess {
		return nil
	}
	for uid := range state.progress.Services {
		if _, present := state.currentServices[uid]; !present && serviceCleanupComplete[uid] {
			delete(state.progress.Services, uid)
			state.progressChanged = true
		}
	}
	if !state.progressChanged {
		return nil
	}
	if len(state.progress.Services) == 0 {
		if state.progressExists {
			if err := r.cpClient.Delete(ctx, state.progressConfigMap); err != nil && !apierrors.IsNotFound(err) {
				return newSafeLoadBalancerCleanupError("failed to retire completed AWS load balancer cleanup progress", err)
			}
		}
		return nil
	}
	if err := r.saveAWSLoadBalancerCleanupProgress(ctx, state.progressConfigMap, state.progressExists, state.progress); err != nil {
		return newSafeLoadBalancerCleanupError("failed to update AWS load balancer cleanup progress", err)
	}
	return nil
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

func (r *reconciler) loadAWSLoadBalancerCleanupProgress(ctx context.Context, hcp *hyperv1.HostedControlPlane) (*awsLoadBalancerCleanupProgress, *corev1.ConfigMap, bool, error) {
	if hcp.UID == "" {
		return nil, nil, false, newSafeLoadBalancerCleanupError("HostedControlPlane UID is required to load AWS load balancer cleanup progress", nil)
	}
	if r.cpClient == nil {
		return nil, nil, false, newSafeLoadBalancerCleanupError("management-cluster client is unavailable for AWS load balancer cleanup progress", nil)
	}
	progressReader := r.cpAPIReader
	if progressReader == nil {
		progressReader = r.cpClient
	}
	configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: awsLoadBalancerProgressConfigMapName(hcp.UID)}}
	if err := progressReader.Get(ctx, client.ObjectKeyFromObject(configMap), configMap); err != nil {
		if apierrors.IsNotFound(err) {
			region := ""
			if hcp.Spec.Platform.AWS != nil {
				region = hcp.Spec.Platform.AWS.Region
			}
			return &awsLoadBalancerCleanupProgress{HCPUID: hcp.UID, InfraID: hcp.Spec.InfraID, Region: region, Services: map[string]awsLoadBalancerServiceProof{}}, configMap, false, nil
		}
		return nil, nil, false, newSafeLoadBalancerCleanupError("failed to read AWS load balancer cleanup progress from management cluster", err)
	}
	serialized := configMap.Data[awsLoadBalancerProgressDataKey]
	progress := &awsLoadBalancerCleanupProgress{}
	if serialized == "" {
		return nil, nil, false, newSafeLoadBalancerCleanupError(fmt.Sprintf("progress ConfigMap has no data for key %q", awsLoadBalancerProgressDataKey), nil)
	}
	if err := json.Unmarshal([]byte(serialized), progress); err != nil {
		return nil, nil, false, newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress is invalid", fmt.Errorf("failed to unmarshal progress ConfigMap data: %w", err))
	}
	region := ""
	if hcp.Spec.Platform.AWS != nil {
		region = hcp.Spec.Platform.AWS.Region
	}
	if progress.HCPUID != hcp.UID {
		return nil, nil, false, newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress UID does not match the HostedControlPlane", nil)
	}
	if progress.InfraID != hcp.Spec.InfraID {
		return nil, nil, false, newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress InfraID does not match the HostedControlPlane", nil)
	}
	if progress.Region != "" && progress.Region != region {
		return nil, nil, false, newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress AWS region does not match the HostedControlPlane", nil)
	}
	if progress.VPCID != "" && hcp.Spec.Platform.AWS != nil && hcp.Spec.Platform.AWS.CloudProviderConfig != nil && progress.VPCID != hcp.Spec.Platform.AWS.CloudProviderConfig.VPC {
		return nil, nil, false, newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress VPC ID does not match the HostedControlPlane", nil)
	}
	if progress.Services == nil {
		progress.Services = map[string]awsLoadBalancerServiceProof{}
	}
	return progress, configMap, true, nil
}

func ensureAWSLoadBalancerProgressOwnerReference(configMap *corev1.ConfigMap, hcp *hyperv1.HostedControlPlane) (bool, error) {
	if len(configMap.OwnerReferences) > 1 || (len(configMap.OwnerReferences) == 1 && configMap.OwnerReferences[0].UID != hcp.UID) {
		return false, newSafeLoadBalancerCleanupError("AWS load balancer cleanup progress ConfigMap has an unexpected owner", nil)
	}

	controller := true
	blockOwnerDeletion := false
	expected := metav1.OwnerReference{
		APIVersion:         hyperv1.GroupVersion.String(),
		Kind:               "HostedControlPlane",
		Name:               hcp.Name,
		UID:                hcp.UID,
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}
	if len(configMap.OwnerReferences) == 1 {
		current := configMap.OwnerReferences[0]
		if current.APIVersion == expected.APIVersion && current.Kind == expected.Kind && current.Name == expected.Name && current.UID == expected.UID && current.Controller != nil && *current.Controller && current.BlockOwnerDeletion != nil && !*current.BlockOwnerDeletion {
			return false, nil
		}
	}
	configMap.OwnerReferences = []metav1.OwnerReference{expected}
	return true, nil
}

func (r *reconciler) saveAWSLoadBalancerCleanupProgress(ctx context.Context, configMap *corev1.ConfigMap, exists bool, progress *awsLoadBalancerCleanupProgress) error {
	serialized, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("failed to serialize AWS load balancer cleanup progress: %w", err)
	}
	if configMap.Data == nil {
		configMap.Data = map[string]string{}
	}
	configMap.Data[awsLoadBalancerProgressDataKey] = string(serialized)
	if exists {
		return r.cpClient.Update(ctx, configMap)
	}
	return r.cpClient.Create(ctx, configMap)
}

func awsLoadBalancerProgressConfigMapName(uid types.UID) string {
	digest := sha256.Sum256([]byte(uid))
	return "aws-lb-cleanup-" + hex.EncodeToString(digest[:12])
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
	for i := range remaining.Items {
		if waitKeys.Has(client.ObjectKeyFromObject(&remaining.Items[i])) {
			return true, nil
		}
	}
	return false, nil
}
