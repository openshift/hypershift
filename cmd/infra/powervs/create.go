package powervs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	hypershiftLog "github.com/openshift/hypershift/cmd/log"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	"github.com/IBM-Cloud/power-go-client/clients/instance"
	"github.com/IBM-Cloud/power-go-client/ibmpisession"
	"github.com/IBM-Cloud/power-go-client/power/models"
	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/networking-go-sdk/dnsrecordsv1"
	"github.com/IBM/networking-go-sdk/transitgatewayapisv1"
	"github.com/IBM/networking-go-sdk/zonesv1"
	"github.com/IBM/platform-services-go-sdk/globalcatalogv1"
	"github.com/IBM/platform-services-go-sdk/globaltaggingv1"
	"github.com/IBM/platform-services-go-sdk/iamidentityv1"
	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"
	"github.com/IBM/platform-services-go-sdk/resourcemanagerv2"
	"github.com/IBM/vpc-go-sdk/vpcv1"
	"github.com/go-logr/logr"
	regionutils "github.com/ppc64le-cloud/powervs-utils"
	"github.com/spf13/cobra"
)

const (
	// Resource name suffix for creation
	cloudInstanceNameSuffix  = "pvs"
	vpcNameSuffix            = "vpc"
	vpcSubnetNameSuffix      = "vpc-sn"
	transitGatewayNameSuffix = "tg"

	// CIS service name
	cisService = "internet-svcs"

	// PowerVS service and plan name
	powerVSService     = "power-iaas"
	powerVSServicePlan = "power-virtual-server-group"

	// Resource desired states
	vpcAvailableState        = "available"
	cloudInstanceActiveState = "active"
	cloudInstanceFailedState = "failed"
	dhcpServiceActiveState   = "ACTIVE"

	// Resource undesired state
	dhcpServiceErrorState = "ERROR"

	// Time duration for monitoring the resource readiness
	dhcpPollingInterval          = time.Minute * 1
	pollingInterval              = time.Second * 5
	vpcCreationTimeout           = time.Minute * 5
	cloudInstanceCreationTimeout = time.Minute * 5
	dhcpServerCreationTimeout    = time.Minute * 30

	// Service Name
	powerVsService  = "powervs"
	vpcService      = "vpc"
	platformService = "platform"
	cosService      = "cos"

	// Secret suffix
	kubeCloudControllerManagerCreds = "cloud-controller-creds"
	nodePoolManagementCreds         = "node-management-creds"
	ingressOperatorCreds            = "ingress-creds"
	storageOperatorCreds            = "storage-creds"
	imageRegistryOperatorCreds      = "image-registry-creds"
)

// CreateInfraOptions command line options for setting up infra in IBM PowerVS cloud
type CreateInfraOptions struct {
	Name                        string
	Namespace                   string
	BaseDomain                  string
	ResourceGroup               string
	InfraID                     string
	Region                      string
	Zone                        string
	CloudInstanceID             string
	VPCRegion                   string
	VPC                         string
	OutputFile                  string
	Debug                       bool
	RecreateSecrets             bool
	TransitGatewayGlobalRouting bool
	TransitGatewayLocation      string
	TransitGateway              string
}

type TimeDuration struct {
	time.Duration
}

var (
	cloudApiKey             string
	timeoutErrorKeywords    = []string{"status 522", "status 524"}
	unsupportedPowerVSZones = []string{"wdc06"}

	powerVsDefaultUrl = func(region string) string { return fmt.Sprintf("https://%s.power-iaas.cloud.ibm.com", region) }
	vpcDefaultUrl     = func(region string) string { return fmt.Sprintf("https://%s.iaas.cloud.ibm.com/v1", region) }

	customEpEnvNameMapping = map[string]string{
		powerVsService:  "IBMCLOUD_POWER_API_ENDPOINT",
		vpcService:      "IBMCLOUD_VPC_API_ENDPOINT",
		platformService: "IBMCLOUD_PLATFORM_API_ENDPOINT",
		cosService:      "IBMCLOUD_COS_API_ENDPOINT",
	}

	dhcpServerLimitExceeds = func(dhcpServerCount int) error {
		return fmt.Errorf("more than one DHCP server is not allowed in a service instance, found %d dhcp servers", dhcpServerCount)
	}
)

// MarshalJSON custom marshaling func for time.Duration to parse Duration into user-friendly format
func (d *TimeDuration) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"%s"`, d.Round(time.Millisecond).String())), nil
}

// UnmarshalJSON custom unmarshalling func for time.Duration
func (d *TimeDuration) UnmarshalJSON(b []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(strings.Trim(string(b), `"`))
	return err
}

var clusterTag = func(infraID string) string { return fmt.Sprintf("kubernetes.io-cluster-%s:owned", infraID) }
var currentDate = fmt.Sprintf("%d-%02d-%02d", time.Now().Year(), time.Now().Month(), time.Now().Day())

type CreateStat struct {
	Duration TimeDuration `json:"duration"`
	Status   string       `json:"status,omitempty"`
}

type InfraCreationStat struct {
	VPC                 CreateStat `json:"vpc"`
	VPCSubnet           CreateStat `json:"vpcSubnet"`
	CloudInstance       CreateStat `json:"cloudInstance"`
	DHCPService         CreateStat `json:"dhcpService"`
	TransitGatewayState CreateStat `json:"transitGatewayState"`
}

type Secrets struct {
	KubeCloudControllerManager *corev1.Secret
	NodePoolManagement         *corev1.Secret
	IngressOperator            *corev1.Secret
	StorageOperator            *corev1.Secret
	ImageRegistryOperator      *corev1.Secret
}

// Infra resource info in IBM Cloud for setting up hypershift nodepool
type Infra struct {
	ID                     string            `json:"id"`
	Region                 string            `json:"region"`
	Zone                   string            `json:"zone"`
	VPCRegion              string            `json:"vpcRegion"`
	AccountID              string            `json:"accountID"`
	BaseDomain             string            `json:"baseDomain"`
	CISCRN                 string            `json:"cisCrn"`
	CISDomainID            string            `json:"cisDomainID"`
	ResourceGroup          string            `json:"resourceGroup"`
	ResourceGroupID        string            `json:"-"`
	CloudInstanceID        string            `json:"cloudInstanceID"`
	DHCPSubnet             string            `json:"dhcpSubnet"`
	DHCPSubnetID           string            `json:"dhcpSubnetID"`
	DHCPID                 string            `json:"-"`
	VPCName                string            `json:"vpcName"`
	VPCID                  string            `json:"-"`
	VPCCRN                 string            `json:"-"`
	VPCRoutingTableID      string            `json:"-"`
	VPCSubnetName          string            `json:"vpcSubnetName"`
	VPCSubnetID            string            `json:"-"`
	Stats                  InfraCreationStat `json:"stats"`
	Secrets                Secrets           `json:"secrets"`
	CloudInstanceCRN       string            `json:"-"`
	TransitGatewayLocation string            `json:"-"`
	TransitGatewayID       string            `json:"-"`
}

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "powervs",
		Short:        "Creates PowerVS infrastructure resources for a cluster",
		SilenceUsage: true,
	}

	opts := CreateInfraOptions{
		Namespace:              "clusters",
		Name:                   "example",
		Region:                 "us-south",
		Zone:                   "us-south",
		VPCRegion:              "us-south",
		TransitGatewayLocation: "us-south",
	}

	cmd.Flags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "IBM Cloud CIS Domain")
	cmd.Flags().StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "IBM Cloud Resource Group")
	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A namespace to contain the generated resources")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID with which to tag IBM Cloud resources")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "IBM Cloud PowerVS Region")
	cmd.Flags().StringVar(&opts.Zone, "zone", opts.Zone, "IBM Cloud PowerVS Zone")
	cmd.Flags().StringVar(&opts.CloudInstanceID, "cloud-instance-id", opts.CloudInstanceID, "IBM PowerVS Cloud Instance ID. Use this flag to reuse an existing PowerVS Cloud Instance resource for cluster's infra")
	cmd.Flags().StringVar(&opts.VPCRegion, "vpc-region", opts.VPCRegion, "IBM Cloud VPC Region for VPC resources")
	cmd.Flags().StringVar(&opts.VPC, "vpc", opts.VPC, "IBM Cloud VPC Name. Use this flag to reuse an existing VPC resource for cluster's infra")
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", opts.OutputFile, "Path to file that will contain output information from infra resources (optional)")
	cmd.Flags().BoolVar(&opts.Debug, "debug", opts.Debug, "Enabling this will print PowerVS API Request & Response logs")
	cmd.Flags().BoolVar(&opts.RecreateSecrets, "recreate-secrets", opts.RecreateSecrets, "Enabling this flag will recreate creds mentioned https://hypershift-docs.netlify.app/reference/api/#hypershift.openshift.io/v1alpha1.PowerVSPlatformSpec here. This is required when rerunning 'hypershift create cluster powervs' or 'hypershift create infra powervs' commands, since API key once created cannot be retrieved again. Please make sure that cluster name used is unique across different management clusters before using this flag")
	cmd.Flags().BoolVar(&opts.TransitGatewayGlobalRouting, "transit-gateway-global-routing", opts.TransitGatewayGlobalRouting, "Enabling this flag chooses global routing mode when creating transit gateway")
	cmd.Flags().StringVar(&opts.TransitGatewayLocation, "transit-gateway-location", opts.TransitGatewayLocation, "IBM Cloud Transit Gateway location")
	cmd.Flags().StringVar(&opts.TransitGateway, "transit-gateway", opts.TransitGateway, "IBM Cloud Transit Gateway. Use this flag to reuse an existing Transit Gateway resource for cluster's infra")

	// these options are only for development and testing purpose,
	// can use these to reuse the existing resources, so hiding it.
	cmd.Flags().MarkHidden("cloud-instance-id")
	cmd.Flags().MarkHidden("vpc")
	cmd.Flags().MarkHidden("transit-gateway")
	cmd.MarkFlagRequired("base-domain")
	cmd.MarkFlagRequired("resource-group")
	cmd.MarkFlagRequired("infra-id")

	logger := hypershiftLog.Log.WithName(opts.InfraID)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context(), logger); err != nil {
			logger.Error(err, "Failed to create infrastructure")
			return err
		}
		logger.Info("Successfully created infrastructure")
		return nil
	}

	return cmd
}

// Run Hypershift Infra Creation
func (options *CreateInfraOptions) Run(ctx context.Context, logger logr.Logger) error {
	err := checkUnsupportedPowerVSZone(options.Zone)
	if err != nil {
		return err
	}
	if options.TransitGatewayLocation == "" {
		return fmt.Errorf("transit gateway location is required")
	}

	infra := &Infra{
		ID:            options.InfraID,
		BaseDomain:    options.BaseDomain,
		ResourceGroup: options.ResourceGroup,
		Region:        options.Region,
		Zone:          options.Zone,
		VPCRegion:     options.VPCRegion,
	}

	defer func() {
		options.Output(infra, logger)
	}()

	if err := infra.SetupInfra(ctx, logger, options); err != nil {
		return err
	}

	return nil
}

func (options *CreateInfraOptions) Output(infra *Infra, logger logr.Logger) {
	out := os.Stdout
	if len(options.OutputFile) > 0 {
		var err error
		out, err = os.Create(options.OutputFile)
		if err != nil {
			logger.Error(err, "cannot create output file")
		}
		defer out.Close()
	}
	outputBytes, err := json.MarshalIndent(infra, "", "  ")
	if err != nil {
		logger.WithName(options.InfraID).Error(err, "failed to serialize output infra")
	}
	_, err = out.Write(outputBytes)
	if err != nil {
		logger.Error(err, "failed to write output infra json")
	}
}

// checkUnsupportedPowerVSZone omitting powervs zones that does not support hypershift infra creation flow
func checkUnsupportedPowerVSZone(zone string) error {
	for i := 0; i < len(unsupportedPowerVSZones); i++ {
		if unsupportedPowerVSZones[i] == zone {
			return fmt.Errorf("%s is unupported PowerVS zone, please use another PowerVS zone", zone)
		}
	}

	return nil
}

func GetAPIKey() (string, error) {
	apiKey := os.Getenv("IBMCLOUD_API_KEY")
	if apiKey != "" {
		return apiKey, nil
	}
	apiKeyCredFile := os.Getenv("IBMCLOUD_CREDENTIALS")
	if apiKeyCredFile != "" {
		data, err := os.ReadFile(apiKeyCredFile)
		if err != nil {
			return "", fmt.Errorf("error reading from IBMCLOUD_CREDENTIALS file %s: %w", apiKeyCredFile, err)
		}
		apiKey = strings.Trim(string(data), "\n")
		return apiKey, nil
	}

	return "", nil
}

// SetupInfra infra creation orchestration
func (infra *Infra) SetupInfra(ctx context.Context, logger logr.Logger, options *CreateInfraOptions) error {
	startTime := time.Now()
	var err error

	logger.Info("Setup infra started")

	cloudApiKey, err = GetAPIKey()
	if err != nil {
		return fmt.Errorf("error retrieving IBM Cloud API Key: %w", err)
	}

	// if CLOUD API KEY is not set, infra cannot be set up.
	if cloudApiKey == "" {
		return fmt.Errorf("cloud API Key not set. Set it with IBMCLOUD_API_KEY env var or set file path containing API Key credential in IBMCLOUD_CREDENTIALS")
	}

	infra.AccountID, err = getAccount(ctx, getIAMAuth())
	if err != nil {
		return fmt.Errorf("error retrieving account ID %w", err)
	}

	infra.ResourceGroupID, err = getResourceGroupID(ctx, options.ResourceGroup, infra.AccountID)
	if err != nil {
		return fmt.Errorf("error getting id for resource group %s, %w", options.ResourceGroup, err)
	}

	if err := infra.setupBaseDomain(ctx, logger, options); err != nil {
		return fmt.Errorf("error setup base domain: %w", err)
	}

	gtag, err := globaltaggingv1.NewGlobalTaggingV1(&globaltaggingv1.GlobalTaggingV1Options{Authenticator: getIAMAuth()})
	if err != nil {
		return err
	}

	v1, err := createVpcService(logger, options.VPCRegion)
	if err != nil {
		return fmt.Errorf("error creating vpc service: %w", err)
	}

	if err := infra.setupVpc(ctx, logger, options, v1, gtag); err != nil {
		return fmt.Errorf("error setup vpc: %w", err)
	}

	if err := infra.setupVpcSubnet(ctx, logger, options, v1); err != nil {
		return fmt.Errorf("error setup vpc subnet: %w", err)
	}

	session, err := createPowerVSSession(infra.AccountID, options.Region, options.Zone, options.Debug)
	infra.AccountID = session.Options.UserAccount
	if err != nil {
		return fmt.Errorf("error creating powervs session: %w", err)
	}

	if err := infra.setupPowerVSCloudInstance(ctx, logger, options, gtag); err != nil {
		return fmt.Errorf("error setup powervs cloud instance: %w", err)
	}

	if err := infra.setupTransitGateway(ctx, logger, options, gtag); err != nil {
		return fmt.Errorf("error setup transit gateway: %w", err)
	}

	if err := infra.setupPowerVSDHCP(ctx, logger, options, session); err != nil {
		return fmt.Errorf("error setup powervs dhcp server: %w", err)
	}

	// setupSecrets need parameter cloudInstanceId, hence invoked after setupPowerVSCloudInstance
	if err := infra.setupSecrets(logger, options); err != nil {
		return fmt.Errorf("error setup secrets: %w", err)
	}

	logger.Info("Setup infra completed in", "duration", time.Since(startTime).String())
	return nil
}

// setupSecrets generate secrets for control plane components
func (infra *Infra) setupSecrets(logger logr.Logger, options *CreateInfraOptions) error {
	var err error
	var powerVsCloudInstanceID string

	if options.CloudInstanceID != "" {
		powerVsCloudInstanceID = options.CloudInstanceID
	} else if infra.CloudInstanceID != "" {
		powerVsCloudInstanceID = infra.CloudInstanceID
	} else {
		return fmt.Errorf("unable to limit access scope to instance level: cloud instance not found")
	}

	if options.RecreateSecrets {
		deleteSecrets(options.Name, options.Namespace, powerVsCloudInstanceID, infra.AccountID, infra.ResourceGroupID)
	}

	logger.Info("Creating Secrets ...")

	infra.Secrets = Secrets{}

	kubeCloudControllerManagerCR, err = updateCRYaml(kubeCloudControllerManagerCR, "kubeCloudControllerManagerCRTemplate", powerVsCloudInstanceID)
	if err != nil {
		return fmt.Errorf("error updating kube cloud controller manager yaml: %w", err)
	}
	infra.Secrets.KubeCloudControllerManager, err = setupServiceID(options.Name, cloudApiKey, infra.AccountID, infra.ResourceGroupID,
		kubeCloudControllerManagerCR, kubeCloudControllerManagerCreds, options.Namespace)
	if err != nil {
		return fmt.Errorf("error setup kube cloud controller manager secret: %w", err)
	}

	nodePoolManagementCR, err = updateCRYaml(nodePoolManagementCR, "nodePoolManagementCRTemplate", powerVsCloudInstanceID)
	if err != nil {
		return fmt.Errorf("error updating nodepool management yaml: %w", err)
	}
	infra.Secrets.NodePoolManagement, err = setupServiceID(options.Name, cloudApiKey, infra.AccountID, infra.ResourceGroupID,
		nodePoolManagementCR, nodePoolManagementCreds, options.Namespace)
	if err != nil {
		return fmt.Errorf("error setup nodepool management secret: %w", err)
	}

	infra.Secrets.IngressOperator, err = setupServiceID(options.Name, cloudApiKey, infra.AccountID, "",
		ingressOperatorCR, ingressOperatorCreds, options.Namespace)
	if err != nil {
		return fmt.Errorf("error setup ingress operator secret: %w", err)
	}

	storageOperatorCR, err = updateCRYaml(storageOperatorCR, "storageOperatorCRTemplate", powerVsCloudInstanceID)
	if err != nil {
		return fmt.Errorf("error updating storage operator yaml: %w", err)
	}
	infra.Secrets.StorageOperator, err = setupServiceID(options.Name, cloudApiKey, infra.AccountID, infra.ResourceGroupID,
		storageOperatorCR, storageOperatorCreds, options.Namespace)
	if err != nil {
		return fmt.Errorf("error setup storage operator secret: %w", err)
	}

	infra.Secrets.ImageRegistryOperator, err = setupServiceID(options.Name, cloudApiKey, infra.AccountID, infra.ResourceGroupID,
		imageRegistryOperatorCR, imageRegistryOperatorCreds, options.Namespace)
	if err != nil {
		return fmt.Errorf("error setup image registry operator secret: %w", err)
	}

	logger.Info("Secrets Ready")

	return nil
}

// getIAMAuth getting core.Authenticator
func getIAMAuth() *core.IamAuthenticator {
	return &core.IamAuthenticator{
		ApiKey: cloudApiKey,
	}
}

// getCISDomainDetails getting CIS domain details like CRN and domainID
func getCISDomainDetails(ctx context.Context, baseDomain string) (string, string, error) {
	var CISCRN, CISDomainID string
	rcv2, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{
		Authenticator: getIAMAuth(),
		URL:           getCustomEndpointUrl(platformService, resourcecontrollerv2.DefaultServiceURL),
	})
	if err != nil {
		return "", "", err
	}

	if rcv2 == nil {
		return "", "", fmt.Errorf("unable to get resource controller")
	}

	// getting list of resource instance of type cis
	serviceID, _, err := getServiceInfo(ctx, cisService, "")

	if err != nil {
		return "", "", fmt.Errorf("error retrieving cis service %w", err)
	}

	f := func(start string) (bool, string, error) {
		listResourceOpt := resourcecontrollerv2.ListResourceInstancesOptions{ResourceID: &serviceID}

		// for getting the next page
		if start != "" {
			listResourceOpt.Start = &start
		}
		resourceList, _, err := rcv2.ListResourceInstancesWithContext(ctx, &listResourceOpt)

		if err != nil {
			return false, "", err
		}

		if resourceList == nil {
			return false, "", fmt.Errorf("resourceList returned is nil")
		}

		// looping through all resource instance of type cis until given base domain is found
		for _, resource := range resourceList.Resources {
			// trying to loop over all resource's zones to find the matched domain name
			// if any issue in processing current resource, will continue to process next resource's zones until the given domain name matches
			var zv1 *zonesv1.ZonesV1
			zv1, err = zonesv1.NewZonesV1(&zonesv1.ZonesV1Options{Authenticator: getIAMAuth(), Crn: resource.CRN})
			if err != nil {
				continue
			}
			if zv1 == nil {
				continue
			}
			var zoneList *zonesv1.ListZonesResp
			zoneList, _, err = zv1.ListZonesWithContext(ctx, &zonesv1.ListZonesOptions{})
			if err != nil {
				continue
			}

			if zoneList != nil {
				for _, zone := range zoneList.Result {
					if *zone.Name == baseDomain {
						CISCRN = *resource.CRN
						CISDomainID = *zone.ID
						return true, "", nil
					}
				}
			}
		}

		// For paging over next set of resources getting the start token
		if resourceList.NextURL != nil && *resourceList.NextURL != "" {
			return false, *resourceList.NextURL, nil
		}

		return true, "", nil
	}

	err = pagingHelper(f)

	if err != nil {
		return "", "", err
	}

	if CISCRN == "" || CISDomainID == "" {
		return "", "", fmt.Errorf("unable to get cis information with base domain %s", baseDomain)
	}

	return CISCRN, CISDomainID, nil
}

// checkForExistingDNSRecord check for existing DNS record with the cluster name
func checkForExistingDNSRecord(ctx context.Context, options *CreateInfraOptions, CISCRN string, CISDomainID string) error {
	dnsRecordsV1, err := dnsrecordsv1.NewDnsRecordsV1(&dnsrecordsv1.DnsRecordsV1Options{Crn: &CISCRN, ZoneIdentifier: &CISDomainID, Authenticator: getIAMAuth()})
	if err != nil {
		return fmt.Errorf("error creating dns record client: %w", err)
	}

	recordName := fmt.Sprintf("*.apps.%s.%s", options.Name, options.BaseDomain)
	listDnsRecordsOpt := &dnsrecordsv1.ListAllDnsRecordsOptions{Name: &recordName}

	dnsRecordsL, _, err := dnsRecordsV1.ListAllDnsRecordsWithContext(ctx, listDnsRecordsOpt)
	if err != nil {
		return err
	}

	if len(dnsRecordsL.Result) == 0 {
		return nil
	}

	return fmt.Errorf("existing DNS record '%s' found in base domain %s, cannot proceed to cluster creation when dns record already exists with the cluster name", recordName, options.BaseDomain)
}

// setupBaseDomain get domain id and crn of given base domain
// TODO(dharaneeshvrd): Currently, resource group provided will be considered only for VPC and PowerVS. Need to look at utilising a common resource group in future for CIS service too and use it while filtering the list
func (infra *Infra) setupBaseDomain(ctx context.Context, logger logr.Logger, options *CreateInfraOptions) error {
	var err error
	infra.CISCRN, infra.CISDomainID, err = getCISDomainDetails(ctx, options.BaseDomain)

	if err != nil {
		return fmt.Errorf("error retrieving cis domain details %w", err)
	}

	if err = checkForExistingDNSRecord(ctx, options, infra.CISCRN, infra.CISDomainID); err != nil {
		return err
	}

	logger.Info("BaseDomain Info Ready", "CRN", infra.CISCRN, "DomainID", infra.CISDomainID)
	return nil
}

// getServiceInfo retrieving id info of given service and service plan
func getServiceInfo(ctx context.Context, service string, servicePlan string) (string, string, error) {
	var serviceID, servicePlanID string
	gcv1, err := globalcatalogv1.NewGlobalCatalogV1(&globalcatalogv1.GlobalCatalogV1Options{
		Authenticator: getIAMAuth(),
		URL:           getCustomEndpointUrl(platformService, globalcatalogv1.DefaultServiceURL),
	})
	if err != nil {
		return "", "", err
	}

	if gcv1 == nil {
		return "", "", fmt.Errorf("unable to get global catalog")
	}

	// TO-DO need to explore paging for catalog list since ListCatalogEntriesOptions does not take start
	include := "*"
	listCatalogEntriesOpt := globalcatalogv1.ListCatalogEntriesOptions{Include: &include, Q: &service}
	catalogEntriesList, _, err := gcv1.ListCatalogEntriesWithContext(ctx, &listCatalogEntriesOpt)
	if err != nil {
		return "", "", err
	}
	if catalogEntriesList != nil {
		for _, catalog := range catalogEntriesList.Resources {
			if *catalog.Name == service {
				serviceID = *catalog.ID
			}
		}
	}

	if serviceID == "" {
		return "", "", fmt.Errorf("could not retrieve service id for service %s", service)
	} else if servicePlan == "" {
		return serviceID, "", nil
	} else {
		kind := "plan"
		getChildOpt := globalcatalogv1.GetChildObjectsOptions{ID: &serviceID, Kind: &kind}
		var childObjResult *globalcatalogv1.EntrySearchResult
		childObjResult, _, err = gcv1.GetChildObjectsWithContext(ctx, &getChildOpt)
		if err != nil {
			return "", "", err
		}
		for _, plan := range childObjResult.Resources {
			if *plan.Name == servicePlan {
				servicePlanID = *plan.ID
				return serviceID, servicePlanID, nil
			}
		}
	}
	err = fmt.Errorf("could not retrieve plan id for service name: %s & service plan name: %s", service, servicePlan)
	return "", "", err
}

// getCustomEndpointUrl appending custom endpoint to the url if the respective resource's env is set
func getCustomEndpointUrl(serviceName string, defaultUrl string) string {
	apiEP := os.Getenv(customEpEnvNameMapping[serviceName])
	url := defaultUrl
	if apiEP != "" {
		if serviceName == cosService {
			url = strings.Replace(defaultUrl, "s3.", fmt.Sprintf("s3.%s.", apiEP), 1)
		} else {
			url = strings.Replace(defaultUrl, "https://", fmt.Sprintf("https://%s.", apiEP), 1)
		}
	}

	return url
}

// getResourceGroupID retrieving id of resource group
func getResourceGroupID(ctx context.Context, resourceGroup string, accountID string) (string, error) {
	rmv2, err := resourcemanagerv2.NewResourceManagerV2(&resourcemanagerv2.ResourceManagerV2Options{
		Authenticator: getIAMAuth(),
		URL:           getCustomEndpointUrl(platformService, resourcemanagerv2.DefaultServiceURL),
	})
	if err != nil {
		return "", err
	}

	if rmv2 == nil {
		return "", fmt.Errorf("unable to get resource controller")
	}

	rmv2ListResourceGroupOpt := resourcemanagerv2.ListResourceGroupsOptions{Name: &resourceGroup, AccountID: &accountID}
	resourceGroupListResult, _, err := rmv2.ListResourceGroupsWithContext(ctx, &rmv2ListResourceGroupOpt)
	if err != nil {
		return "", err
	}

	if resourceGroupListResult != nil && len(resourceGroupListResult.Resources) > 0 {
		rg := resourceGroupListResult.Resources[0]
		resourceGroupID := *rg.ID
		return resourceGroupID, nil
	}

	err = fmt.Errorf("could not retrieve resource group id for %s", resourceGroup)
	return "", err
}

// createCloudInstance creating powervs cloud instance
func (infra *Infra) createCloudInstance(ctx context.Context, logger logr.Logger, options *CreateInfraOptions) (*resourcecontrollerv2.ResourceInstance, error) {

	rcv2, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{
		Authenticator: getIAMAuth(),
		URL:           getCustomEndpointUrl(platformService, resourcecontrollerv2.DefaultServiceURL),
	})

	if err != nil {
		return nil, err
	}

	if rcv2 == nil {
		return nil, fmt.Errorf("unable to get resource controller")
	}

	serviceID, servicePlanID, err := getServiceInfo(ctx, powerVSService, powerVSServicePlan)
	if err != nil {
		return nil, fmt.Errorf("error retrieving id info for powervs service %w", err)
	}

	cloudInstanceName := fmt.Sprintf("%s-%s", options.InfraID, cloudInstanceNameSuffix)

	// validate if already a cloud instance available with the infra provided
	// if yes, make use of that instead of trying to create a new one
	resourceInstance, err := validateCloudInstanceByName(ctx, cloudInstanceName, infra.ResourceGroupID, options.Zone, serviceID, servicePlanID)

	if resourceInstance != nil {
		if err != nil && (err.Error() == cloudInstanceNotInActiveState(*resourceInstance.State).Error()) {
			err = fmt.Errorf("already a PowerVS instance exist with the infraID but it is not in usable state: %v", err.Error())
			return nil, err
		}
		logger.Info("Using existing PowerVS Cloud Instance", "name", cloudInstanceName)
		return resourceInstance, nil
	}

	logger.Info("Creating PowerVS Cloud Instance ...")
	target := options.Zone

	resourceInstanceOpt := resourcecontrollerv2.CreateResourceInstanceOptions{Name: &cloudInstanceName,
		ResourceGroup:  &infra.ResourceGroupID,
		ResourcePlanID: &servicePlanID,
		Target:         &target}

	startTime := time.Now()
	resourceInstance, _, err = rcv2.CreateResourceInstanceWithContext(ctx, &resourceInstanceOpt)
	if err != nil {
		return nil, err
	}

	if resourceInstance == nil {
		return nil, fmt.Errorf("create cloud instance returned nil")
	}

	if *resourceInstance.State == cloudInstanceActiveState {
		return resourceInstance, nil
	}

	f := func() (bool, error) {
		resourceInstance, _, err = rcv2.GetResourceInstanceWithContext(ctx, &resourcecontrollerv2.GetResourceInstanceOptions{ID: resourceInstance.ID})
		logger.Info("Waiting for cloud instance to up", "id", resourceInstance.ID, "state", *resourceInstance.State)

		if err != nil {
			return false, err
		}

		if *resourceInstance.State == cloudInstanceActiveState {
			return true, nil
		}
		if *resourceInstance.State == cloudInstanceFailedState {
			return false, fmt.Errorf("cloud instance is in failed state")
		}

		return false, nil
	}

	if err = wait.PollImmediate(pollingInterval, cloudInstanceCreationTimeout, f); err != nil {
		return nil, err
	}

	infra.Stats.CloudInstance.Duration.Duration = time.Since(startTime)

	return resourceInstance, nil
}

// getAccount getting the account id from core.Authenticator
func getAccount(ctx context.Context, auth core.Authenticator) (string, error) {
	iamv1, err := iamidentityv1.NewIamIdentityV1(&iamidentityv1.IamIdentityV1Options{
		Authenticator: auth,
		URL:           getCustomEndpointUrl(platformService, iamidentityv1.DefaultServiceURL),
	})
	if err != nil {
		return "", err
	}

	apiKeyDetailsOpt := iamidentityv1.GetAPIKeysDetailsOptions{IamAPIKey: &cloudApiKey}
	apiKey, _, err := iamv1.GetAPIKeysDetailsWithContext(ctx, &apiKeyDetailsOpt)
	if err != nil {
		return "", err
	}
	if apiKey == nil {
		return "", fmt.Errorf("could not retrieve account id")
	}

	return *apiKey.AccountID, nil
}

// createPowerVSSession creates PowerVSSession of type *ibmpisession.IBMPISession
func createPowerVSSession(accountID string, powerVSRegion string, powerVSZone string, debug bool) (*ibmpisession.IBMPISession, error) {
	auth := getIAMAuth()

	opt := &ibmpisession.IBMPIOptions{Authenticator: auth,
		Debug:       debug,
		URL:         getCustomEndpointUrl(powerVsService, powerVsDefaultUrl(powerVSRegion)),
		UserAccount: accountID,
		Zone:        powerVSZone}

	return ibmpisession.NewIBMPISession(opt)
}

// createVpcService creates VpcService of type *vpcv1.VpcV1
func createVpcService(logger logr.Logger, region string) (*vpcv1.VpcV1, error) {
	v1, err := vpcv1.NewVpcV1(&vpcv1.VpcV1Options{
		ServiceName:   "vpcs",
		Authenticator: getIAMAuth(),
		URL:           getCustomEndpointUrl(vpcService, vpcDefaultUrl(region)),
	})
	logger.Info("Created VPC Service for", "URL", v1.GetServiceURL())
	return v1, err
}

// setupPowerVSCloudInstance takes care of setting up powervs cloud instance
func (infra *Infra) setupPowerVSCloudInstance(ctx context.Context, logger logr.Logger, options *CreateInfraOptions, gtag *globaltaggingv1.GlobalTaggingV1) error {
	logger.Info("Setting up PowerVS Cloud Instance ...")
	var cloudInstance *resourcecontrollerv2.ResourceInstance
	if options.CloudInstanceID != "" {
		logger.Info("Validating PowerVS Cloud Instance", "id", options.CloudInstanceID)
		var err error
		cloudInstance, err = validateCloudInstanceByID(ctx, options.CloudInstanceID)
		if err != nil {
			return fmt.Errorf("error validating cloud instance id %s, %w", options.CloudInstanceID, err)
		}
	} else {
		var err error
		cloudInstance, err = infra.createCloudInstance(ctx, logger, options)
		if err != nil {
			return fmt.Errorf("error creating cloud instance: %w", err)
		}
	}

	if cloudInstance != nil {
		infra.CloudInstanceID = *cloudInstance.GUID
		// This is required for transit gateway to create connection to powervs instance
		infra.CloudInstanceCRN = *cloudInstance.CRN
		infra.Stats.CloudInstance.Status = *cloudInstance.State
	}

	if infra.CloudInstanceID == "" {
		return fmt.Errorf("unable to setup powervs cloud instance")
	}

	if err := attachTag(gtag, options.InfraID, cloudInstance.CRN, fmt.Sprintf("%s-%s", infra.ID, cloudInstanceNameSuffix)); err != nil {
		logger.Error(err, "error attaching tags to powervs cloud instance")
	}

	logger.Info("PowerVS Cloud Instance Ready", "id", infra.CloudInstanceID)
	return nil
}

// setupVpc takes care of setting up vpc
func (infra *Infra) setupVpc(ctx context.Context, logger logr.Logger, options *CreateInfraOptions, v1 *vpcv1.VpcV1, gtag *globaltaggingv1.GlobalTaggingV1) error {
	logger.Info("Setting up VPC ...")
	var vpc *vpcv1.VPC
	if options.VPC != "" {
		var err error
		logger.Info("Validating VPC", "name", options.VPC)
		vpc, err = validateVpc(ctx, options.VPC, infra.ResourceGroupID, v1)
		if err != nil {
			return err
		}
	} else {
		var err error
		vpc, err = infra.createVpc(ctx, logger, options, infra.ResourceGroupID, v1)
		if err != nil {
			return err
		}
	}

	if vpc != nil {
		infra.VPCName = *vpc.Name
		infra.VPCID = *vpc.ID
		infra.VPCCRN = *vpc.CRN
		infra.VPCRoutingTableID = *vpc.DefaultRoutingTable.ID
		infra.Stats.VPC.Status = *vpc.Status
	}

	if infra.VPCID == "" {
		return fmt.Errorf("unable to setup vpc")
	}

	if err := attachTag(gtag, options.InfraID, &infra.VPCCRN, fmt.Sprintf("%s-%s", infra.ID, vpcNameSuffix)); err != nil {
		logger.Error(err, "error attaching tags to vpc")
	}

	logger.Info("VPC Ready", "ID", infra.VPCID)
	return nil
}

// createVpc creates a new vpc with the infra name or will return an existing vpc
func (infra *Infra) createVpc(ctx context.Context, logger logr.Logger, options *CreateInfraOptions, resourceGroupID string, v1 *vpcv1.VpcV1) (*vpcv1.VPC, error) {
	var startTime time.Time
	vpcName := fmt.Sprintf("%s-%s", options.InfraID, vpcNameSuffix)
	vpc, err := validateVpc(ctx, vpcName, resourceGroupID, v1)

	// if vpc already exist use it or proceed with creating a new one, no need to validate err
	if vpc != nil && *vpc.Name == vpcName {
		logger.Info("Using existing VPC", "name", vpcName)
		return vpc, nil
	}

	logger.Info("Creating VPC ...")
	addressPrefixManagement := "auto"

	vpcOption := &vpcv1.CreateVPCOptions{
		ResourceGroup:           &vpcv1.ResourceGroupIdentity{ID: &resourceGroupID},
		Name:                    &vpcName,
		AddressPrefixManagement: &addressPrefixManagement,
	}

	startTime = time.Now()
	vpc, _, err = v1.CreateVPCWithContext(ctx, vpcOption)
	if err != nil {
		return nil, err
	}

	f := func() (bool, error) {

		vpc, _, err = v1.GetVPCWithContext(ctx, &vpcv1.GetVPCOptions{ID: vpc.ID})
		if err != nil {
			return false, err
		}

		if *vpc.Status == vpcAvailableState {
			return true, nil
		}
		return false, nil
	}

	if err = wait.PollImmediate(pollingInterval, vpcCreationTimeout, f); err != nil {
		return nil, err
	}

	// Adding allow rules for VPC's default security group to allow http and https for ingress
	for _, port := range []int64{80, 443} {
		_, _, err = v1.CreateSecurityGroupRuleWithContext(ctx, &vpcv1.CreateSecurityGroupRuleOptions{
			SecurityGroupID: vpc.DefaultSecurityGroup.ID,

			SecurityGroupRulePrototype: &vpcv1.SecurityGroupRulePrototype{
				Direction: ptr.To("inbound"),
				Protocol:  ptr.To("tcp"),
				PortMax:   ptr.To(port),
				PortMin:   ptr.To(port),
			},
		})

		if err != nil {
			return nil, fmt.Errorf("error attaching inbound security group rule to allow %d to vpc %v", port, err)
		}
	}

	if !startTime.IsZero() && vpc != nil {
		infra.Stats.VPC.Duration.Duration = time.Since(startTime)
	}

	return vpc, nil
}

// setupVpcSubnet takes care of setting up subnet in the vpc
func (infra *Infra) setupVpcSubnet(ctx context.Context, logger logr.Logger, options *CreateInfraOptions, v1 *vpcv1.VpcV1) error {
	logger.Info("Setting up VPC Subnet ...")

	logger.Info("Getting existing VPC Subnet info ...")
	var subnet *vpcv1.Subnet
	f := func(start string) (bool, string, error) {
		// check for existing subnets
		listSubnetOpt := vpcv1.ListSubnetsOptions{ResourceGroupID: &infra.ResourceGroupID, RoutingTableID: &infra.VPCRoutingTableID}
		if start != "" {
			listSubnetOpt.Start = &start
		}

		vpcSubnetL, _, err := v1.ListSubnetsWithContext(ctx, &listSubnetOpt)
		if err != nil {
			return false, "", err
		}

		if vpcSubnetL == nil {
			return false, "", fmt.Errorf("subnet list returned is nil")
		}

		if len(vpcSubnetL.Subnets) > 0 {
			for _, sn := range vpcSubnetL.Subnets {
				if *sn.VPC.ID == infra.VPCID {
					infra.VPCSubnetName = *sn.Name
					infra.VPCSubnetID = *sn.ID
					subnet = &sn
					return true, "", nil
				}
			}
		}

		if vpcSubnetL.Next != nil && *vpcSubnetL.Next.Href != "" {
			return false, *vpcSubnetL.Next.Href, nil
		}
		return true, "", nil
	}

	// if subnet already exist use it or proceed with creating a new one, no need to validate err
	_ = pagingHelper(f)

	if infra.VPCSubnetID == "" {
		var err error
		subnet, err = infra.createVpcSubnet(ctx, logger, options, v1)
		if err != nil {
			return err
		}
		infra.VPCSubnetName = *subnet.Name
		infra.VPCSubnetID = *subnet.ID
	}

	if subnet != nil {
		infra.Stats.VPCSubnet.Status = *subnet.Status
	}

	logger.Info("VPC Subnet Ready", "ID", infra.VPCSubnetID)
	return nil
}

// createVpcSubnet creates a new subnet in vpc with the infra name or will return an existing subnet in the vpc
func (infra *Infra) createVpcSubnet(ctx context.Context, logger logr.Logger, options *CreateInfraOptions, v1 *vpcv1.VpcV1) (*vpcv1.Subnet, error) {
	logger.Info("Create VPC Subnet ...")
	var subnet *vpcv1.Subnet
	var startTime time.Time
	vpcIdent := &vpcv1.VPCIdentity{CRN: &infra.VPCCRN}
	resourceGroupIdent := &vpcv1.ResourceGroupIdentity{ID: &infra.ResourceGroupID}
	subnetName := fmt.Sprintf("%s-%s", options.InfraID, vpcSubnetNameSuffix)
	ipVersion := "ipv4"
	zones, _, err := v1.ListRegionZonesWithContext(ctx, &vpcv1.ListRegionZonesOptions{RegionName: &options.VPCRegion})
	if err != nil {
		return nil, err
	}

	addressPrefixL, _, err := v1.ListVPCAddressPrefixesWithContext(ctx, &vpcv1.ListVPCAddressPrefixesOptions{VPCID: &infra.VPCID})
	if err != nil {
		return nil, err
	}

	// loop through all zones in given region and get respective address prefix and try to create subnet
	// if subnet creation fails in first zone, try in other zones until succeeds
	for _, zone := range zones.Zones {

		zoneIdent := &vpcv1.ZoneIdentity{Name: zone.Name}

		var ipv4CidrBlock *string
		for _, addressPrefix := range addressPrefixL.AddressPrefixes {
			if *zoneIdent.Name == *addressPrefix.Zone.Name {
				ipv4CidrBlock = addressPrefix.CIDR
				break
			}
		}

		subnetProto := &vpcv1.SubnetPrototype{VPC: vpcIdent,
			Name:          &subnetName,
			ResourceGroup: resourceGroupIdent,
			Zone:          zoneIdent,
			IPVersion:     &ipVersion,
			Ipv4CIDRBlock: ipv4CidrBlock,
		}

		startTime = time.Now()
		subnet, _, err = v1.CreateSubnetWithContext(ctx, &vpcv1.CreateSubnetOptions{SubnetPrototype: subnetProto})
		if err != nil {
			continue
		}
		break
	}

	if subnet == nil {
		return nil, fmt.Errorf("CreateSubnet returned nil")
	}

	f := func() (bool, error) {

		subnet, _, err = v1.GetSubnetWithContext(ctx, &vpcv1.GetSubnetOptions{ID: subnet.ID})
		if err != nil {
			return false, err
		}

		if *subnet.Status == vpcAvailableState {
			return true, nil
		}
		return false, nil
	}

	if err = wait.PollImmediate(pollingInterval, vpcCreationTimeout, f); err != nil {
		return nil, err
	}

	if !startTime.IsZero() {
		infra.Stats.VPCSubnet.Duration.Duration = time.Since(startTime)
	}

	return subnet, nil
}

// useExistingDHCP returns details of existing DHCP server
func useExistingDHCP(dhcpServers models.DHCPServers) (string, error) {
	if len(dhcpServers) == 1 {
		dhcp := dhcpServers[0]
		return *dhcp.ID, nil
	}

	return "", dhcpServerLimitExceeds(len(dhcpServers))
}

// setupPowerVSDHCP takes care of setting up dhcp in powervs
func (infra *Infra) setupPowerVSDHCP(ctx context.Context, logger logr.Logger, options *CreateInfraOptions, session *ibmpisession.IBMPISession) error {
	logger.Info("Setting up PowerVS DHCP ...")
	client := instance.NewIBMPIDhcpClient(ctx, session, infra.CloudInstanceID)

	var dhcpServer *models.DHCPServerDetail

	dhcpServers, err := client.GetAll()
	if err != nil {
		return err
	}

	// only one dhcp server is allowed per cloud instance
	// if already a dhcp server existing in cloud instance use that instead of creating a new one
	if len(dhcpServers) > 0 {
		logger.Info("Using existing DHCP server present in cloud instance")
		var dhcpServerID string
		dhcpServerID, err = useExistingDHCP(dhcpServers)
		if err != nil {
			return err
		}

		dhcpServer, err = client.Get(dhcpServerID)
		if *dhcpServer.Status != dhcpServiceActiveState {
			var isActive bool
			f := func() (bool, error) {
				dhcpServer, isActive, err = isDHCPServerActive(logger, client, dhcpServerID)
				return isActive, err
			}

			if err = wait.PollImmediate(dhcpPollingInterval, dhcpServerCreationTimeout, f); err != nil {
				return err
			}
		}
	} else {
		logger.Info("Creating PowerVS DHCPServer...")
		dhcpServer, err = infra.createPowerVSDhcp(logger, options, client)
	}

	if err != nil {
		return err
	}

	if dhcpServer != nil {
		infra.DHCPID = *dhcpServer.ID
		if dhcpServer.Network != nil {
			infra.DHCPSubnet = *dhcpServer.Network.Name
			infra.DHCPSubnetID = *dhcpServer.Network.ID
		}
		infra.Stats.DHCPService.Status = *dhcpServer.Status
	}

	if infra.DHCPID == "" || infra.DHCPSubnetID == "" {
		return fmt.Errorf("unable to setup powervs dhcp server, dhcp server id or subnet id returned is empty. dhcpServerId: %s, dhcpPrivateSubnetId: %s", infra.DHCPID, infra.DHCPSubnetID)
	}

	logger.Info("PowerVS DHCP Server and Private Subnet Ready", "id", infra.DHCPID, "subnetId", infra.DHCPSubnetID)
	return nil
}

// isNotRetryableError validates err contains possible retryable error keywords like timeoutErrorKeywords, if yes, return the same error else return nil
func isNotRetryableError(err error, retryableErrorKeywords []string) error {
	for _, e := range retryableErrorKeywords {
		if strings.Contains(err.Error(), e) {
			return nil
		}
	}
	return err
}

// isDHCPServerActive monitors DHCP status to reach either ACTIVE or ERROR status which indicates it reaches a final state
// returns an instance of DHCPServerDetail for further processing and true if it reaches ACTIVE status
func isDHCPServerActive(logger logr.Logger, client *instance.IBMPIDhcpClient, dhcpID string) (*models.DHCPServerDetail, bool, error) {
	var err error
	dhcpServer, err := client.Get(dhcpID)
	if err != nil {
		if err = isNotRetryableError(err, timeoutErrorKeywords); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}

	if dhcpServer != nil {
		logger.Info("Waiting for DHCPServer to up", "id", *dhcpServer.ID, "status", *dhcpServer.Status)
		if *dhcpServer.Status == dhcpServiceActiveState {
			return dhcpServer, true, nil
		}

		if *dhcpServer.Status == dhcpServiceErrorState {
			return nil, false, fmt.Errorf("dhcp server is in error state")
		}
	}

	return nil, false, nil
}

// createPowerVSDhcp creates a new dhcp server in powervs
func (infra *Infra) createPowerVSDhcp(logger logr.Logger, options *CreateInfraOptions, client *instance.IBMPIDhcpClient) (*models.DHCPServerDetail, error) {
	startTime := time.Now()
	var dhcpServer *models.DHCPServerDetail

	// With the recent update default DNS server is pointing to loop back address in DHCP. Hence, passed 1.1.1.1 public DNS resolver.
	dhcpServerCreateOpts := &models.DHCPServerCreate{DNSServer: ptr.To("1.1.1.1")}
	dhcp, err := client.Create(dhcpServerCreateOpts)

	if err != nil {
		return nil, err
	}

	if dhcp == nil {
		return nil, fmt.Errorf("created dhcp server is nil")
	}

	var isActive bool
	f := func() (bool, error) {
		dhcpServer, isActive, err = isDHCPServerActive(logger, client, *dhcp.ID)
		return isActive, err
	}

	if err = wait.PollImmediate(dhcpPollingInterval, dhcpServerCreationTimeout, f); err != nil {
		return nil, err
	}

	if dhcpServer != nil {
		infra.Stats.DHCPService.Duration.Duration = time.Since(startTime)
	}
	return dhcpServer, nil
}

// attachTag would attach tags to cloud resources which can be used to filter resources with API as well as in IBM Cloud UI.
// "kubernetes.io-cluster-<infraID>:owned" tag would be attached to the resources
func attachTag(gtag *globaltaggingv1.GlobalTaggingV1, infraID string, resourceId *string, resourceName string) error {

	if _, _, err := gtag.AttachTag(&globaltaggingv1.AttachTagOptions{Resources: []globaltaggingv1.Resource{
		{ResourceID: resourceId},
	},
		TagNames: []string{clusterTag(infraID), fmt.Sprintf("Name:%s", resourceName)},
	}); err != nil {
		return err
	}

	return nil
}

// setupTransitGateway set up the transit gateway for the cluster infra
func (infra *Infra) setupTransitGateway(ctx context.Context, logger logr.Logger, options *CreateInfraOptions, gtag *globaltaggingv1.GlobalTaggingV1) error {
	logger.Info("Setting up Transit Gateway ...")

	tgapisv1, err := transitgatewayapisv1.NewTransitGatewayApisV1(&transitgatewayapisv1.TransitGatewayApisV1Options{
		Authenticator: getIAMAuth(),
		Version:       ptr.To(currentDate),
	})

	if err != nil {
		return err
	}
	var transitGateway *transitgatewayapisv1.TransitGateway
	if options.TransitGateway != "" {
		logger.Info("Validating Transit Gateway", "name", options.TransitGateway)
		transitGateway, err = validateTransitGatewayByName(ctx, tgapisv1, options.TransitGateway, true)
		if err != nil {
			return fmt.Errorf("error validating transit gateway by name %s, %w", options.TransitGateway, err)
		}
	} else {
		transitGateway, err = infra.createTransitGateway(ctx, logger, options, tgapisv1)
		if err != nil {
			return fmt.Errorf("error creating transit gateway: %w", err)
		}
	}

	if transitGateway == nil {
		return fmt.Errorf("unable to setup transit gateway")
	}

	infra.TransitGatewayID = *transitGateway.ID
	infra.Stats.TransitGatewayState.Status = *transitGateway.Status

	if err := attachTag(gtag, options.InfraID, transitGateway.Crn, fmt.Sprintf("%s-%s", infra.ID, transitGatewayNameSuffix)); err != nil {
		logger.Error(err, "error attaching tags to transit gateway")
	}

	logger.Info("Transit Gateway Ready", "id", infra.TransitGatewayID)
	return nil
}

// createTransitGateway creates transit gateway and connections
func (infra *Infra) createTransitGateway(ctx context.Context, logger logr.Logger, options *CreateInfraOptions, tgapisv1 *transitgatewayapisv1.TransitGatewayApisV1) (*transitgatewayapisv1.TransitGateway, error) {

	transitGatewayName := fmt.Sprintf("%s-%s", options.InfraID, transitGatewayNameSuffix)

	tg, err := validateTransitGatewayByName(ctx, tgapisv1, transitGatewayName, true)
	if err != nil && err.Error() != transitGatewayNotFound(transitGatewayName).Error() {
		return nil, fmt.Errorf("error validating existing transit gateway: %w", err)
	}

	if tg != nil {
		logger.Info("Using existing transit gateway ...")
		return tg, nil
	}

	logger.Info("Creating Transit Gateway ...")
	// Checking if global routing required for transit gateway.
	globalRouting := regionutils.IsGlobalRoutingRequiredForTG(options.Region, options.VPCRegion)
	tg, _, err = tgapisv1.CreateTransitGatewayWithContext(ctx, &transitgatewayapisv1.CreateTransitGatewayOptions{
		Location:      ptr.To(options.TransitGatewayLocation),
		Name:          ptr.To(transitGatewayName),
		Global:        ptr.To(globalRouting || options.TransitGatewayGlobalRouting),
		ResourceGroup: &transitgatewayapisv1.ResourceGroupIdentity{ID: ptr.To(infra.ResourceGroupID)},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating transit gateway: %w", err)
	}

	tgVPCCon, _, err := tgapisv1.CreateTransitGatewayConnectionWithContext(ctx, &transitgatewayapisv1.CreateTransitGatewayConnectionOptions{
		TransitGatewayID: tg.ID,
		NetworkType:      ptr.To("vpc"),
		NetworkID:        ptr.To(infra.VPCCRN),
		Name:             ptr.To(fmt.Sprintf("%s-vpc-con", transitGatewayName)),
	})
	if err != nil {
		return nil, fmt.Errorf("error creating vpc connection in transit gateway: %w", err)
	}

	tgPVSCon, _, err := tgapisv1.CreateTransitGatewayConnectionWithContext(ctx, &transitgatewayapisv1.CreateTransitGatewayConnectionOptions{
		TransitGatewayID: tg.ID,
		NetworkType:      ptr.To("power_virtual_server"),
		NetworkID:        ptr.To(infra.CloudInstanceCRN),
		Name:             ptr.To(fmt.Sprintf("%s-pvs-con", transitGatewayName)),
	})
	if err != nil {
		return nil, fmt.Errorf("error creating powervs connection in transit gateway: %w", err)
	}

	isConnectionUp := func(connectionID string) error {
		f := func(ctx2 context.Context) (bool, error) {
			tgConn, _, err := tgapisv1.GetTransitGatewayConnectionWithContext(ctx2, &transitgatewayapisv1.GetTransitGatewayConnectionOptions{
				TransitGatewayID: tg.ID,
				ID:               ptr.To(connectionID),
			})
			if err != nil {
				return false, err
			}

			if *tgConn.Status == "attached" {
				return true, nil
			}

			return false, nil
		}

		if err = wait.PollUntilContextTimeout(ctx, time.Second*30, time.Minute*15, true, f); err != nil {
			return err
		}

		return nil
	}

	logger.Info("Checking VPC Connection status ...")
	if err = isConnectionUp(*tgVPCCon.ID); err != nil {
		return nil, fmt.Errorf("error checking vpc connection's status: %w", err)
	}
	logger.Info("Transit gateway VPC connection OK")

	logger.Info("Checking PowerVS Connection status ...")
	if err = isConnectionUp(*tgPVSCon.ID); err != nil {
		return nil, fmt.Errorf("error checking powervs connection's status: %w", err)
	}
	logger.Info("Transit gateway PowerVS connection OK")

	return tg, nil
}
