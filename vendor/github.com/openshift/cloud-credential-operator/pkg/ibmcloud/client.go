package ibmcloud

import (
	"fmt"
	"strings"

	"github.com/IBM/go-sdk-core/v5/core"
	identityv1 "github.com/IBM/platform-services-go-sdk/iamidentityv1"
	pmv1 "github.com/IBM/platform-services-go-sdk/iampolicymanagementv1"
	"github.com/IBM/platform-services-go-sdk/resourcemanagerv2"
)

//go:generate mockgen -source=./client.go -destination=./mock/client_generated.go -package=mock

// Client is a wrapper object for actual IBMCloud SDK clients to allow for easier testing.
type Client interface {
	CreatePolicy(*pmv1.CreatePolicyOptions) (*pmv1.Policy, *core.DetailedResponse, error)
	CreateServiceID(*identityv1.CreateServiceIDOptions) (*identityv1.ServiceID, *core.DetailedResponse, error)
	ListServiceID(*identityv1.ListServiceIdsOptions) (*identityv1.ServiceIDList, *core.DetailedResponse, error)
	DeleteServiceID(*identityv1.DeleteServiceIDOptions) (*core.DetailedResponse, error)
	CreateAPIKey(*identityv1.CreateAPIKeyOptions) (*identityv1.APIKey, *core.DetailedResponse, error)
	ListAPIKeys(*identityv1.ListAPIKeysOptions) (*identityv1.APIKeyList, *core.DetailedResponse, error)
	DeleteAPIKey(*identityv1.DeleteAPIKeyOptions) (*core.DetailedResponse, error)
	GetAPIKeysDetails(*identityv1.GetAPIKeysDetailsOptions) (*identityv1.APIKey, *core.DetailedResponse, error)
	NewGetAPIKeysDetailsOptions() *identityv1.GetAPIKeysDetailsOptions
	ListResourceGroups(*resourcemanagerv2.ListResourceGroupsOptions) (*resourcemanagerv2.ResourceGroupList, *core.DetailedResponse, error)
}

type ClientParams struct {
	InfraName string
	// APIEndpoint is the base domain of the IBM Cloud API to target (e.g. "test.cloud.ibm.com").
	// When empty, the SDK defaults (production cloud.ibm.com endpoints) are used.
	APIEndpoint string
}

type ibmcloudClient struct {
	authenticator           *core.IamAuthenticator
	pmClient                *pmv1.IamPolicyManagementV1
	clientID                string
	identityClient          *identityv1.IamIdentityV1
	resourceManagerV2Client *resourcemanagerv2.ResourceManagerV2
}

func (i *ibmcloudClient) CreateAPIKey(options *identityv1.CreateAPIKeyOptions) (*identityv1.APIKey, *core.DetailedResponse, error) {
	return i.identityClient.CreateAPIKey(options)
}

func (i *ibmcloudClient) ListAPIKeys(options *identityv1.ListAPIKeysOptions) (*identityv1.APIKeyList, *core.DetailedResponse, error) {
	return i.identityClient.ListAPIKeys(options)
}

func (i *ibmcloudClient) DeleteAPIKey(options *identityv1.DeleteAPIKeyOptions) (*core.DetailedResponse, error) {
	return i.identityClient.DeleteAPIKey(options)
}

func (i *ibmcloudClient) NewGetAPIKeysDetailsOptions() *identityv1.GetAPIKeysDetailsOptions {
	return i.identityClient.NewGetAPIKeysDetailsOptions()
}

func (i *ibmcloudClient) ListResourceGroups(options *resourcemanagerv2.ListResourceGroupsOptions) (*resourcemanagerv2.ResourceGroupList, *core.DetailedResponse, error) {
	return i.resourceManagerV2Client.ListResourceGroups(options)
}

func (i *ibmcloudClient) GetAPIKeysDetails(options *identityv1.GetAPIKeysDetailsOptions) (*identityv1.APIKey, *core.DetailedResponse, error) {
	return i.identityClient.GetAPIKeysDetails(options)
}

func (i *ibmcloudClient) CreateServiceID(options *identityv1.CreateServiceIDOptions) (*identityv1.ServiceID, *core.DetailedResponse, error) {
	return i.identityClient.CreateServiceID(options)
}

func (i *ibmcloudClient) ListServiceID(options *identityv1.ListServiceIdsOptions) (*identityv1.ServiceIDList, *core.DetailedResponse, error) {
	return i.identityClient.ListServiceIds(options)
}

func (i *ibmcloudClient) DeleteServiceID(options *identityv1.DeleteServiceIDOptions) (*core.DetailedResponse, error) {
	return i.identityClient.DeleteServiceID(options)
}

func (i *ibmcloudClient) CreatePolicy(options *pmv1.CreatePolicyOptions) (*pmv1.Policy, *core.DetailedResponse, error) {
	return i.pmClient.CreatePolicy(options)
}

// validateAPIEndpoint ensures the provided API endpoint is a bare hostname/domain with no
// scheme or trailing slash (e.g. "test.cloud.ibm.com"), returns a cleaned value or an error.
func validateAPIEndpoint(endpoint string) (string, error) {
	// Strip a scheme the user may have accidentally included.
	for _, scheme := range []string{"https://", "http://"} {
		endpoint = strings.TrimPrefix(endpoint, scheme)
	}
	// Strip trailing slashes.
	endpoint = strings.TrimRight(endpoint, "/")

	// After stripping, the value must not still contain "://"
	if strings.Contains(endpoint, "://") {
		return "", fmt.Errorf("invalid api-endpoint %q: must be a bare domain (e.g. test.cloud.ibm.com), not a full URL", endpoint)
	}
	return endpoint, nil
}

func NewClient(apiKey string, params *ClientParams) (Client, error) {
	authenticator := &core.IamAuthenticator{
		ApiKey: apiKey,
	}

	agentText := "defaultAgent"
	var iamURL, resourceManagerURL string
	if params != nil {
		if params.InfraName != "" {
			agentText = params.InfraName
		}
		if params.APIEndpoint != "" {
			endpoint, err := validateAPIEndpoint(params.APIEndpoint)
			if err != nil {
				return nil, err
			}
			// Both the IAM Identity and IAM Policy Management APIs are served from the same iam.* host
			iamURL = fmt.Sprintf("https://iam.%s", endpoint)
			resourceManagerURL = fmt.Sprintf("https://resource-controller.%s", endpoint)
		}
	}

	if iamURL != "" {
		authenticator.URL = iamURL
	}

	err := authenticator.Validate()
	if err != nil {
		return nil, err
	}

	userAgentString := fmt.Sprintf("OpenShift/4.x Cloud Credential Operator: %s", agentText)

	serviceClientOptions := &pmv1.IamPolicyManagementV1Options{
		Authenticator: authenticator,
	}
	if iamURL != "" {
		serviceClientOptions.URL = iamURL
	}
	serviceClient, err := pmv1.NewIamPolicyManagementV1(serviceClientOptions)
	if err != nil {
		return nil, err
	}
	serviceClient.Service.SetUserAgent(userAgentString)

	identityv1Options := &identityv1.IamIdentityV1Options{
		Authenticator: authenticator,
	}
	if iamURL != "" {
		identityv1Options.URL = iamURL
	}
	identityClient, err := identityv1.NewIamIdentityV1(identityv1Options)
	if err != nil {
		return nil, err
	}
	identityClient.Service.SetUserAgent(userAgentString)

	resourceManagerV2Options := &resourcemanagerv2.ResourceManagerV2Options{
		Authenticator: authenticator,
	}
	if resourceManagerURL != "" {
		resourceManagerV2Options.URL = resourceManagerURL
	}

	resourceManagerV2Client, err := resourcemanagerv2.NewResourceManagerV2(resourceManagerV2Options)
	if err != nil {
		return nil, err
	}

	return &ibmcloudClient{
		authenticator:           authenticator,
		pmClient:                serviceClient,
		identityClient:          identityClient,
		resourceManagerV2Client: resourceManagerV2Client,
	}, nil
}
