package awsutil

import (
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// FindResourceTagByKey searches for a resource tag with the specified key in the given slice of AWSResourceTags.
// Returns the tag if found, nil otherwise.
func FindResourceTagByKey(tags []hyperv1.AWSResourceTag, key string) *hyperv1.AWSResourceTag {
	for i := range tags {
		if tags[i].Key == key {
			return &tags[i]
		}
	}
	return nil
}

// HasResourceTagWithValue checks if a resource tag exists with the specified key and value.
// Returns true if the tag exists with the exact key-value pair, false otherwise.
func HasResourceTagWithValue(tags []hyperv1.AWSResourceTag, key, value string) bool {
	tag := FindResourceTagByKey(tags, key)
	return tag != nil && tag.Value == value
}

// GetResourceTagValue retrieves the value of a resource tag with the specified key.
// Returns the value if found, empty string otherwise.
func GetResourceTagValue(tags []hyperv1.AWSResourceTag, key string) string {
	tag := FindResourceTagByKey(tags, key)
	if tag != nil {
		return tag.Value
	}
	return ""
}

// IsROSAHCPFromTags checks if the cluster is a ROSA HCP (Red Hat OpenShift Service on AWS Hosted Control Plane)
// by looking for the specific tag combination.
func IsROSAHCPFromTags(tags []hyperv1.AWSResourceTag) bool {
	return HasResourceTagWithValue(tags, "red-hat-clustertype", "rosa")
}

// IsROSAHCP checks if the cluster is a ROSA HCP (Red Hat OpenShift Service on AWS Hosted Control Plane)
// by looking for the MANAGED_SERVICE key to match the ROSA-HCP value.
func IsROSAHCP() bool {
	return os.Getenv("MANAGED_SERVICE") == hyperv1.RosaHCP
}
