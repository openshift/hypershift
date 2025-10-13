package manifestclient

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

type SerializedRequestish interface {
	GetSerializedRequest() *SerializedRequest
	SuggestedFilenames() (string, string, string)
	DeepCopy() SerializedRequestish
}

type FileOriginatedSerializedRequest struct {
	MetadataFilename string
	BodyFilename     string
	OptionsFilename  string

	SerializedRequest SerializedRequest
}

type TrackedSerializedRequest struct {
	RequestNumber int

	SerializedRequest SerializedRequest
}

type SerializedRequest struct {
	ActionMetadata
	KindType schema.GroupVersionKind

	Options []byte
	Body    []byte
}

func RequestsForResource[S ~[]E, E SerializedRequestish](mutations S, metadata ActionMetadata) []SerializedRequestish {
	ret := []SerializedRequestish{}
	for _, mutation := range mutations {
		if mutation.GetSerializedRequest().GetLookupMetadata() == metadata {
			ret = append(ret, mutation)
		}
	}
	return ret
}

// Difference returns a set of objects that are not in s2.
// For example:
// s1 = {a1, a2, a3}
// s2 = {a1, a2, a4, a5}
// s1.Difference(s2) = {a3}
// s2.Difference(s1) = {a4, a5}
func DifferenceOfSerializedRequests[S ~[]E, E SerializedRequestish, T ~[]F, F SerializedRequestish](lhs S, rhs T) S {
	ret := S{}

	for i, currLHS := range lhs {
		found := false
		for _, currRHS := range rhs {
			if EquivalentSerializedRequests(currLHS, currRHS) {
				found = true
				break
			}
		}
		if !found {
			ret = append(ret, lhs[i])
		}
	}
	return ret
}

func AreAllSerializedRequestsEquivalent[S ~[]E, E SerializedRequestish, T ~[]F, F SerializedRequestish](lhs S, rhs T) bool {
	if len(DifferenceOfSerializedRequests(lhs, rhs)) != 0 {
		return false
	}
	if len(DifferenceOfSerializedRequests(rhs, lhs)) != 0 {
		return false
	}
	return true
}

func AreAllSerializedRequestsEquivalentWithReasons[S ~[]E, E SerializedRequestish, T ~[]F, F SerializedRequestish](lhs S, rhs T) (bool, []SerializedRequest, []SerializedRequest) {
	missingInRHS := DifferenceOfSerializedRequests(lhs, rhs)
	missingInLHS := DifferenceOfSerializedRequests(rhs, lhs)

	if len(missingInRHS) == 0 && len(missingInLHS) == 0 {
		return true, nil, nil
	}

	missingInRHSAsSerializedRequest := []SerializedRequest{}
	missingInLHSAsSerializedRequest := []SerializedRequest{}
	for _, curr := range missingInRHS {
		missingInRHSAsSerializedRequest = append(missingInRHSAsSerializedRequest, *curr.GetSerializedRequest())
	}
	for _, curr := range missingInLHS {
		missingInLHSAsSerializedRequest = append(missingInLHSAsSerializedRequest, *curr.GetSerializedRequest())
	}

	return false, missingInRHSAsSerializedRequest, missingInLHSAsSerializedRequest
}

func EquivalentSerializedRequests(lhs, rhs SerializedRequestish) bool {
	return lhs.GetSerializedRequest().Equals(rhs.GetSerializedRequest())
}

func MakeFilenameGoModSafe(in string) string {
	// go mod doesn't like colons, so rename those.  We might theoretically conflict, but we shouldn't practically do so often
	return strings.Replace(in, ":", "-COLON-", -1)
}

func (lhs *FileOriginatedSerializedRequest) Equals(rhs *FileOriginatedSerializedRequest) bool {
	return CompareFileOriginatedSerializedRequest(lhs, rhs) == 0
}

func CompareFileOriginatedSerializedRequest(lhs, rhs *FileOriginatedSerializedRequest) int {
	switch {
	case lhs == nil && rhs == nil:
		return 0
	case lhs == nil && rhs != nil:
		return 1
	case lhs != nil && rhs == nil:
		return -1
	}

	if cmp := CompareSerializedRequest(&lhs.SerializedRequest, &rhs.SerializedRequest); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(lhs.MetadataFilename, rhs.MetadataFilename); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(lhs.BodyFilename, rhs.BodyFilename); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(lhs.OptionsFilename, rhs.OptionsFilename); cmp != 0 {
		return cmp
	}

	return 0
}

func (lhs *TrackedSerializedRequest) Equals(rhs *TrackedSerializedRequest) bool {
	return CompareTrackedSerializedRequest(lhs, rhs) == 0
}

func CompareTrackedSerializedRequest(lhs, rhs *TrackedSerializedRequest) int {
	switch {
	case lhs == nil && rhs == nil:
		return 0
	case lhs == nil && rhs != nil:
		return 1
	case lhs != nil && rhs == nil:
		return -1
	}

	if lhs.RequestNumber < rhs.RequestNumber {
		return -1
	} else if lhs.RequestNumber > rhs.RequestNumber {
		return 1
	}

	return CompareSerializedRequest(&lhs.SerializedRequest, &rhs.SerializedRequest)
}

func (lhs *SerializedRequest) Equals(rhs *SerializedRequest) bool {
	return CompareSerializedRequest(lhs, rhs) == 0
}

func CompareSerializedRequest(lhs, rhs *SerializedRequest) int {
	switch {
	case lhs == nil && rhs == nil:
		return 0
	case lhs == nil && rhs != nil:
		return 1
	case lhs != nil && rhs == nil:
		return -1
	}

	if cmp := strings.Compare(string(lhs.Action), string(rhs.Action)); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(lhs.PatchType, rhs.PatchType); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(lhs.FieldManager, rhs.FieldManager); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(lhs.ControllerInstanceName, rhs.ControllerInstanceName); cmp != 0 {
		return cmp
	}

	if cmp := strings.Compare(lhs.ResourceType.Group, rhs.ResourceType.Group); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(lhs.ResourceType.Version, rhs.ResourceType.Version); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(lhs.ResourceType.Resource, rhs.ResourceType.Resource); cmp != 0 {
		return cmp
	}

	if cmp := strings.Compare(lhs.KindType.Group, rhs.KindType.Group); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(lhs.KindType.Version, rhs.KindType.Version); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(lhs.KindType.Kind, rhs.KindType.Kind); cmp != 0 {
		return cmp
	}

	if cmp := strings.Compare(lhs.Namespace, rhs.Namespace); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(lhs.Name, rhs.Name); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(lhs.GenerateName, rhs.GenerateName); cmp != 0 {
		return cmp
	}

	if cmp := bytes.Compare(lhs.Body, rhs.Body); cmp != 0 {
		return cmp
	}
	if cmp := bytes.Compare(lhs.Options, rhs.Options); cmp != 0 {
		return cmp
	}

	return 0
}

func (a FileOriginatedSerializedRequest) GetSerializedRequest() *SerializedRequest {
	return &a.SerializedRequest
}

func (a TrackedSerializedRequest) GetSerializedRequest() *SerializedRequest {
	return &a.SerializedRequest
}

func (a SerializedRequest) GetSerializedRequest() *SerializedRequest {
	return &a
}

func (a FileOriginatedSerializedRequest) SuggestedFilenames() (string, string, string) {
	return a.MetadataFilename, a.BodyFilename, a.OptionsFilename
}

func (a TrackedSerializedRequest) SuggestedFilenames() (string, string, string) {
	return suggestedFilenames(a.SerializedRequest)
}

func (a SerializedRequest) SuggestedFilenames() (string, string, string) {
	return suggestedFilenames(a)
}

func suggestedFilenames(a SerializedRequest) (string, string, string) {
	bodyHash := hashRequestToPrefix(a.Body, a.Options)

	groupName := a.ResourceType.Group
	if len(groupName) == 0 {
		groupName = "core"
	}

	scopingString := ""
	if len(a.Namespace) > 0 {
		scopingString = filepath.Join("namespaces", a.Namespace)
	} else {
		scopingString = filepath.Join("cluster-scoped-resources")
	}

	metadataFilename := MakeFilenameGoModSafe(
		filepath.Join(
			string(a.Action),
			scopingString,
			groupName,
			a.ResourceType.Resource,
			fmt.Sprintf("%s-metadata-%s%s.yaml", bodyHash, a.Name, a.GenerateName),
		),
	)
	bodyFilename := MakeFilenameGoModSafe(
		filepath.Join(
			string(a.Action),
			scopingString,
			groupName,
			a.ResourceType.Resource,
			fmt.Sprintf("%s-body-%s%s.yaml", bodyHash, a.Name, a.GenerateName),
		),
	)
	optionsFilename := ""
	if len(a.Options) > 0 {
		optionsFilename = MakeFilenameGoModSafe(
			filepath.Join(
				string(a.Action),
				scopingString,
				groupName,
				a.ResourceType.Resource,
				fmt.Sprintf("%s-options-%s%s.yaml", bodyHash, a.Name, a.GenerateName),
			),
		)
	}
	return metadataFilename, bodyFilename, optionsFilename
}

func hashRequestToPrefix(data, options []byte) string {
	switch {
	case len(data) > 0:
		return hashForFilenamePrefix(data)
	case len(options) > 0:
		return hashForFilenamePrefix(options)
	default:
		return "MISSING"
	}
}

func hashForFilenamePrefix(data []byte) string {
	if len(data) == 0 {
		return "MISSING"
	}
	hash := sha256.New()
	hash.Write(data)
	hashBytes := hash.Sum(nil)

	// we're looking to deconflict filenames, not protect the crown jewels
	return fmt.Sprintf("%x", hashBytes[len(hashBytes)-2:])
}

func (a FileOriginatedSerializedRequest) DeepCopy() SerializedRequestish {
	return FileOriginatedSerializedRequest{
		MetadataFilename:  a.MetadataFilename,
		BodyFilename:      a.BodyFilename,
		OptionsFilename:   a.OptionsFilename,
		SerializedRequest: a.SerializedRequest.DeepCopy().(SerializedRequest),
	}
}

func (a TrackedSerializedRequest) DeepCopy() SerializedRequestish {
	return TrackedSerializedRequest{
		RequestNumber:     a.RequestNumber,
		SerializedRequest: a.SerializedRequest.DeepCopy().(SerializedRequest),
	}
}

func (a SerializedRequest) DeepCopy() SerializedRequestish {
	return SerializedRequest{
		ActionMetadata: ActionMetadata{
			Action: a.Action,
			ResourceMetadata: ResourceMetadata{
				ResourceType: a.ResourceType,
				Namespace:    a.Namespace,
				Name:         a.Name,
				GenerateName: a.GenerateName,
			},
			PatchType:              a.PatchType,
			FieldManager:           a.FieldManager,
			ControllerInstanceName: a.ControllerInstanceName,
		},
		KindType: a.KindType,
		Options:  bytes.Clone(a.Options),
		Body:     bytes.Clone(a.Body),
	}
}

func (a SerializedRequest) StringID() string {
	return fmt.Sprintf("%s-%s.%s.%s/%s%s[%s]", a.Action, a.KindType.Kind, a.KindType.Version, a.KindType.Group, a.Name, a.GenerateName, a.Namespace)
}

func (a SerializedRequest) GetLookupMetadata() ActionMetadata {
	return a.ActionMetadata
}
