package libraryapplyconfiguration

import (
	"bytes"
	"fmt"
	"github.com/google/go-cmp/cmp"
	"github.com/openshift/library-go/pkg/manifestclient"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/yaml"
	"reflect"
)

func EquivalentApplyConfigurationResultIgnoringEvents(lhs, rhs ApplyConfigurationResult) []string {
	reasons := []string{}
	reasons = append(reasons, equivalentErrors("Error", lhs.Error(), rhs.Error())...)
	reasons = append(reasons, equivalentRunResults("ControllerResults", lhs.ControllerResults(), rhs.ControllerResults())...)

	for _, clusterType := range sets.List(AllClusterTypes) {
		currLHS := lhs.MutationsForClusterType(clusterType)
		currRHS := rhs.MutationsForClusterType(clusterType)
		reasons = append(reasons, EquivalentClusterApplyResultIgnoringEvents(string(clusterType), currLHS, currRHS)...)
	}

	return reasons
}

func equivalentErrors(field string, lhs, rhs error) []string {
	reasons := []string{}
	switch {
	case lhs == nil && rhs == nil:
	case lhs == nil && rhs != nil:
		reasons = append(reasons, fmt.Sprintf("%v: lhs=nil, rhs=%v", field, rhs))
	case lhs != nil && rhs == nil:
		reasons = append(reasons, fmt.Sprintf("%v: lhs=%v, rhs=nil", field, lhs))
	case lhs.Error() != rhs.Error():
		reasons = append(reasons, fmt.Sprintf("%v: lhs=%v, rhs=%v", field, lhs, rhs))
	}

	return reasons
}

func equivalentRunResults(field string, lhs, rhs *ApplyConfigurationRunResult) []string {
	reasons := []string{}
	switch {
	case lhs == nil && rhs == nil:
	case lhs == nil && rhs != nil:
		reasons = append(reasons, fmt.Sprintf("%v: lhs=nil, rhs=%v", field, rhs))
	case lhs != nil && rhs == nil:
		reasons = append(reasons, fmt.Sprintf("%v: lhs=%v, rhs=nil", field, lhs))
	default:
		if !reflect.DeepEqual(lhs, rhs) {
			reasons = append(reasons, fmt.Sprintf("%v: diff: %v", field, cmp.Diff(lhs, rhs)))
		}
	}

	return reasons
}

func EquivalentClusterApplyResultIgnoringEvents(field string, lhs, rhs SingleClusterDesiredMutationGetter) []string {
	switch {
	case lhs == nil && rhs == nil:
		return nil
	case lhs == nil && rhs != nil:
		return []string{fmt.Sprintf("%v: lhs=nil, len(rhs)=%v", field, len(rhs.Requests().AllRequests()))}
	case lhs != nil && rhs == nil:
		return []string{fmt.Sprintf("%v: len(lhs)=%v, rhs=nil", field, len(lhs.Requests().AllRequests()))}
	case lhs != nil && rhs != nil:
		// check the rest
	}

	lhsAllRequests := RemoveEvents(lhs.Requests().AllRequests())
	rhsAllRequests := RemoveEvents(rhs.Requests().AllRequests())

	// TODO different method with prettier message
	equivalent, missingInRHS, missingInLHS := manifestclient.AreAllSerializedRequestsEquivalentWithReasons(lhsAllRequests, rhsAllRequests)
	if equivalent {
		return nil
	}

	reasons := []string{}
	reasons = append(reasons, reasonForDiff("rhs", missingInRHS, rhsAllRequests)...)

	uniquelyMissingInLHS := []manifestclient.SerializedRequest{}
	for _, currMissingInLHS := range missingInLHS {
		lhsMetadata := expandedMetadataFor(currMissingInLHS.GetSerializedRequest())
		found := false
		for _, currMissingInRHS := range missingInRHS {
			rhsMetadata := expandedMetadataFor(currMissingInRHS.GetSerializedRequest())
			if lhsMetadata == rhsMetadata {
				found = true
				break
			}
		}
		if !found {
			uniquelyMissingInLHS = append(uniquelyMissingInLHS, currMissingInLHS)
		}
	}
	reasons = append(reasons, reasonForDiff("lhs", uniquelyMissingInLHS, lhsAllRequests)...)

	qualifiedReasons := []string{}
	for _, curr := range reasons {
		qualifiedReasons = append(qualifiedReasons, fmt.Sprintf("%s: %s", field, curr))
	}
	return qualifiedReasons
}

// expandedMetadata is useful for describing diffs, potentially to get pushed into manifestclient
type expandedMetadata struct {
	metadata               manifestclient.ActionMetadata
	fieldManager           string
	controllerInstanceName string
}

func expandedMetadataFor(serializedRequest *manifestclient.SerializedRequest) expandedMetadata {
	if serializedRequest == nil {
		return expandedMetadata{}
	}
	metadata := serializedRequest.GetLookupMetadata()
	fieldManager := ""
	controllerInstanceName := ""

	isApply := serializedRequest.Action == manifestclient.ActionApply || serializedRequest.Action == manifestclient.ActionApplyStatus
	if isApply {
		lhsOptions := &metav1.ApplyOptions{}
		if err := yaml.Unmarshal(serializedRequest.Options, lhsOptions); err == nil {
			// ignore err.  if it doesn't work we get the zero value and that's ok
			fieldManager = lhsOptions.FieldManager
		}
	}

	bodyObj := &unstructured.Unstructured{
		Object: map[string]interface{}{},
	}
	if err := yaml.Unmarshal(serializedRequest.Body, &bodyObj.Object); err == nil {
		// ignore err.  if it doesn't work we get the zero value and that's ok
		if bodyObj != nil && len(bodyObj.GetAnnotations()["synthetic.mom.openshift.io/controller-instance-name"]) > 0 {
			controllerInstanceName = bodyObj.GetAnnotations()["synthetic.mom.openshift.io/controller-instance-name"]
		}
		if bodyObj != nil && len(bodyObj.GetAnnotations()["operator.openshift.io/controller-instance-name"]) > 0 {
			controllerInstanceName = bodyObj.GetAnnotations()["operator.openshift.io/controller-instance-name"]
		}
	}

	return expandedMetadata{
		metadata:               metadata,
		fieldManager:           fieldManager,
		controllerInstanceName: controllerInstanceName,
	}
}

func reasonForDiff(nameOfDestination string, sourceRequestsToCheck []manifestclient.SerializedRequest, allDestinationRequests []manifestclient.SerializedRequestish) []string {
	reasons := []string{}

	for _, currSourceRequest := range sourceRequestsToCheck {
		currDestinationRequests := manifestclient.RequestsForResource(allDestinationRequests, currSourceRequest.GetLookupMetadata())

		if len(currDestinationRequests) == 0 {
			reasons = append(reasons, fmt.Sprintf("%s is missing: %v", nameOfDestination, currSourceRequest.StringID()))
			continue
		}

		isApply := currSourceRequest.GetSerializedRequest().Action == manifestclient.ActionApply || currSourceRequest.GetSerializedRequest().Action == manifestclient.ActionApplyStatus
		lhsMetadata := expandedMetadataFor(currSourceRequest.GetSerializedRequest())

		found := false
		mismatchReasons := []string{}
		for i, currDestinationRequest := range currDestinationRequests {
			if manifestclient.EquivalentSerializedRequests(currSourceRequest, currDestinationRequest) {
				found = true
				mismatchReasons = nil
				break
			}
			// if we're doing an apply and the field manager doesn't match, then it's just a case of "content isn't here" versus a diff
			// actions match because the metadata (which contains action) matched
			if isApply {
				lhsOptions := currSourceRequest.GetSerializedRequest().Options
				rhsOptions := currDestinationRequest.GetSerializedRequest().Options
				if !bytes.Equal(lhsOptions, rhsOptions) {
					// if the options for apply (which contains the field manager) aren't the same, then the requests
					// are logically different requests and incomparable
					continue
				}
			}

			// we know the metadata is the same, something else doesn't match
			if !bytes.Equal(currSourceRequest.GetSerializedRequest().Body, currDestinationRequest.GetSerializedRequest().Body) {
				mismatchReasons = append(mismatchReasons,
					fmt.Sprintf("mutation: %v, fieldManager=%v, controllerInstanceName=%v, %v[%d]: body diff: %v",
						currSourceRequest.GetSerializedRequest().StringID(),
						lhsMetadata.fieldManager,
						lhsMetadata.controllerInstanceName,
						nameOfDestination,
						i,
						cmp.Diff(currSourceRequest.GetSerializedRequest().Body, currDestinationRequest.GetSerializedRequest().Body),
					),
				)
			}
			if !bytes.Equal(currSourceRequest.GetSerializedRequest().Options, currDestinationRequest.GetSerializedRequest().Options) {
				mismatchReasons = append(mismatchReasons,
					fmt.Sprintf("mutation: %v, fieldManager=%v, controllerInstanceName=%v, %v[%d]: options diff: %v",
						currSourceRequest.GetSerializedRequest().StringID(),
						lhsMetadata.fieldManager,
						lhsMetadata.controllerInstanceName,
						nameOfDestination,
						i,
						cmp.Diff(currSourceRequest.GetSerializedRequest().Options, currDestinationRequest.GetSerializedRequest().Options),
					),
				)
			}
		}
		if found {
			continue
		}
		if !found && len(mismatchReasons) == 0 {
			mismatchReasons = append(mismatchReasons, fmt.Sprintf("%s is missing equivalent request for fieldManager=%v controllerInstanceName=%v: %v", nameOfDestination, lhsMetadata.fieldManager, lhsMetadata.controllerInstanceName, currSourceRequest.StringID()))
		}
		reasons = append(reasons, mismatchReasons...)
	}
	return reasons
}
