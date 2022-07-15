package ibmcloud

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"reflect"

	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/platform-services-go-sdk/iamidentityv1"
	"github.com/IBM/platform-services-go-sdk/iampolicymanagementv1"

	credreqv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	"github.com/openshift/cloud-credential-operator/pkg/ibmcloud"
)

const (
	manifestsDirName      = "manifests"
	secretFileNamePattern = "%s-%s-credentials.yaml"
)

var _ Provision = &ServiceID{}

type Provision interface {
	Validate() error

	Do() error
	UnDo(string) error

	List() ([]iamidentityv1.ServiceID, error)

	Refresh() error
	RemoveStaleKeys() error

	Dump(string) error

	Delete(bool) error
}

type ServiceID struct {
	ibmcloud.Client
	*iamidentityv1.ServiceID

	name            string
	accountID       string
	resourceGroupID string
	cr              *credreqv1.CredentialsRequest
	apiKey          *string
}

func (s *ServiceID) List() ([]iamidentityv1.ServiceID, error) {
	_, err := s.extractPolicies()
	if err != nil {
		return nil, err
	}

	options := &iamidentityv1.ListServiceIdsOptions{
		AccountID: &s.accountID,
		Name:      &s.name,
	}
	list, _, err := s.Client.ListServiceID(options)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to list the serviceIDs")
	}
	if len(list.Serviceids) >= 1 {
		return list.Serviceids, nil
	}
	return nil, nil
}

func (s *ServiceID) Validate() error {
	codec, err := credreqv1.NewCodec()
	if err != nil {
		return errors.Wrap(err, "Failed to create credReq codec")
	}

	var unknown runtime.Unknown
	err = codec.DecodeProviderSpec(s.cr.Spec.ProviderSpec, &unknown)
	if err != nil {
		return errors.Wrapf(err, "failed to DecodeProviderSpec")
	}

	if unknown.Kind != reflect.TypeOf(credreqv1.IBMCloudProviderSpec{}).Name() &&
		unknown.Kind != reflect.TypeOf(credreqv1.IBMCloudPowerVSProviderSpec{}).Name() {
		return fmt.Errorf("not supported of kind: %s", unknown.Kind)
	}

	list, err := s.List()
	if err != nil {
		return err
	}
	if len(list) != 0 {
		return errors.Errorf("exists with the same name: %s, please delete the entries or create with a different name", s.name)
	}
	return nil
}

func (s *ServiceID) Do() error {
	serviceIDOptions := &iamidentityv1.CreateServiceIDOptions{
		AccountID: &s.accountID,
		Name:      &s.name,
	}
	id, _, err := s.Client.CreateServiceID(serviceIDOptions)
	if err != nil {
		return err
	}
	s.ServiceID = id
	policies, err := s.extractPolicies()
	if err != nil {
		return errors.Wrapf(err, "Failed to extract the policies: %+v", err)
	}
	// Create a new Access Policy for each policy in the CredReq.
	for _, policy := range policies {
		err = s.createPolicy(&policy)
		if err != nil {
			return errors.Wrapf(err, "Failed to create access policy with: %+v", policy)
		}
	}

	if err := s.createAPIKey(); err != nil {
		return errors.Wrapf(err, "Failed to create an API Key for ServiceID Name: %s, ID: %s", *s.ServiceID.Name, *s.ServiceID.ID)
	}
	return nil
}

func (s *ServiceID) BuildSecret() (*corev1.Secret, error) {
	if s.apiKey == nil || s.cr == nil {
		return nil, errors.New("apiKey or credentialRequest can't be nil")
	}
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.cr.Spec.SecretRef.Name,
			Namespace: s.cr.Spec.SecretRef.Namespace,
		},
		StringData: map[string]string{
			//TODO(mkumatag): Remove the entry for ibmcloud_api_key once all the in-cluster components migrate to use the GetAuthenticatorFromEnvironment method
			"ibmcloud_api_key":    *s.apiKey,
			"ibm-credentials.env": fmt.Sprintf("IBMCLOUD_AUTHTYPE=iam\nIBMCLOUD_APIKEY=%s", *s.apiKey),
		},
		Type: corev1.SecretTypeOpaque,
	}, nil
}

func (s *ServiceID) Dump(targetDir string) error {
	if s.apiKey == nil || s.cr == nil {
		return errors.New("apiKey or credentialRequest can't be nil")
	}
	manifestsDir := filepath.Join(targetDir, manifestsDirName)

	fileName := fmt.Sprintf(secretFileNamePattern, s.cr.Spec.SecretRef.Namespace, s.cr.Spec.SecretRef.Name)
	filePath := filepath.Join(manifestsDir, fileName)

	secret, err := s.BuildSecret()
	if err != nil {
		return errors.Wrapf(err, "Failed to Dump the secret for the serviceID: %s", s.name)
	}
	data, err := yaml.Marshal(secret)
	if err != nil {
		return errors.Wrapf(err, "Failed to marshal the secret for the serviceID: %s", s.name)
	}

	if err := ioutil.WriteFile(filePath, data, 0600); err != nil {
		return errors.Wrap(err, "Failed to save Secret file")
	}

	log.Printf("Saved credentials configuration to: %s", filePath)

	return nil
}

func (s *ServiceID) createAPIKey() error {
	// Create a new API Key for the Service ID.
	apiKeyOptions := &iamidentityv1.CreateAPIKeyOptions{
		Name:  &APIKeyName,
		IamID: s.IamID,
	}
	apiKey, _, err := s.Client.CreateAPIKey(apiKeyOptions)
	if err != nil {
		return errors.Wrap(err, "Failed to create Service ID API key")
	}
	s.apiKey = apiKey.Apikey
	return nil
}

func (s *ServiceID) createPolicy(policy *credreqv1.AccessPolicy) error {
	// Construct the subjects with the newly created Service ID.
	subjects := []iampolicymanagementv1.PolicySubject{
		{
			Attributes: []iampolicymanagementv1.SubjectAttribute{
				{
					Name:  core.StringPtr("iam_id"),
					Value: core.StringPtr(*s.IamID),
				},
			},
		},
	}

	// Construct the access policy's roles.
	roles := make([]iampolicymanagementv1.PolicyRole, len(policy.Roles))
	for i, role := range policy.Roles {
		roles[i] = iampolicymanagementv1.PolicyRole{
			RoleID: core.StringPtr(role),
		}
	}

	// Construct the access policy's resource attributes.
	resourceAttributes := make([]iampolicymanagementv1.ResourceAttribute, len(policy.Attributes))
	for i, attr := range policy.Attributes {
		resourceAttributes[i] = iampolicymanagementv1.ResourceAttribute{
			Name:  core.StringPtr(attr.Name),
			Value: core.StringPtr(attr.Value),
		}
	}

	// Append the resource group attribute if specified as a command line argument.
	if s.resourceGroupID != "" {
		resourceGroupAttrName := "resourceGroupId"
		for _, attr := range resourceAttributes {
			if *attr.Name == "resourceType" && *attr.Value == "resource-group" {
				resourceGroupAttrName = "resource"
				break
			}
		}
		resourceAttributes = append(resourceAttributes, iampolicymanagementv1.ResourceAttribute{
			Name:  &resourceGroupAttrName,
			Value: &s.resourceGroupID,
		})
	}

	// Append the required accountId attribute.
	resourceAttributes = append(resourceAttributes, iampolicymanagementv1.ResourceAttribute{
		Name:  core.StringPtr("accountId"),
		Value: &s.accountID,
	})

	resources := []iampolicymanagementv1.PolicyResource{{
		Attributes: resourceAttributes,
	}}

	// Create the access policy.
	policyOptions := &iampolicymanagementv1.CreatePolicyOptions{
		Type:      core.StringPtr("access"),
		Subjects:  subjects,
		Roles:     roles,
		Resources: resources,
	}
	iamAccessPolicy, _, err := s.Client.CreatePolicy(policyOptions)
	if err != nil {
		return errors.Wrap(err, "Failed to create policy")
	}

	apJSON, _ := json.MarshalIndent(iamAccessPolicy, "", "  ")
	log.Printf("Created IAM Access Policy:\n%s", apJSON)

	return nil
}

func (s *ServiceID) extractPolicies() (policies []credreqv1.AccessPolicy, returnErr error) {
	codec, returnErr := credreqv1.NewCodec()
	if returnErr != nil {
		return nil, errors.Wrap(returnErr, "Failed to create credReq codec")
	}
	var unknown runtime.Unknown
	returnErr = codec.DecodeProviderSpec(s.cr.Spec.ProviderSpec, &unknown)
	if returnErr != nil {
		return nil, returnErr
	}

	switch unknown.Kind {
	case reflect.TypeOf(credreqv1.IBMCloudProviderSpec{}).Name():
		ibmcloudProviderSpec := &credreqv1.IBMCloudProviderSpec{}
		if err := codec.DecodeProviderSpec(s.cr.Spec.ProviderSpec, ibmcloudProviderSpec); err != nil {
			return nil, errors.Wrap(err, "Failed to decode the provider spec")
		}
		policies = ibmcloudProviderSpec.Policies
	case reflect.TypeOf(credreqv1.IBMCloudPowerVSProviderSpec{}).Name():
		ibmCloudPowerVSProviderSpec := &credreqv1.IBMCloudPowerVSProviderSpec{}
		if err := codec.DecodeProviderSpec(s.cr.Spec.ProviderSpec, ibmCloudPowerVSProviderSpec); err != nil {
			return nil, errors.Wrap(err, "Failed to decode the provider spec")
		}
		policies = ibmCloudPowerVSProviderSpec.Policies
	default:
		return nil, fmt.Errorf("not supported of kind: %s", unknown.Kind)
	}
	return
}

func (s *ServiceID) UnDo(targetDir string) error {
	if s.ServiceID == nil {
		return fmt.Errorf("no ServiceID present for: %s", s.name)
	}

	log.Printf("Deleting the ServiceID, Name:%s, ID: %s", *s.Name, *s.ID)
	options := &iamidentityv1.DeleteServiceIDOptions{
		ID: s.ID}
	_, err := s.Client.DeleteServiceID(options)
	if err != nil {
		log.Printf("Failed to delete the Service ID: %s", *s.ID)
	} else {
		log.Printf("Successfully deleted the Service ID: %s", *s.ID)
	}

	secretFileName := filepath.Join(targetDir, manifestsDirName, fmt.Sprintf(secretFileNamePattern, s.cr.Spec.SecretRef.Namespace, s.cr.Spec.SecretRef.Name))
	if _, err := os.Stat(secretFileName); err == nil {
		log.Printf("Deleting the generated secret, file:%s", secretFileName)
		err = os.Remove(secretFileName)
		if err != nil {
			log.Printf("failed to delete file: %s", secretFileName)
		}
	}

	return nil
}

func (s *ServiceID) Refresh() error {
	return s.createAPIKey()
}

func (s *ServiceID) RemoveStaleKeys() error {
	options := &iamidentityv1.ListAPIKeysOptions{
		IamID:     s.IamID,
		AccountID: s.AccountID,
		Sort:      core.StringPtr("created_at"),
	}
	keys, _, err := s.Client.ListAPIKeys(options)
	if err != nil {
		return errors.Wrap(err, "Failed to ListAPIKeys")
	}
	// remove all the stale keys except the latest one
	for i := 0; i < len(keys.Apikeys)-1; i++ {
		deleteOptions := &iamidentityv1.DeleteAPIKeyOptions{
			ID: keys.Apikeys[i].ID,
		}
		if _, err := s.Client.DeleteAPIKey(deleteOptions); err != nil {
			return errors.Wrap(err, "Failed to DeleteAPIKey")
		}
	}
	return nil
}

func (s *ServiceID) Delete(force bool) error {
	log.Printf("Deleting the service account with name: %s", s.name)
	start := ""
	var allrecs []iamidentityv1.ServiceID
	var pg int64 = 1
	for {
		listServiceIDOptions := iamidentityv1.ListServiceIdsOptions{
			AccountID: &s.accountID,
			Name:      &s.name,
			Pagesize:  &pg,
		}
		if start != "" {
			listServiceIDOptions.Pagetoken = &start
		}

		serviceIDs, _, err := s.Client.ListServiceID(&listServiceIDOptions)
		if err != nil {
			return errors.Wrap(err, "Error listing Service Ids")
		}
		start = getPageToken(serviceIDs.Next)
		allrecs = append(allrecs, serviceIDs.Serviceids...)
		if start == "" {
			break
		}
	}

	if len(allrecs) > 1 && !force {
		return fmt.Errorf("more than one ServiceIDs present with %s name, please run with --force flag to delete all the entries forcefully", s.name)
	} else if len(allrecs) == 0 {
		log.Printf("no ServiceID found with name: %s", s.name)
		return nil
	} else {
		if force {
			log.Printf("--force flag present, will delete all the entries with %s name forcefully", s.name)
		}
		for _, serviceID := range allrecs {
			log.Printf("deleting the ServiceID with name: %s, ID: %s", *serviceID.Name, *serviceID.ID)
			options := &iamidentityv1.DeleteServiceIDOptions{
				ID: serviceID.ID}
			if _, err := s.Client.DeleteServiceID(options); err != nil {
				return err
			}
		}
	}

	return nil
}

// getPageToken reads the pagetoken query from the URL
func getPageToken(next *string) string {
	if next == nil {
		return ""
	}
	u, err := url.Parse(*next)
	if err != nil {
		return ""
	}
	q := u.Query()
	return q.Get("pagetoken")
}

func NewServiceID(client ibmcloud.Client, prefix, accountID, resourceGroupID string, cr *credreqv1.CredentialsRequest) *ServiceID {
	return &ServiceID{
		Client:          client,
		name:            prefix + "-" + cr.Spec.SecretRef.Namespace + "-" + cr.Spec.SecretRef.Name,
		cr:              cr,
		accountID:       accountID,
		resourceGroupID: resourceGroupID,
	}
}
