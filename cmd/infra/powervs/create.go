package powervs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/IBM-Cloud/power-go-client/clients/instance"
	"github.com/IBM-Cloud/power-go-client/ibmpisession"
	"github.com/IBM-Cloud/power-go-client/power/models"
	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/networking-go-sdk/zonesv1"
	"github.com/IBM/platform-services-go-sdk/globalcatalogv1"
	"github.com/IBM/platform-services-go-sdk/iamidentityv1"
	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"
	"github.com/IBM/platform-services-go-sdk/resourcemanagerv2"
	"github.com/IBM/vpc-go-sdk/vpcv1"

	"github.com/openshift/hypershift/cmd/log"
)

var cloudApiKey = os.Getenv("IBMCLOUD_API_KEY")

const (
	// Resource name suffix for creation
	cloudInstanceNameSuffix = "hypershift-nodepool"
	vpcNameSuffix           = "hypershift-vpc"
	vpcSubnetNameSuffix     = "hypershift-vpc-subnet"
	cloudConnNameSuffix     = "hypershift-cloud-connection"

	// Default cloud connection speed
	defaultCloudConnSpeed = 5000

	// CIS service name
	cisService = "internet-svcs"

	// PowerVS service and plan name
	powerVSService     = "power-iaas"
	powerVSServicePlan = "power-virtual-server-group"

	// Resource desired states
	vpcAvailableState               = "available"
	cloudInstanceActiveState        = "active"
	dhcpServiceActiveState          = "ACTIVE"
	cloudConnectionEstablishedState = "established"

	// Resource undesired state
	dhcpServiceErrorState = "ERROR"

	// Time duration for monitoring the resource readiness
	pollingInterval                  = time.Second * 5
	vpcCreationTimeout               = time.Minute * 5
	cloudInstanceCreationTimeout     = time.Minute * 5
	cloudConnEstablishedStateTimeout = time.Minute * 30
	dhcpServerCreationTimeout        = time.Minute * 30
	cloudConnUpdateTimeout           = time.Minute * 10
)

// CreateInfraOptions ...
// command line options for setting up infra in IBM PowerVS cloud
type CreateInfraOptions struct {
	BaseDomain             string
	ResourceGroup          string
	InfraID                string
	PowerVSRegion          string
	PowerVSZone            string
	PowerVSCloudInstanceID string
	PowerVSCloudConnection string
	VpcRegion              string
	Vpc                    string
	OutputFile             string
	Debug                  bool
}

type TimeDuration struct {
	time.Duration
}

// MarshalJSON ...
// custom marshaling func for time.Duration to parse Duration into user-friendly format
func (d *TimeDuration) MarshalJSON() (b []byte, err error) {
	return []byte(fmt.Sprintf(`"%s"`, d.Round(time.Millisecond).String())), nil
}

// UnmarshalJSON ...
// custom unmarshalling func for time.Duration
func (d *TimeDuration) UnmarshalJSON(b []byte) (err error) {
	d.Duration, err = time.ParseDuration(strings.Trim(string(b), `"`))
	return
}

type CreateStat struct {
	Duration TimeDuration `json:"duration"`
	Status   string       `json:"status,omitempty"`
}

type InfraCreationStat struct {
	Vpc            CreateStat `json:"vpc"`
	VpcSubnet      CreateStat `json:"vpcSubnet"`
	CloudInstance  CreateStat `json:"cloudInstance"`
	DhcpService    CreateStat `json:"dhcpService"`
	CloudConnState CreateStat `json:"cloudConnState"`
}

// Infra ...
// resource info in IBM Cloud for setting up hypershift nodepool
type Infra struct {
	ID                       string            `json:"id"`
	AccountID                string            `json:"accountID"`
	CisCrn                   string            `json:"cisCrn"`
	CisDomainID              string            `json:"cisDomainID"`
	ResourceGroupID          string            `json:"resourceGroupID"`
	PowerVSCloudInstanceID   string            `json:"powerVSCloudInstanceID"`
	PowerVSDhcpSubnetID      string            `json:"powerVSDhcpSubnetID"`
	PowerVSDhcpID            string            `json:"powerVSDhcpID"`
	PowerVSCloudConnectionID string            `json:"powerVSCloudConnectionID"`
	VpcName                  string            `json:"vpcName"`
	VpcID                    string            `json:"vpcID"`
	VpcCrn                   string            `json:"vpcCrn"`
	VpcRoutingTableID        string            `json:"-"`
	VpcSubnetName            string            `json:"vpcSubnetName"`
	VpcSubnetID              string            `json:"vpcSubnetID"`
	Stats                    InfraCreationStat `json:"stats"`
}

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "powervs",
		Short:        "Creates PowerVS infrastructure resources for a cluster",
		SilenceUsage: true,
	}

	opts := CreateInfraOptions{
		PowerVSRegion: "us-south",
		PowerVSZone:   "us-south",
		VpcRegion:     "us-south",
	}

	cmd.Flags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "IBM Cloud CIS Domain")
	cmd.Flags().StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "IBM Cloud Resource Group")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID with which to tag IBM Cloud resources")
	cmd.Flags().StringVar(&opts.PowerVSRegion, "powervs-region", opts.PowerVSRegion, "IBM Cloud PowerVS Region")
	cmd.Flags().StringVar(&opts.PowerVSZone, "powervs-zone", opts.PowerVSZone, "IBM Cloud PowerVS Zone")
	cmd.Flags().StringVar(&opts.PowerVSCloudInstanceID, "powervs-cloud-instance-id", opts.PowerVSCloudInstanceID, "IBM PowerVS Cloud Instance ID")
	cmd.Flags().StringVar(&opts.VpcRegion, "vpc-region", opts.VpcRegion, "IBM Cloud VPC Region for VPC resources")
	cmd.Flags().StringVar(&opts.Vpc, "vpc", opts.Vpc, "IBM Cloud VPC Name")
	cmd.Flags().StringVar(&opts.PowerVSCloudConnection, "powervs-cloud-connection", opts.PowerVSCloudConnection, "IBM Cloud PowerVS Cloud Connection")
	cmd.Flags().StringVar(&opts.OutputFile, "output-file", opts.OutputFile, "Path to file that will contain output information from infra resources (optional)")
	cmd.Flags().BoolVar(&opts.Debug, "debug", opts.Debug, "Enabling this will print PowerVS API Request & Response logs")

	// these options are only for development and testing purpose,
	// can use these to reuse the existing resources, so hiding it.
	// for using these flags, the connection b/w all the resources should be pre-set up properly
	// e.g. cloud instance should contain a cloud connection attached to the dhcp server and provided vpc
	cmd.Flags().MarkHidden("powervs-cloud-instance-id")
	cmd.Flags().MarkHidden("vpc")
	cmd.Flags().MarkHidden("powervs-cloud-connection")

	cmd.MarkFlagRequired("base-domain")
	cmd.MarkFlagRequired("resource-group")
	cmd.MarkFlagRequired("infra-id")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context()); err != nil {
			log.Log.Error(err, "Failed to create infrastructure")
			return err
		}
		log.Log.Info("Successfully created infrastructure")
		return nil
	}

	return cmd
}

func (options *CreateInfraOptions) Run(ctx context.Context) (err error) {
	infra := &Infra{ID: options.InfraID}

	defer func() {
		out := os.Stdout
		if len(options.OutputFile) > 0 {
			var err error
			out, err = os.Create(options.OutputFile)
			if err != nil {
				log.Log.WithName(options.InfraID).Error(err, "cannot create output file")
			}
			defer out.Close()
		}
		outputBytes, err := json.MarshalIndent(infra, "", "  ")
		if err != nil {
			log.Log.WithName(options.InfraID).WithName(options.InfraID).Error(err, "failed to serialize output infra")
		}
		_, err = out.Write(outputBytes)
		if err != nil {
			log.Log.WithName(options.InfraID).Error(err, "failed to write output infra json")
		}
	}()

	err = infra.SetupInfra(options)
	if err != nil {
		return err
	}

	return nil
}

// SetupInfra ...
// infra creation orchestration
func (infra *Infra) SetupInfra(options *CreateInfraOptions) (err error) {
	startTime := time.Now()

	log.Log.WithName(options.InfraID).Info("Setup infra started")

	// if IBMCLOUD_API_KEY is not set, infra cannot be set up.
	if cloudApiKey == "" {
		return fmt.Errorf("IBMCLOUD_API_KEY not set")
	}

	infra.ResourceGroupID, err = getResourceGroupID(options.ResourceGroup)
	if err != nil {
		return fmt.Errorf("error getting id for resource group %s, %w", options.ResourceGroup, err)
	}

	err = infra.setupBaseDomain(options)
	if err != nil {
		return fmt.Errorf("error setup base domain: %w", err)
	}

	v1, err := createVpcService(options.VpcRegion, options.InfraID)
	if err != nil {
		return fmt.Errorf("error creating vpc service: %w", err)
	}

	err = infra.setupVpc(options, v1)
	if err != nil {
		return fmt.Errorf("error setup vpc: %w", err)
	}

	err = infra.setupVpcSubnet(options, v1)
	if err != nil {
		return fmt.Errorf("error setup vpc subnet: %w", err)
	}

	session, err := createPowerVSSession(options.PowerVSRegion, options.PowerVSZone, options.Debug)
	infra.AccountID = session.Options.UserAccount
	if err != nil {
		return fmt.Errorf("error creating powervs session: %w", err)
	}

	err = infra.setupPowerVSCloudInstance(options)
	if err != nil {
		return fmt.Errorf("error setup powervs cloud instance: %w", err)
	}

	err = infra.setupPowerVSCloudConnection(options, session)
	if err != nil {
		return fmt.Errorf("error setup powervs cloud connection: %w", err)
	}

	err = infra.setupPowerVSDhcp(options, session)
	if err != nil {
		return fmt.Errorf("error setup powervs dhcp server: %w", err)
	}

	err = infra.isCloudConnectionReady(options, session)
	if err != nil {
		return fmt.Errorf("cloud connection is not up: %w", err)
	}

	log.Log.WithName(options.InfraID).Info("Setup infra completed in", "duration", time.Since(startTime).String())
	return
}

// getIAMAuth...
// getting core.Authenticator
func getIAMAuth() *core.IamAuthenticator {
	return &core.IamAuthenticator{
		ApiKey: cloudApiKey,
	}
}

// setupBaseDomain ...
// get domain id and crn of given base domain
func (infra *Infra) setupBaseDomain(options *CreateInfraOptions) (err error) {
	rcv2, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{Authenticator: getIAMAuth()})
	if err != nil {
		return
	}

	if rcv2 == nil {
		return fmt.Errorf("unable to get resource controller")
	}

	// getting list of resource instance of type cis
	serviceID, _, err := getServiceInfo(cisService, "")

	if err != nil {
		err = fmt.Errorf("error retrieving cis service %w", err)
		return
	}

	f := func(start string) (isDone bool, nextUrl string, err error) {
		listResourceOpt := resourcecontrollerv2.ListResourceInstancesOptions{ResourceID: &serviceID}

		// for getting the next page
		if start != "" {
			listResourceOpt.Start = &start
		}
		resourceList, _, err := rcv2.ListResourceInstances(&listResourceOpt)

		if err != nil {
			return
		}

		if resourceList == nil {
			err = fmt.Errorf("resourceList returned is nil")
			return
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
			zoneList, _, err = zv1.ListZones(&zonesv1.ListZonesOptions{})
			if err != nil {
				continue
			}

			if zoneList != nil {
				for _, zone := range zoneList.Result {
					if *zone.Name == options.BaseDomain {
						infra.CisCrn = *resource.CRN
						infra.CisDomainID = *zone.ID
						isDone = true
						return
					}
				}
			}
		}

		// For paging over next set of resources getting the start token
		if resourceList.NextURL != nil || *resourceList.NextURL != "" {
			nextUrl = *resourceList.NextURL
			return
		}

		isDone = true
		return
	}

	err = pagingHelper(f)
	if err != nil {
		return
	}

	if infra.CisCrn == "" || infra.CisDomainID == "" {
		return fmt.Errorf("unable to get cis information with base domain %s", options.BaseDomain)
	}

	log.Log.WithName(options.InfraID).Info("BaseDomain Info Ready", "CRN", infra.CisCrn, "DomainID", infra.CisDomainID)
	return
}

// getServiceInfo ...
// retrieving id info of given service and service plan
func getServiceInfo(service string, servicePlan string) (serviceID string, servicePlanID string, err error) {
	gcv1, err := globalcatalogv1.NewGlobalCatalogV1(&globalcatalogv1.GlobalCatalogV1Options{Authenticator: getIAMAuth()})
	if err != nil {
		return
	}

	if gcv1 == nil {
		err = fmt.Errorf("unable to get global catalog")
		return
	}

	// TO-DO need to explore paging for catalog list since ListCatalogEntriesOptions does not take start
	include := "*"
	listCatalogEntriesOpt := globalcatalogv1.ListCatalogEntriesOptions{Include: &include, Q: &service}
	catalogEntriesList, _, err := gcv1.ListCatalogEntries(&listCatalogEntriesOpt)
	if err != nil {
		return
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
		childObjResult, _, err = gcv1.GetChildObjects(&getChildOpt)
		if err != nil {
			return
		}
		for _, plan := range childObjResult.Resources {
			if *plan.Name == servicePlan {
				servicePlanID = *plan.ID
				return
			}
		}
	}
	err = fmt.Errorf("could not retrieve plan id for service name: %s & service plan name: %s", service, servicePlan)
	return
}

// getResourceGroupID ...
// retrieving id of resource group
func getResourceGroupID(resourceGroup string) (resourceGroupID string, err error) {
	rmv2, err := resourcemanagerv2.NewResourceManagerV2(&resourcemanagerv2.ResourceManagerV2Options{Authenticator: getIAMAuth()})
	if err != nil {
		return
	}

	if rmv2 == nil {
		err = fmt.Errorf("unable to get resource controller")
		return
	}

	rmv2ListResourceGroupOpt := resourcemanagerv2.ListResourceGroupsOptions{Name: &resourceGroup}
	resourceGroupListResult, _, err := rmv2.ListResourceGroups(&rmv2ListResourceGroupOpt)
	if err != nil {
		return
	}

	if resourceGroupListResult != nil {
		for _, rg := range resourceGroupListResult.Resources {
			if *rg.Name == resourceGroup {
				resourceGroupID = *rg.ID
				return
			}
		}
	}

	err = fmt.Errorf("could not retrieve resource group id for %s", resourceGroup)
	return
}

// createCloudInstance ...
// creating powervs cloud instance
func (infra *Infra) createCloudInstance(options *CreateInfraOptions) (resourceInstance *resourcecontrollerv2.ResourceInstance, err error) {

	rcv2, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{Authenticator: getIAMAuth()})
	if err != nil {
		return
	}

	if rcv2 == nil {
		err = fmt.Errorf("unable to get resource controller")
		return
	}

	serviceID, servicePlanID, err := getServiceInfo(powerVSService, powerVSServicePlan)
	if err != nil {
		err = fmt.Errorf("error retrieving id info for powervs service %w", err)
		return
	}

	cloudInstanceName := fmt.Sprintf("%s-%s", options.InfraID, cloudInstanceNameSuffix)

	// validate if already a cloud instance available with the infra provided
	// if yes, make use of that instead of trying to create a new one
	resourceInstance, err = validateCloudInstanceByName(cloudInstanceName, infra.ResourceGroupID, options.PowerVSZone, serviceID, servicePlanID)

	if resourceInstance != nil {
		log.Log.WithName(options.InfraID).Info("Using existing PowerVS Cloud Instance", "name", cloudInstanceName)
		return
	}

	log.Log.WithName(options.InfraID).Info("Creating PowerVS Cloud Instance ...")
	target := options.PowerVSZone

	resourceInstanceOpt := resourcecontrollerv2.CreateResourceInstanceOptions{Name: &cloudInstanceName,
		ResourceGroup:  &infra.ResourceGroupID,
		ResourcePlanID: &servicePlanID,
		Target:         &target}

	startTime := time.Now()
	resourceInstance, _, err = rcv2.CreateResourceInstance(&resourceInstanceOpt)
	if err != nil {
		return
	}

	if resourceInstance == nil {
		err = fmt.Errorf("create cloud instance returned nil")
		return
	}

	if *resourceInstance.State == cloudInstanceActiveState {
		return
	}

	f := func() (cond bool, err error) {
		resourceInstance, _, err = rcv2.GetResourceInstance(&resourcecontrollerv2.GetResourceInstanceOptions{ID: resourceInstance.ID})
		log.Log.WithName(options.InfraID).Info("Waiting for cloud instance to up", "id", resourceInstance.ID, "state", *resourceInstance.State)

		if err != nil {
			return
		}

		if *resourceInstance.State == cloudInstanceActiveState {
			cond = true
			return
		}
		return
	}

	err = wait.PollImmediate(pollingInterval, cloudInstanceCreationTimeout, f)

	infra.Stats.CloudInstance.Duration.Duration = time.Since(startTime)

	return
}

// getAccount ...
// getting the account id from core.Authenticator
func getAccount(auth core.Authenticator) (accountID string, err error) {
	iamv1, err := iamidentityv1.NewIamIdentityV1(&iamidentityv1.IamIdentityV1Options{Authenticator: auth})
	if err != nil {
		return
	}

	apiKeyDetailsOpt := iamidentityv1.GetAPIKeysDetailsOptions{IamAPIKey: &cloudApiKey}
	apiKey, _, err := iamv1.GetAPIKeysDetails(&apiKeyDetailsOpt)
	if err != nil {
		return
	}
	if apiKey == nil {
		err = fmt.Errorf("could retrieve account id")
		return
	}

	accountID = *apiKey.AccountID
	return
}

// createPowerVSSession ...
// creates PowerVSSession of type *ibmpisession.IBMPISession
func createPowerVSSession(powerVSRegion string, powerVSZone string, debug bool) (session *ibmpisession.IBMPISession, err error) {
	auth := getIAMAuth()
	account, err := getAccount(auth)

	if err != nil {
		return
	}

	opt := &ibmpisession.IBMPIOptions{Authenticator: auth,
		Debug:       debug,
		Region:      powerVSRegion,
		UserAccount: account,
		Zone:        powerVSZone}

	session, err = ibmpisession.NewIBMPISession(opt)
	return
}

// createVpcService ...
// creates VpcService of type *vpcv1.VpcV1
func createVpcService(region string, infraID string) (v1 *vpcv1.VpcV1, err error) {
	v1, err = vpcv1.NewVpcV1(&vpcv1.VpcV1Options{
		ServiceName:   "vpcs",
		Authenticator: getIAMAuth(),
		URL:           fmt.Sprintf("https://%s.iaas.cloud.ibm.com/v1", region),
	})
	log.Log.WithName(infraID).Info("Created VPC Service for", "URL", v1.GetServiceURL())
	return
}

// setupPowerVSCloudInstance ...
// takes care of setting up powervs cloud instance
func (infra *Infra) setupPowerVSCloudInstance(options *CreateInfraOptions) (err error) {
	log.Log.WithName(options.InfraID).Info("Setting up PowerVS Cloud Instance ...")
	var cloudInstance *resourcecontrollerv2.ResourceInstance
	if options.PowerVSCloudInstanceID != "" {
		log.Log.WithName(options.InfraID).Info("Validating PowerVS Cloud Instance", "id", options.PowerVSCloudInstanceID)
		cloudInstance, err = validateCloudInstanceByID(options.PowerVSCloudInstanceID)
		if err != nil {
			return fmt.Errorf("error validating cloud instance id %s, %w", options.PowerVSCloudInstanceID, err)
		}
	} else {
		cloudInstance, err = infra.createCloudInstance(options)
		if err != nil {
			return fmt.Errorf("error creating cloud instance: %w", err)
		}
	}

	if cloudInstance != nil {
		infra.PowerVSCloudInstanceID = *cloudInstance.GUID
		infra.Stats.CloudInstance.Status = *cloudInstance.State

	}

	if infra.PowerVSCloudInstanceID == "" {
		return fmt.Errorf("unable to setup powervs cloud instance")
	}

	log.Log.WithName(options.InfraID).Info("PowerVS Cloud Instance Ready", "id", infra.PowerVSCloudInstanceID)
	return
}

// setupVpc ...
// takes care of setting up vpc
func (infra *Infra) setupVpc(options *CreateInfraOptions, v1 *vpcv1.VpcV1) (err error) {
	log.Log.WithName(options.InfraID).Info("Setting up VPC ...")
	var vpc *vpcv1.VPC
	if options.Vpc != "" {
		log.Log.WithName(options.InfraID).Info("Validating VPC", "name", options.Vpc)
		vpc, err = validateVpc(options.Vpc, infra.ResourceGroupID, v1)
		if err != nil {
			return
		}
	} else {
		vpc, err = infra.createVpc(options, infra.ResourceGroupID, v1)
		if err != nil {
			return
		}
	}

	if vpc != nil {
		infra.VpcName = *vpc.Name
		infra.VpcID = *vpc.ID
		infra.VpcCrn = *vpc.CRN
		infra.VpcRoutingTableID = *vpc.DefaultRoutingTable.ID
		infra.Stats.Vpc.Status = *vpc.Status
	}

	if infra.VpcID == "" {
		return fmt.Errorf("unable to setup vpc")
	}

	log.Log.WithName(options.InfraID).Info("VPC Ready", "ID", infra.VpcID)
	return
}

// createVpc ...
// creates a new vpc with the infra name or will return an existing vpc
func (infra *Infra) createVpc(options *CreateInfraOptions, resourceGroupID string, v1 *vpcv1.VpcV1) (vpc *vpcv1.VPC, err error) {
	var startTime time.Time
	vpcName := fmt.Sprintf("%s-%s", options.InfraID, vpcNameSuffix)
	vpc, err = validateVpc(vpcName, resourceGroupID, v1)

	// if vpc already exist use it or proceed with creating a new one, no need to validate err
	if vpc != nil && *vpc.Name == vpcName {
		log.Log.WithName(options.InfraID).Info("Using existing VPC", "name", vpcName)
		return
	}

	log.Log.WithName(options.InfraID).Info("Creating VPC ...")
	addressPrefixManagement := "auto"

	vpcOption := &vpcv1.CreateVPCOptions{
		ResourceGroup:           &vpcv1.ResourceGroupIdentity{ID: &resourceGroupID},
		Name:                    &vpcName,
		AddressPrefixManagement: &addressPrefixManagement,
	}

	startTime = time.Now()
	vpc, _, err = v1.CreateVPC(vpcOption)
	if err != nil {
		return
	}

	f := func() (cond bool, err error) {

		vpc, _, err = v1.GetVPC(&vpcv1.GetVPCOptions{ID: vpc.ID})
		if err != nil {
			return
		}

		if *vpc.Status == vpcAvailableState {
			cond = true
			return
		}
		return
	}

	err = wait.PollImmediate(pollingInterval, vpcCreationTimeout, f)

	if !startTime.IsZero() && vpc != nil {
		infra.Stats.Vpc.Duration.Duration = time.Since(startTime)
	}

	return
}

// setupVpcSubnet ...
// takes care of setting up subnet in the vpc
func (infra *Infra) setupVpcSubnet(options *CreateInfraOptions, v1 *vpcv1.VpcV1) (err error) {
	log.Log.WithName(options.InfraID).Info("Setting up VPC Subnet ...")

	log.Log.WithName(options.InfraID).Info("Getting existing VPC Subnet info ...")
	var subnet *vpcv1.Subnet
	f := func(start string) (isDone bool, nextUrl string, err error) {
		// check for existing subnets
		listSubnetOpt := vpcv1.ListSubnetsOptions{ResourceGroupID: &infra.ResourceGroupID, RoutingTableID: &infra.VpcRoutingTableID}
		if start != "" {
			listSubnetOpt.Start = &start
		}

		vpcSubnetL, _, err := v1.ListSubnets(&listSubnetOpt)
		if err != nil {
			return
		}

		if vpcSubnetL == nil {
			err = fmt.Errorf("subnet list returned is nil")
			return
		}

		if len(vpcSubnetL.Subnets) > 0 {
			for _, sn := range vpcSubnetL.Subnets {
				if *sn.VPC.ID == infra.VpcID {
					infra.VpcSubnetName = *sn.Name
					infra.VpcSubnetID = *sn.ID
					subnet = &sn
					isDone = true
					return
				}
			}
		}

		if vpcSubnetL.Next != nil && *vpcSubnetL.Next.Href != "" {
			nextUrl = *vpcSubnetL.Next.Href
			return
		}
		isDone = true
		return
	}

	// if subnet already exist use it or proceed with creating a new one, no need to validate err
	_ = pagingHelper(f)

	if infra.VpcSubnetID == "" {
		subnet, err = infra.createVpcSubnet(options, v1)
		if err != nil {
			return
		}
		infra.VpcSubnetName = *subnet.Name
		infra.VpcSubnetID = *subnet.ID
	}

	if subnet != nil {
		infra.Stats.VpcSubnet.Status = *subnet.Status
	}

	log.Log.WithName(options.InfraID).Info("VPC Subnet Ready", "ID", infra.VpcSubnetID)
	return
}

// createVpcSubnet ...
// creates a new subnet in vpc with the infra name or will return an existing subnet in the vpc
func (infra *Infra) createVpcSubnet(options *CreateInfraOptions, v1 *vpcv1.VpcV1) (subnet *vpcv1.Subnet, err error) {
	log.Log.WithName(options.InfraID).Info("Create VPC Subnet ...")
	var startTime time.Time
	vpcIdent := &vpcv1.VPCIdentity{CRN: &infra.VpcCrn}
	resourceGroupIdent := &vpcv1.ResourceGroupIdentity{ID: &infra.ResourceGroupID}
	subnetName := fmt.Sprintf("%s-%s", options.InfraID, vpcSubnetNameSuffix)
	ipVersion := "ipv4"
	zones, _, err := v1.ListRegionZones(&vpcv1.ListRegionZonesOptions{RegionName: &options.VpcRegion})
	if err != nil {
		return
	}

	addressPrefixL, _, err := v1.ListVPCAddressPrefixes(&vpcv1.ListVPCAddressPrefixesOptions{VPCID: &infra.VpcID})
	if err != nil {
		return
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
		subnet, _, err = v1.CreateSubnet(&vpcv1.CreateSubnetOptions{SubnetPrototype: subnetProto})
		if err != nil {
			continue
		}
		break
	}

	if subnet == nil {
		err = fmt.Errorf("CreateSubnet returned nil")
		return
	}

	f := func() (cond bool, err error) {

		subnet, _, err = v1.GetSubnet(&vpcv1.GetSubnetOptions{ID: subnet.ID})
		if err != nil {
			return
		}

		if *subnet.Status == vpcAvailableState {
			cond = true
			return
		}
		return
	}

	err = wait.PollImmediate(pollingInterval, vpcCreationTimeout, f)

	if !startTime.IsZero() {
		infra.Stats.VpcSubnet.Duration.Duration = time.Since(startTime)
	}

	return
}

// setupPowerVSCloudConnection ...
// takes care of setting up cloud connection in powervs
func (infra *Infra) setupPowerVSCloudConnection(options *CreateInfraOptions, session *ibmpisession.IBMPISession) (err error) {
	log.Log.WithName(options.InfraID).Info("Setting up PowerVS Cloud Connection ...")
	client := instance.NewIBMPICloudConnectionClient(context.Background(), session, infra.PowerVSCloudInstanceID)
	var cloudConnID string
	if options.PowerVSCloudConnection != "" {
		log.Log.WithName(options.InfraID).Info("Validating PowerVS Cloud Connection", "name", options.PowerVSCloudConnection)
		cloudConnID, err = validateCloudConnectionByName(options.PowerVSCloudConnection, client)
		if err != nil {
			return
		}
	} else {
		cloudConnID, err = infra.createCloudConnection(options, client)
		if err != nil {
			return
		}
	}
	if cloudConnID != "" {
		infra.PowerVSCloudConnectionID = cloudConnID
	}

	if infra.PowerVSCloudConnectionID == "" {
		err = fmt.Errorf("unable to setup powervs cloud connection")
		return
	}

	log.Log.WithName(options.InfraID).Info("PowerVS Cloud Connection Ready", "id", infra.PowerVSCloudConnectionID)
	return
}

// createCloudConnection ...
// creates a new cloud connection with the infra name or will return an existing cloud connection
func (infra *Infra) createCloudConnection(options *CreateInfraOptions, client *instance.IBMPICloudConnectionClient) (cloudConnID string, err error) {
	cloudConnName := fmt.Sprintf("%s-%s", options.InfraID, cloudConnNameSuffix)

	// validating existing cloud connection with the infra
	cloudConnID, err = validateCloudConnectionInPowerVSZone(cloudConnName, client)
	if err != nil {
		return
	} else if cloudConnID != "" {
		// if exists, use that and from func isCloudConnectionReady() make the connection to dhcp private network and vpc if not exists already
		log.Log.WithName(options.InfraID).Info("Using existing PowerVS Cloud Connection", "name", cloudConnName)
		return
	}

	log.Log.WithName(options.InfraID).Info("Creating PowerVS Cloud Connection ...")

	var speed int64 = defaultCloudConnSpeed
	var vpcL []*models.CloudConnectionVPC
	vpcCrn := infra.VpcCrn
	vpcL = append(vpcL, &models.CloudConnectionVPC{VpcID: &vpcCrn})

	cloudConnectionEndpointVPC := models.CloudConnectionEndpointVPC{Enabled: true, Vpcs: vpcL}

	cloudConn, cloudConnRespAccepted, err := client.Create(&models.CloudConnectionCreate{Name: &cloudConnName, GlobalRouting: true, Speed: &speed, Vpc: &cloudConnectionEndpointVPC})

	if err != nil {
		return
	}
	if cloudConn != nil {
		cloudConnID = *cloudConn.CloudConnectionID
	} else if cloudConnRespAccepted != nil {
		cloudConnID = *cloudConnRespAccepted.CloudConnectionID
	} else {
		err = fmt.Errorf("could not get cloud connection id")
		return
	}

	return
}

// setupPowerVSDhcp ...
// takes care of setting up dhcp in powervs
func (infra *Infra) setupPowerVSDhcp(options *CreateInfraOptions, session *ibmpisession.IBMPISession) (err error) {
	log.Log.WithName(infra.ID).Info("Setting up PowerVS DHCP ...")
	client := instance.NewIBMPIDhcpClient(context.Background(), session, infra.PowerVSCloudInstanceID)

	var dhcpServer *models.DHCPServerDetail

	dhcpServers, err := client.GetAll()
	if err != nil {
		return
	}

	// only one dhcp server is allowed per cloud instance
	// if already a dhcp server existing in cloud instance use that instead of creating a new one
	if len(dhcpServers) > 0 {
		for _, dhcp := range dhcpServers {
			log.Log.WithName(infra.ID).Info("Using existing DHCP server present in cloud instance")
			dhcpServer = &models.DHCPServerDetail{ID: dhcp.ID, Status: dhcp.Status, Network: dhcp.Network}
			break
		}
	} else {
		log.Log.WithName(infra.ID).Info("Creating PowerVS DhcpServer...")
		dhcpServer, err = infra.createPowerVSDhcp(options, client)
		if err != nil {
			return
		}
	}

	if dhcpServer != nil {
		infra.PowerVSDhcpID = *dhcpServer.ID
		if *dhcpServer.Status == dhcpServiceActiveState && dhcpServer.Network != nil {
			infra.PowerVSDhcpSubnetID = *dhcpServer.Network.ID
		}
		infra.Stats.DhcpService.Status = *dhcpServer.Status
	}

	if infra.PowerVSDhcpID == "" && infra.PowerVSDhcpSubnetID == "" {
		return fmt.Errorf("unable to setup powervs dhcp server and private subnet")
	}

	log.Log.WithName(infra.ID).Info("PowerVS DHCP Server and Private Subnet  Ready", "dhcpServerId", infra.PowerVSDhcpID, "dhcpPrivateSubnetId", infra.PowerVSDhcpSubnetID)
	return
}

// createPowerVSDhcp ...
// creates a new dhcp server in powervs
func (infra *Infra) createPowerVSDhcp(options *CreateInfraOptions, client *instance.IBMPIDhcpClient) (dhcpServer *models.DHCPServerDetail, err error) {
	startTime := time.Now()
	dhcp, err := client.Create(&models.DHCPServerCreate{CloudConnectionID: infra.PowerVSCloudConnectionID})
	if err != nil {
		return
	}

	if dhcp == nil {
		err = fmt.Errorf("created dhcp server is nil")
		return
	}

	f := func() (cond bool, err error) {
		dhcpServer, err = client.Get(*dhcp.ID)
		if err != nil {
			return
		}

		if dhcpServer != nil {
			log.Log.WithName(infra.ID).Info("Waiting for DhcpServer to up", "id", *dhcpServer.ID, "status", *dhcpServer.Status)
			if *dhcpServer.Status == dhcpServiceActiveState {
				cond = true
				return
			}

			if *dhcpServer.Status == dhcpServiceErrorState {
				err = fmt.Errorf("dhcp service is in error state")
				return
			}
		}

		return
	}

	err = wait.PollImmediate(pollingInterval, dhcpServerCreationTimeout, f)

	if dhcpServer != nil {
		infra.Stats.DhcpService.Duration.Duration = time.Since(startTime)
	}
	return
}

// isCloudConnectionReady ...
//make sure cloud connection is connected with dhcp server private network and vpc, and it is in established state
func (infra *Infra) isCloudConnectionReady(options *CreateInfraOptions, session *ibmpisession.IBMPISession) (err error) {
	log.Log.WithName(infra.ID).Info("Making sure PowerVS Cloud Connection is ready ...")
	client := instance.NewIBMPICloudConnectionClient(context.Background(), session, infra.PowerVSCloudInstanceID)
	jobClient := instance.NewIBMPIJobClient(context.Background(), session, infra.PowerVSCloudInstanceID)
	var cloudConn *models.CloudConnection

	startTime := time.Now()
	cloudConn, err = client.Get(infra.PowerVSCloudConnectionID)
	if err != nil {
		return
	}

	// To ensure vpc and dhcp private subnet is attached to cloud connection
	cloudConnNwOk := false
	cloudConnVpcOk := false

	if cloudConn != nil {
		for _, nw := range cloudConn.Networks {
			if *nw.NetworkID == infra.PowerVSDhcpSubnetID {
				cloudConnNwOk = true
			}
		}

		for _, vpc := range cloudConn.Vpc.Vpcs {
			if *vpc.VpcID == infra.VpcCrn {
				cloudConnVpcOk = true
			}
		}
	}

	if !cloudConnVpcOk {
		log.Log.WithName(infra.ID).Info("Updating VPC to cloud connection")
		cloudConnUpdateOpt := models.CloudConnectionUpdate{}

		var vpcL []*models.CloudConnectionVPC
		vpcCrn := infra.VpcCrn
		vpcL = append(vpcL, &models.CloudConnectionVPC{VpcID: &vpcCrn})

		cloudConnUpdateOpt.Vpc = &models.CloudConnectionEndpointVPC{Enabled: true, Vpcs: vpcL}

		enableGR := true
		cloudConnUpdateOpt.GlobalRouting = &enableGR

		_, job, err := client.Update(*cloudConn.CloudConnectionID, &cloudConnUpdateOpt)
		if err != nil {
			log.Log.WithName(infra.ID).Error(err, "error updating cloud connection with vpc")
			return fmt.Errorf("error updating cloud connection with vpc %w", err)
		}
		err = monitorPowerVsJob(*job.ID, jobClient, infra.PowerVSCloudInstanceID, cloudConnUpdateTimeout)
		if err != nil {
			log.Log.WithName(infra.ID).Error(err, "error attaching cloud connection with vpc")
			return fmt.Errorf("error attaching cloud connection with dhcp subnet %w", err)
		}
	}

	if !cloudConnNwOk {
		log.Log.WithName(infra.ID).Info("Adding DHCP private network to cloud connection")
		_, job, err := client.AddNetwork(*cloudConn.CloudConnectionID, infra.PowerVSDhcpSubnetID)
		if err != nil {
			log.Log.WithName(infra.ID).Error(err, "error attaching cloud connection with dhcp subnet")
			return fmt.Errorf("error attaching cloud connection with dhcp subnet %w", err)
		}
		err = monitorPowerVsJob(*job.ID, jobClient, infra.PowerVSCloudInstanceID, cloudConnUpdateTimeout)
		if err != nil {
			log.Log.WithName(infra.ID).Error(err, "error attaching cloud connection with dhcp subnet")
			return fmt.Errorf("error attaching cloud connection with dhcp subnet %w", err)
		}
	}

	f := func() (cond bool, err error) {
		cloudConn, err = client.Get(infra.PowerVSCloudConnectionID)
		if err != nil {
			return
		}

		if cloudConn != nil {
			log.Log.WithName(infra.ID).Info("Waiting for Cloud Connection to up", "id", cloudConn.CloudConnectionID, "status", cloudConn.LinkStatus)
			if *cloudConn.LinkStatus == cloudConnectionEstablishedState {
				cond = true
				return
			}
		}

		return
	}

	err = wait.PollImmediate(pollingInterval, cloudConnEstablishedStateTimeout, f)
	if cloudConn != nil {
		infra.Stats.CloudConnState.Duration.Duration = time.Since(startTime)
		infra.Stats.CloudConnState.Status = *cloudConn.LinkStatus
	}
	if err == nil {
		log.Log.WithName(infra.ID).Info("PowerVS Cloud Connection ready")
		return
	}

	return
}
