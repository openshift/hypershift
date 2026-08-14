package ibmcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/openshift/cloud-credential-operator/pkg/cmd/provisioning"
	"github.com/openshift/cloud-credential-operator/pkg/ibmcloud"
)

// NewRefreshKeysCmd provides the "refresh-keys" subcommand
func NewRefreshKeysCmd() *cobra.Command {
	refreshKeysCmd := &cobra.Command{
		Use:   "refresh-keys",
		Short: "Refresh API Keys for the Service ID",
		RunE:  refreshKeysCmd,
	}

	refreshKeysCmd.PersistentFlags().StringVar(&Options.Name, "name", "", "User-defined name for all created IBM Cloud resources (can be separate from the cluster's infra-id)")
	refreshKeysCmd.MarkPersistentFlagRequired("name")
	refreshKeysCmd.PersistentFlags().StringVar(&Options.CredRequestDir, "credentials-requests-dir", "", "Directory containing files of CredentialsRequests to delete IAM Roles for (can be created by running 'oc adm release extract --credentials-requests --cloud=ibmcloud' against an OpenShift release image)")
	refreshKeysCmd.MarkPersistentFlagRequired("credentials-requests-dir")
	refreshKeysCmd.PersistentFlags().StringVar(&Options.KubeConfigFile, "kubeconfig", "", "absolute path to the kubeconfig file")
	refreshKeysCmd.MarkPersistentFlagRequired("kubeconfig")
	refreshKeysCmd.PersistentFlags().StringVar(&Options.ResourceGroupName, "resource-group-name", "", "Name of the resource group used for scoping the access policies")
	refreshKeysCmd.PersistentFlags().BoolVar(&Options.Create, "create", false, "Create the ServiceID if does not exists")
	refreshKeysCmd.PersistentFlags().BoolVar(&Options.EnableTechPreview, "enable-tech-preview", false, "Opt into processing CredentialsRequests marked as tech-preview")

	return refreshKeysCmd
}

func refreshKeysCmd(cmd *cobra.Command, args []string) error {
	apiKey := getEnv(APIKeyEnvVars)
	if apiKey == "" {
		return fmt.Errorf("%s environment variable not set", APIKeyEnvVars)
	}

	params := &ibmcloud.ClientParams{
		InfraName: Options.Name,
	}

	ibmclient, err := ibmcloud.NewClient(apiKey, params)
	if err != nil {
		return err
	}

	apiKeyDetailsOptions := ibmclient.NewGetAPIKeysDetailsOptions()
	apiKeyDetailsOptions.SetIamAPIKey(apiKey)
	apiKeyDetails, _, err := ibmclient.GetAPIKeysDetails(apiKeyDetailsOptions)
	if err != nil {
		return errors.Wrap(err, "Failed to get details for the given APIKey")
	}

	cs, err := newClientset(Options.KubeConfigFile)
	if err != nil {
		return errors.Wrap(err, "Failed to create the kubernetes clientset")
	}
	err = refreshKeys(ibmclient, cs, apiKeyDetails.AccountID, Options.Name, Options.ResourceGroupName, Options.CredRequestDir, Options.Create, Options.EnableTechPreview)
	if err != nil {
		return errors.Wrap(err, "Failed to refresh keys")
	}
	return nil
}

func refreshKeys(ibmcloudClient ibmcloud.Client, kubeClient kubernetes.Interface, accountID *string, name, resourceGroupName, credReqDir string, create, enableTechPreview bool) error {
	resourceGroupID, err := getResourceGroupID(ibmcloudClient, accountID, resourceGroupName)
	if err != nil {
		return errors.Wrap(err, "Failed to getResourceGroupID")
	}

	// Process directory
	credReqs, err := provisioning.GetListOfCredentialsRequests(credReqDir, enableTechPreview)
	if err != nil {
		return errors.Wrap(err, "Failed to process files containing CredentialsRequests")
	}

	var serviceIDs []*ServiceID
	for _, cr := range credReqs {
		serviceID := NewServiceID(ibmcloudClient, name, *accountID, resourceGroupID, cr)
		serviceIDs = append(serviceIDs, serviceID)
	}

	for _, serviceID := range serviceIDs {
		list, err := serviceID.List()
		if err != nil {
			return errors.Wrapf(err, "Failed to check an existance for the ServiceID: %s", serviceID.name)
		}
		if len(list) == 0 && !create {
			return fmt.Errorf("ServiceID: %s does not exist, rerun with --create flag to create it", serviceID.name)
		}
		if len(list) > 1 {
			return fmt.Errorf("more than one ServiceID found with %s name, please delete the duplicate entries", serviceID.name)
		}
	}

	for _, serviceID := range serviceIDs {
		log.Printf("Refershing the token for ServiceID: %s", serviceID.name)
		list, err := serviceID.List()
		if err != nil {
			return errors.Wrapf(err, "Failed to check an existance for the ServiceID: %s", serviceID.name)
		}
		if len(list) != 0 {
			serviceID.ServiceID = &list[0]
			if err := serviceID.Refresh(); err != nil {
				return errors.Wrapf(err, "Failed to create API Key for ServiceID: %s", serviceID.name)
			}
		} else {
			log.Printf("ServiceID does not exist, creating it.")
			if err := serviceID.Do(); err != nil {
				return errors.Wrap(err, "Failed to process the serviceID")
			}
		}

		secret, err := serviceID.BuildSecret()
		if err != nil {
			return errors.Wrapf(err, "Failed to Dump the secret for the serviceID: %s", serviceID.name)
		}
		data, err := json.Marshal(secret)
		if err != nil {
			return errors.Wrapf(err, "Failed to Marshal")
		}

		log.Printf("Updating/Creating the secret: %s/%s for the serviceID: %s", secret.Namespace, secret.Name, serviceID.name)
		//TODO(mkumatag): replace Patch() call with Apply() after k8s.io/client-go update to v1.21.0 or later
		if _, err := kubeClient.CoreV1().Secrets(secret.Namespace).Patch(context.TODO(), secret.Name, types.ApplyPatchType, data, metav1.PatchOptions{FieldManager: "ccoctl"}); err != nil {
			return errors.Wrapf(err, "Failed to create/update secret")
		}

		log.Printf("Removing the stale API Keys.")
		err = serviceID.RemoveStaleKeys()
		if err != nil {
			return errors.Wrapf(err, "Failed to remove the stale API Keys")
		}
	}

	return nil
}

func newClientset(kubeconfig string) (*kubernetes.Clientset, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}
	// create the clientset
	cs, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return cs, nil
}
