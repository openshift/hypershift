package powervs

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/openshift/hypershift/cmd/log"

	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	"github.com/IBM-Cloud/power-go-client/clients/instance"
	"github.com/IBM-Cloud/power-go-client/ibmpisession"
	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/awserr"
	"github.com/IBM/ibm-cos-sdk-go/aws/credentials/ibmiam"
	cosSession "github.com/IBM/ibm-cos-sdk-go/aws/session"
	"github.com/IBM/ibm-cos-sdk-go/service/s3"
	"github.com/IBM/ibm-cos-sdk-go/service/s3/s3manager"
	"github.com/IBM/networking-go-sdk/dnsrecordsv1"
	"github.com/IBM/networking-go-sdk/transitgatewayapisv1"
	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"
	"github.com/IBM/vpc-go-sdk/vpcv1"
	"github.com/go-logr/logr"
	regionutils "github.com/ppc64le-cloud/powervs-utils"
	"github.com/spf13/cobra"
	"golang.org/x/net/http/httpproxy"
)

const (
	// Time duration for monitoring the resource readiness
	cloudInstanceDeletionTimeout   = time.Minute * 10
	powerVSResourceDeletionTimeout = time.Minute * 5
	vpcResourceDeletionTimeout     = time.Minute * 2
	dhcpServerDeletionTimeout      = time.Minute * 10

	// Resource desired states
	powerVSCloudInstanceRemovedState = "removed"
	powerVSJobCompletedState         = "completed"
	powerVSJobFailedState            = "failed"

	// Resource name prefix
	vpcLbNamePrefix = "kube"

	// IAM endpoint for creating COS session
	iamEndpoint = "https://iam.cloud.ibm.com/identity/token"
)

// DestroyInfraOptions command line options to destroy infra created in IBMCloud for Hypershift
type DestroyInfraOptions struct {
	Name                   string
	Namespace              string
	InfraID                string
	InfrastructureJson     string
	BaseDomain             string
	CISCRN                 string
	CISDomainID            string
	ResourceGroup          string
	Region                 string
	Zone                   string
	CloudInstanceID        string
	DHCPID                 string
	VPCRegion              string
	VPC                    string
	Debug                  bool
	TransitGatewayLocation string
	TransitGateway         string
}

func NewDestroyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "powervs",
		Short:        "Destroys PowerVS infrastructure resources for a cluster",
		SilenceUsage: true,
	}

	opts := DestroyInfraOptions{
		Namespace: "clusters",
		Name:      "example",
	}

	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "Name of the cluster")
	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "A namespace to contain the generated resources")
	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID with which to tag IBM Cloud resources")
	cmd.Flags().StringVar(&opts.InfrastructureJson, "infra-json", opts.InfrastructureJson, "Result of ./hypershift infra create powervs")
	cmd.Flags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "The ingress base domain of the cluster")
	cmd.Flags().StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "IBM Cloud Resource Group")
	cmd.Flags().StringVar(&opts.VPCRegion, "vpc-region", opts.VPCRegion, "IBM Cloud VPC Infra Region")
	cmd.Flags().StringVar(&opts.VPC, "vpc", opts.VPC, "IBM Cloud VPC. Use this flag to reuse an existing VPC resource for cluster's infra")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "IBM Cloud PowerVS Region")
	cmd.Flags().StringVar(&opts.Zone, "zone", opts.Zone, "IBM Cloud PowerVS Zone")
	cmd.Flags().StringVar(&opts.CloudInstanceID, "cloud-instance-id", opts.CloudInstanceID, "IBM PowerVS Cloud Instance ID. Use this flag to reuse an existing PowerVS Cloud Instance resource for cluster's infra")
	cmd.Flags().BoolVar(&opts.Debug, "debug", opts.Debug, "Enabling this will result in debug logs will be printed")
	cmd.Flags().StringVar(&opts.TransitGatewayLocation, "transit-gateway-location", opts.TransitGatewayLocation, "IBM Cloud Transit Gateway location")
	cmd.Flags().StringVar(&opts.TransitGateway, "transit-gateway", opts.TransitGateway, "IBM Cloud Transit Gateway. Use this flag to reuse an existing Transit Gateway resource for cluster's infra")

	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("resource-group")
	_ = cmd.MarkFlagRequired("base-domain")
	_ = cmd.MarkFlagRequired("infra-id")
	_ = cmd.MarkFlagRequired("region")
	_ = cmd.MarkFlagRequired("zone")
	_ = cmd.MarkFlagRequired("vpc-region")

	// these options are only for development and testing purpose, user can pass these flags
	// to destroy the resource created inside these resources for hypershift infra purpose
	_ = cmd.Flags().MarkHidden("vpc")
	_ = cmd.Flags().MarkHidden("cloud-instance-id")
	_ = cmd.Flags().MarkHidden("transit-gateway")

	logger := log.Log.WithName(opts.InfraID)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context(), logger); err != nil {
			logger.Error(err, "Failed to destroy infrastructure")
			return err
		}
		logger.Info("Successfully destroyed infrastructure")
		return nil
	}

	return cmd
}

// Run Hypershift Infra Destroy
func (options *DestroyInfraOptions) Run(ctx context.Context, logger logr.Logger) error {
	if options.TransitGatewayLocation == "" {
		return fmt.Errorf("transit gateway location is required")
	}

	var infra *Infra
	if len(options.InfrastructureJson) > 0 {
		rawInfra, err := os.ReadFile(options.InfrastructureJson)
		if err != nil {
			return fmt.Errorf("failed to read infra json file: %w", err)
		}
		infra = &Infra{}
		if err = json.Unmarshal(rawInfra, infra); err != nil {
			return fmt.Errorf("failed to load infra json: %w", err)
		}
	}

	return options.DestroyInfra(ctx, logger, infra)
}

// DestroyInfra infra destruction orchestration
func (options *DestroyInfraOptions) DestroyInfra(ctx context.Context, logger logr.Logger, infra *Infra) error {
	logger.Info("Destroy Infra Started")
	var err error

	cloudApiKey, err = GetAPIKey()
	if err != nil {
		return fmt.Errorf("error retrieving IBM Cloud API Key: %w", err)
	}

	// if CLOUD API KEY is not set, infra cannot be set up.
	if cloudApiKey == "" {
		return fmt.Errorf("cloud API Key not set. Set it with IBMCLOUD_API_KEY env var or set file path containing API Key credential in IBMCLOUD_CREDENTIALS")
	}

	accountID, err := getAccount(ctx, getIAMAuth())
	if err != nil {
		return fmt.Errorf("error retrieving account ID %w", err)
	}

	resourceGroupID, err := getResourceGroupID(ctx, options.ResourceGroup, accountID)
	if err != nil {
		return err
	}

	serviceID, servicePlanID, err := getServiceInfo(ctx, powerVSService, powerVSServicePlan)
	if err != nil {
		return err
	}

	errL := make([]error, 0)

	if err = deleteDNSRecords(ctx, logger, options); err != nil {
		errL = append(errL, fmt.Errorf("error deleting dns record from cis domain: %w", err))
		logger.Error(err, "error deleting dns record from cis domain")
	}

	if err = deleteCOS(ctx, logger, options, resourceGroupID); err != nil {
		errL = append(errL, fmt.Errorf("error deleting cos buckets: %w", err))
		logger.Error(err, "error deleting cos buckets")
	}

	var powerVsCloudInstanceID string
	var skipPowerVs bool
	var cloudInstance *resourcecontrollerv2.ResourceInstance

	// getting the powervs cloud instance id
	if infra != nil && infra.CloudInstanceID != "" {
		powerVsCloudInstanceID = infra.CloudInstanceID
	} else if options.CloudInstanceID != "" {
		if cloudInstance, err = validateCloudInstanceByID(ctx, options.CloudInstanceID); cloudInstance == nil {
			if err != nil && err.Error() != cloudInstanceNotFound(options.CloudInstanceID).Error() {
				errL = append(errL, err)
			}
			skipPowerVs = true
		} else {
			powerVsCloudInstanceID = options.CloudInstanceID
		}
	} else {
		cloudInstanceName := fmt.Sprintf("%s-%s", options.InfraID, cloudInstanceNameSuffix)
		if cloudInstance, err = validateCloudInstanceByName(ctx, cloudInstanceName, resourceGroupID, options.Zone, serviceID, servicePlanID); cloudInstance == nil {
			if err != nil && err.Error() != cloudInstanceNotFound(cloudInstanceName).Error() {
				errL = append(errL, err)
			}
			skipPowerVs = true
		} else {
			powerVsCloudInstanceID = *cloudInstance.GUID
		}
	}

	if err = deleteSecrets(options.Name, options.Namespace, powerVsCloudInstanceID, accountID, resourceGroupID); err != nil {
		errL = append(errL, fmt.Errorf("error deleting secrets: %w", err))
		logger.Error(err, "error deleting secrets")
	}

	var session *ibmpisession.IBMPISession
	if !skipPowerVs {
		session, err = createPowerVSSession(accountID, options.Region, options.Zone, options.Debug)
		if err != nil {
			return err
		}
	}

	if err = destroyTransitGateway(ctx, logger, options); err != nil {
		errL = append(errL, fmt.Errorf("error destroying transit gateway: %w", err))
		logger.Error(err, "error destroying transit gateway")
	}

	v1, err := createVpcService(logger, options.VPCRegion)
	if err != nil {
		return err
	}

	if err = destroyVpcSubnet(ctx, logger, options, infra, resourceGroupID, v1, options.InfraID); err != nil {
		errL = append(errL, fmt.Errorf("error destroying vpc subnet: %w", err))
		logger.Error(err, "error destroying vpc subnet")
	}

	if err = destroyVpc(ctx, logger, options, infra, resourceGroupID, v1, options.InfraID); err != nil {
		errL = append(errL, fmt.Errorf("error destroying vpc: %w", err))
		logger.Error(err, "error destroying vpc")
	}

	if !skipPowerVs {
		// destroy DHCP server first, to not rely on PowerVs Cloud Instance recursive delete
		// NOTE: not calling dhcp destroy inside the destroyPowerVsCloudInstance, to avoid Private Network deletion getting stuck on modified
		if err = destroyPowerVsDhcpServer(ctx, logger, powerVsCloudInstanceID, options, session); err != nil {
			logger.Error(err, "error destroying DhcpServer")
		}
		if err = destroyPowerVsCloudInstance(ctx, logger, options, powerVsCloudInstanceID); err != nil {
			errL = append(errL, fmt.Errorf("error destroying powervs cloud instance: %w", err))
			logger.Error(err, "error destroying powervs cloud instance")
		}
	}

	logger.Info("Destroy Infra Completed")

	if err = errors.NewAggregate(errL); err != nil {
		return fmt.Errorf("error in destroying infra: %w", err)
	}

	return nil
}

// deleteDNSRecords deletes DNS records from CIS domain
func deleteDNSRecords(ctx context.Context, logger logr.Logger, options *DestroyInfraOptions) error {

	if options.CISCRN == "" || options.CISDomainID == "" {
		var err error
		options.CISCRN, options.CISDomainID, err = getCISDomainDetails(ctx, options.BaseDomain)
		if err != nil {
			return fmt.Errorf("error retrieving cis domain details %w", err)
		}
	}

	dnsRecordsV1, err := dnsrecordsv1.NewDnsRecordsV1(&dnsrecordsv1.DnsRecordsV1Options{Crn: &options.CISCRN, ZoneIdentifier: &options.CISDomainID, Authenticator: getIAMAuth()})
	if err != nil {
		return fmt.Errorf("error creating dns record service %w", err)
	}

	recordName := fmt.Sprintf("*.apps.%s.%s", options.Name, options.BaseDomain)
	listDnsRecordsOpt := &dnsrecordsv1.ListAllDnsRecordsOptions{Name: &recordName}

	dnsRecordsL, _, err := dnsRecordsV1.ListAllDnsRecordsWithContext(ctx, listDnsRecordsOpt)
	if err != nil {
		return err
	}

	if len(dnsRecordsL.Result) == 0 {
		logger.Info("No matching DNS Records present in CIS Domain")
		return nil
	}

	record := dnsRecordsL.Result[0]
	logger.Info("Deleting DNS", "record", recordName)
	deleteRecordOpt := &dnsrecordsv1.DeleteDnsRecordOptions{DnsrecordIdentifier: record.ID}
	if _, _, err = dnsRecordsV1.DeleteDnsRecordWithContext(ctx, deleteRecordOpt); err != nil {
		return err
	}

	return nil
}

// deleteSecrets delete secrets generated for control plane components
func deleteSecrets(name, namespace, cloudInstanceID string, accountID string, resourceGroupID string) error {
	var e error

	kubeCloudControllerManagerCR, e = updateCRYaml(kubeCloudControllerManagerCR, "kubeCloudControllerManagerCRTemplate", cloudInstanceID)
	if e != nil {
		return fmt.Errorf("error updating kube cloud controller manager yaml: %w", e)
	}
	err := deleteServiceID(name, cloudApiKey, accountID, resourceGroupID,
		kubeCloudControllerManagerCR, kubeCloudControllerManagerCreds, namespace)
	if err != nil {
		return fmt.Errorf("error deleting kube cloud controller manager secret: %w", err)
	}

	nodePoolManagementCR, e = updateCRYaml(nodePoolManagementCR, "nodePoolManagementCRTemplate", cloudInstanceID)
	if e != nil {
		return fmt.Errorf("error updating nodepool management yaml: %w", e)
	}
	err = deleteServiceID(name, cloudApiKey, accountID, resourceGroupID,
		nodePoolManagementCR, nodePoolManagementCreds, namespace)
	if err != nil {
		return fmt.Errorf("error deleting nodepool management secret: %w", err)
	}

	err = deleteServiceID(name, cloudApiKey, accountID, "",
		ingressOperatorCR, ingressOperatorCreds, namespace)
	if err != nil {
		return fmt.Errorf("error deleting ingress operator secret: %w", err)
	}

	storageOperatorCR, e = updateCRYaml(storageOperatorCR, "storageOperatorCRTemplate", cloudInstanceID)
	if e != nil {
		return fmt.Errorf("error updating storage operator yaml: %w", e)
	}
	err = deleteServiceID(name, cloudApiKey, accountID, resourceGroupID,
		storageOperatorCR, storageOperatorCreds, namespace)
	if err != nil {
		return fmt.Errorf("error deleting ingress operator secret: %w", err)
	}

	if err = deleteServiceID(name, cloudApiKey, accountID, resourceGroupID,
		imageRegistryOperatorCR, imageRegistryOperatorCreds, namespace); err != nil {
		return fmt.Errorf("error deleting image registry operator secret: %w", err)
	}

	return nil
}

// destroyPowerVsDhcpServer destroying powervs dhcp server
func destroyPowerVsDhcpServer(ctx context.Context, logger logr.Logger, cloudInstanceID string, options *DestroyInfraOptions, session *ibmpisession.IBMPISession) error {
	// Attempt dhcp server deletion only when cloud instance is created by hypershift
	// Do not clean up, if PowerVS instance is user provided
	if options.CloudInstanceID != "" {
		return nil
	}

	client := instance.NewIBMPIDhcpClient(ctx, session, cloudInstanceID)

	dhcpServers, err := client.GetAll()
	if err != nil {
		return err
	}

	if len(dhcpServers) == 0 {
		logger.Info("No DHCP servers available to delete in PowerVS")
		return nil
	}

	dhcpID := *dhcpServers[0].ID
	logger.Info("Deleting DHCP server", "id", dhcpID)
	if err = client.Delete(dhcpID); err != nil {
		return err
	}

	instanceClient := instance.NewIBMPIInstanceClient(ctx, session, cloudInstanceID)

	// TO-DO: need to replace the logic of waiting for dhcp service deletion by using jobReference.
	// jobReference is not yet added in SDK
	return wait.PollUntilContextTimeout(ctx, pollingInterval, dhcpServerDeletionTimeout, true, func(context.Context) (bool, error) {
		dhcpInstance, err := instanceClient.Get(dhcpID)
		if err != nil {
			if err = isNotRetryableError(err, timeoutErrorKeywords); err == nil {
				return false, nil
			}

			errMsg := err.Error()

			switch {
			case strings.Contains(errMsg, "pvm-instance does not exist"):
				// Instance does not exist; proceed with destruction
				err = nil
				return true, nil

			case strings.Contains(errMsg, "unable to get the server volume attachment list"):
				// There is flaky internal server error in pvm-instance get call; retry destroy
				return false, nil

			default:
				return false, err
			}
		}

		logger.Info("Waiting for DhcpServer to destroy", "id", *dhcpInstance.PvmInstanceID, "status", *dhcpInstance.Status)
		return false, nil
	})

}

// destroyPowerVsCloudInstance destroying powervs cloud instance
func destroyPowerVsCloudInstance(ctx context.Context, logger logr.Logger, options *DestroyInfraOptions, cloudInstanceID string) error {
	rcv2, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{Authenticator: getIAMAuth()})
	if err != nil {
		return err
	}

	// Attempt cloud instance deletion only when it is created by hypershift
	// Nothing to clean up on user provided PowerVS instance
	if options.CloudInstanceID == "" {
		for retry := 0; retry < 5; retry++ {
			logger.Info("Deleting PowerVS cloud instance", "id", cloudInstanceID)
			if _, err = rcv2.DeleteResourceInstanceWithContext(ctx, &resourcecontrollerv2.DeleteResourceInstanceOptions{ID: &cloudInstanceID}); err != nil {
				logger.Error(err, "error in deleting powervs cloud instance")
				continue
			}

			if err = wait.PollUntilContextTimeout(ctx, pollingInterval, cloudInstanceDeletionTimeout, true, func(context.Context) (bool, error) {
				resourceInst, resp, err := rcv2.GetResourceInstanceWithContext(ctx, &resourcecontrollerv2.GetResourceInstanceOptions{ID: &cloudInstanceID})
				if err != nil {
					logger.Error(err, "error in querying deleted cloud instance", "resp", resp.String())
					return false, err
				}

				if resp.StatusCode >= 400 {
					return false, fmt.Errorf("retrying due to resp code is %d and message is %s", resp.StatusCode, resp.String())
				}
				if resourceInst != nil {
					if *resourceInst.State == powerVSCloudInstanceRemovedState {
						return true, nil
					}

					logger.Info("Waiting for PowerVS cloud instance deletion", "status", *resourceInst.State, "lastOp", resourceInst.LastOperation)
				}

				return false, nil
			}); err == nil {
				break
			}
			logger.Info("Retrying cloud instance deletion ...")
		}
	}
	return err
}

// destroyVpc destroying vpc
func destroyVpc(ctx context.Context, logger logr.Logger, options *DestroyInfraOptions, infra *Infra, resourceGroupID string, v1 *vpcv1.VpcV1, infraID string) error {
	if options.VPC != "" {
		logger.Info("Skipping VPC deletion since its user provided")
		return nil
	}

	if infra != nil && infra.VPCID != "" {
		return deleteVpc(ctx, logger, infra.VPCID, v1, infraID)
	}

	f := func(start string) (bool, string, error) {
		vpcListOpt := vpcv1.ListVpcsOptions{ResourceGroupID: &resourceGroupID}
		if start != "" {
			vpcListOpt.Start = &start
		}

		vpcL, _, err := v1.ListVpcsWithContext(ctx, &vpcListOpt)
		if err != nil {
			return false, "", err
		}

		if vpcL == nil || len(vpcL.Vpcs) <= 0 {
			logger.Info("No VPC available to delete")
			return true, "", nil
		}

		// Will attempt VPC deletion only if the VPC is created by this script by matching the VPC naming convention followed by this script
		vpcName := fmt.Sprintf("%s-%s", options.InfraID, vpcNameSuffix)

		for _, vpc := range vpcL.Vpcs {
			if *vpc.Name == vpcName && strings.Contains(*vpc.CRN, options.VPCRegion) {
				if err = deleteVpc(ctx, logger, *vpc.ID, v1, infraID); err != nil {
					return false, "", err
				}
				return true, "", nil
			}
		}
		if vpcL.Next != nil && *vpcL.Next.Href != "" {
			return false, *vpcL.Next.Href, nil
		}
		return true, "", nil
	}

	return pagingHelper(f)
}

// deleteVpc deletes the vpc id passed
func deleteVpc(ctx context.Context, logger logr.Logger, id string, v1 *vpcv1.VpcV1, infraID string) error {
	logger.Info("Deleting VPC", "id", id)
	_, err := v1.DeleteVPCWithContext(ctx, &vpcv1.DeleteVPCOptions{ID: &id})
	return err
}

// destroyVpcSubnet destroying vpc subnet
func destroyVpcSubnet(ctx context.Context, logger logr.Logger, options *DestroyInfraOptions, infra *Infra, resourceGroupID string, v1 *vpcv1.VpcV1, infraID string) error {
	if infra != nil && infra.VPCSubnetID != "" {
		return deleteVpcSubnet(ctx, logger, infra.VPCSubnetID, v1, options)
	}

	f := func(start string) (bool, string, error) {

		listSubnetOpt := vpcv1.ListSubnetsOptions{ResourceGroupID: &resourceGroupID}
		if start != "" {
			listSubnetOpt.Start = &start
		}
		subnetL, _, err := v1.ListSubnetsWithContext(ctx, &listSubnetOpt)
		if err != nil {
			return false, "", err
		}

		if subnetL == nil || len(subnetL.Subnets) <= 0 {
			logger.Info("No VPC Subnets available to delete")
			return true, "", nil
		}

		// Consider deleting only if the subnet created by this script by matching the VPC naming convention followed by this script
		vpcName := fmt.Sprintf("%s-%s", options.InfraID, vpcNameSuffix)

		for _, subnet := range subnetL.Subnets {
			if (*subnet.VPC.Name == vpcName || *subnet.VPC.Name == options.VPC) && strings.Contains(*subnet.Zone.Name, options.VPCRegion) {
				if err = deleteVpcSubnet(ctx, logger, *subnet.ID, v1, options); err != nil {
					return false, "", err
				}
				return true, "", nil
			}
		}

		// For paging over next set of resources getting the start token and passing it for next iteration
		if subnetL.Next != nil && *subnetL.Next.Href != "" {
			return false, *subnetL.Next.Href, nil
		}

		return true, "", nil
	}

	return pagingHelper(f)
}

// deleteVpcSubnet deletes the subnet id passed and LB attached to it
func deleteVpcSubnet(ctx context.Context, logger logr.Logger, id string, v1 *vpcv1.VpcV1, options *DestroyInfraOptions) error {
	// deleting the load balancer before proceeding to subnet deletion, since LB is an attached resource to the subnet
	var err error
	for retry := 0; retry < 5; retry++ {
		if err = destroyVpcLB(ctx, logger, options, id, v1); err == nil {
			break
		}
		logger.Info("retrying VPC Load Balancer deletion")
	}

	if err != nil {
		logger.Error(err, "error destroying VPC Load Balancer")
		return fmt.Errorf("error destroying VPC Load Balancer %w", err)
	}

	// In case of user provided VPC delete only load balancer and not the subnet
	if options.VPC != "" {
		return nil
	}

	logger.Info("Deleting VPC subnet", "subnetId", id)

	if _, err := v1.DeleteSubnetWithContext(ctx, &vpcv1.DeleteSubnetOptions{ID: &id}); err != nil {
		return err
	}

	return wait.PollUntilContextTimeout(ctx, pollingInterval, vpcResourceDeletionTimeout, true, func(context.Context) (bool, error) {
		if _, _, err := v1.GetSubnetWithContext(ctx, &vpcv1.GetSubnetOptions{ID: &id}); err != nil {
			if strings.Contains(err.Error(), "Subnet not found") {
				return true, nil
			} else {
				return false, err
			}
		}
		return false, nil
	})
}

// destroyVpcLB destroys VPC Load Balancer
func destroyVpcLB(ctx context.Context, logger logr.Logger, options *DestroyInfraOptions, subnetID string, v1 *vpcv1.VpcV1) error {

	deleteLB := func(id string) error {
		logger.Info("Deleting VPC LoadBalancer:", "id", id)
		if _, err := v1.DeleteLoadBalancerWithContext(ctx, &vpcv1.DeleteLoadBalancerOptions{ID: &id}); err != nil {
			return err
		}

		return wait.PollUntilContextTimeout(ctx, pollingInterval, vpcResourceDeletionTimeout, true, func(context.Context) (bool, error) {
			_, _, err := v1.GetLoadBalancerWithContext(ctx, &vpcv1.GetLoadBalancerOptions{ID: &id})
			if err != nil && strings.Contains(err.Error(), "cannot be found") {
				return true, nil
			}
			return false, err
		})
	}

	f := func(start string) (bool, string, error) {

		listLBOpt := vpcv1.ListLoadBalancersOptions{}
		if start != "" {
			listLBOpt.Start = &start
		}
		loadBalancerL, _, err := v1.ListLoadBalancersWithContext(ctx, &listLBOpt)
		if err != nil {
			return false, "", err
		}

		if loadBalancerL == nil || len(loadBalancerL.LoadBalancers) <= 0 {
			logger.Info("No Load Balancers available to delete")
			return true, "", nil
		}

		// Consider deleting LB which starting with 'kube-<cluster-name>'
		// Which are provisioned by cloud controller manager. Below is the code ref for the naming convention.
		// https://github.com/openshift/cloud-provider-powervs/blob/master/pkg/vpcctl/vpc_provider.go#L235
		lbName := fmt.Sprintf("%s-%s", vpcLbNamePrefix, options.Name)

		for _, lb := range loadBalancerL.LoadBalancers {
			for _, subnet := range lb.Subnets {
				if *subnet.ID == subnetID {
					if strings.Contains(*lb.Name, lbName) {
						if err = deleteLB(*lb.ID); err != nil {
							return false, "", err
						}
						return true, "", nil
					}
				}
			}
		}

		// For paging over next set of resources getting the start token and passing it for next iteration
		if loadBalancerL.Next != nil && *loadBalancerL.Next.Href != "" {
			return false, *loadBalancerL.Next.Href, nil
		}

		return true, "", nil
	}

	return pagingHelper(f)
}

// createCOSClient creates COS client to interact with the COS for clean up
func createCOSClient(serviceInstanceCRN string, location string) (*s3.S3, error) {
	// if IBMCLOUD_COS_API_ENDPOINT is set, will use custom endpoint with the default cosEndpoint
	serviceEndpoint := getCustomEndpointUrl(cosService, fmt.Sprintf("s3.%s.cloud-object-storage.appdomain.cloud", location))

	awsOptions := cosSession.Options{
		Config: aws.Config{
			Endpoint: &serviceEndpoint,
			Region:   &location,
			HTTPClient: &http.Client{
				Transport: &http.Transport{
					Proxy: func(req *http.Request) (*url.URL, error) {
						return httpproxy.FromEnvironment().ProxyFunc()(req.URL)
					},
					DialContext: (&net.Dialer{
						Timeout:   30 * time.Second,
						KeepAlive: 30 * time.Second,
						DualStack: true,
					}).DialContext,
					ForceAttemptHTTP2:     true,
					MaxIdleConns:          100,
					IdleConnTimeout:       90 * time.Second,
					TLSHandshakeTimeout:   10 * time.Second,
					ExpectContinueTimeout: 1 * time.Second,
				},
			},
			S3ForcePathStyle: aws.Bool(true),
		},
	}

	awsOptions.Config.Credentials = ibmiam.NewStaticCredentials(aws.NewConfig(), getCustomEndpointUrl(platformService, iamEndpoint), cloudApiKey, serviceInstanceCRN)

	sess, err := cosSession.NewSessionWithOptions(awsOptions)
	if err != nil {
		return nil, err
	}
	return s3.New(sess), nil
}

// findCOSInstance find COS resource instance by name
func findCOSInstance(rcv2 *resourcecontrollerv2.ResourceControllerV2, cosInstanceName string, resourceGroupID string) (*resourcecontrollerv2.ResourceInstance, error) {
	// cosResourcePlanID is a UID of cloud object storage service from ibm cloud catalog, need this for resource filtering
	// https://cloud.ibm.com/docs/cloud-object-storage?topic=cloud-object-storage-faq-provision resource plan id of cloud object storage service specified
	cosResourcePlanID := "744bfc56-d12c-4866-88d5-dac9139e0e5d"
	instances, resp, err := rcv2.ListResourceInstances(
		&resourcecontrollerv2.ListResourceInstancesOptions{
			Name:            &cosInstanceName,
			ResourceGroupID: &resourceGroupID,
			ResourcePlanID:  ptr.To(cosResourcePlanID),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("unable to get resource instances: %s with resp code: %d", err.Error(), resp.StatusCode)
	}

	if len(instances.Resources) < 1 {
		return nil, fmt.Errorf("COS instance unavailable")
	}

	return &instances.Resources[0], nil
}

// isBucketNotFound determines if a set of S3 errors are indicative
// of if a bucket is truly not found.
func isBucketNotFound(err interface{}) bool {
	switch s3Err := err.(type) {
	case awserr.Error:
		if s3Err.Code() == "NoSuchBucket" {
			return true
		}
		origErr := s3Err.OrigErr()
		if origErr != nil {
			return isBucketNotFound(origErr)
		}
	case s3manager.Error:
		if s3Err.OrigErr != nil {
			return isBucketNotFound(s3Err.OrigErr)
		}
	case s3manager.Errors:
		if len(s3Err) > 0 {
			return isBucketNotFound(s3Err[0])
		}
	}
	return false
}

// deleteCOSBucket deletes COS bucket and the objects in it
func deleteCOSBucket(ctx context.Context, bucketName string, cosClient *s3.S3) error {
	iter := s3manager.NewDeleteListIterator(cosClient, &s3.ListObjectsInput{
		Bucket: aws.String(bucketName),
	})

	// Deleting objects under the bucket
	if err := s3manager.NewBatchDeleteWithClient(cosClient).Delete(ctx, iter); err != nil && !isBucketNotFound(err) {
		return err
	}

	// Deleting the bucket
	if _, err := cosClient.DeleteBucketWithContext(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String(bucketName),
	}); err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() != s3.ErrCodeNoSuchBucket {
				return err
			}
		} else {
			return err
		}
	}

	return cosClient.WaitUntilBucketNotExistsWithContext(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
}

// deleteCOS deletes the COS instance and associated resources like objects, buckets and resource keys
func deleteCOS(ctx context.Context, logger logr.Logger, options *DestroyInfraOptions, resourceGroupID string) error {
	// cosInstanceName is generated by following the notation used to generate `serviceInstanceName` var here https://github.com/openshift/cluster-image-registry-operator/blob/86635851e94d656cddecabf4c13ef31b90d3e994/pkg/storage/ibmcos/ibmcos.go#L246
	cosInstanceName := fmt.Sprintf("%s-%s", options.InfraID, "image-registry")

	rcv2, err := resourcecontrollerv2.NewResourceControllerV2(
		&resourcecontrollerv2.ResourceControllerV2Options{
			Authenticator: getIAMAuth(),
			URL:           getCustomEndpointUrl(platformService, resourcecontrollerv2.DefaultServiceURL),
		})
	if err != nil {
		return err
	}

	// Checking COS instance available to delete, if not skip COS deletion
	cosInstance, err := findCOSInstance(rcv2, cosInstanceName, resourceGroupID)
	if err != nil {
		if err.Error() == "COS instance unavailable" {
			logger.Info("No COS Instance available to delete")
			return nil
		}
		return err
	}

	// Deciding cosRegion based on Power VS region with region mapping
	cosRegion, err := regionutils.COSRegionForPowerVSRegion(options.Region)
	if err != nil {
		return err
	}
	cosClient, err := createCOSClient(*cosInstance.CRN, cosRegion)
	if err != nil {
		return err
	}

	bucketNamePrefix := fmt.Sprintf("%s-%s", cosInstanceName, cosRegion)

	// Deleting COS buckets before proceeding to COS instance deletion
	bucketList, err := cosClient.ListBuckets(&s3.ListBucketsInput{IBMServiceInstanceId: cosInstance.ID})
	if err != nil {
		return err
	}
	for _, bucket := range bucketList.Buckets {
		if strings.HasPrefix(*bucket.Name, bucketNamePrefix) {
			if err := deleteCOSBucket(ctx, *bucket.Name, cosClient); err != nil {
				return err
			}
		}
	}

	// Deleting resource keys associated with the COS instance before proceeding to COS instance deletion
	keysL, _, err := rcv2.ListResourceKeysForInstance(&resourcecontrollerv2.ListResourceKeysForInstanceOptions{ID: cosInstance.ID})
	if err != nil {
		return err
	}
	if len(keysL.Resources) > 0 {
		for _, key := range keysL.Resources {
			// Deleting resource keys(service id) associated with COS
			err = deleteServiceIDByCRN(options.Name, cloudApiKey, *key.Credentials.IamServiceidCRN)
			if err != nil {
				return err
			}
			_, err = rcv2.DeleteResourceKeyWithContext(ctx, &resourcecontrollerv2.DeleteResourceKeyOptions{ID: key.ID})
			if err != nil {
				return err
			}
		}
	}

	logger.Info("Deleting COS Instance", "name", cosInstanceName)
	_, err = rcv2.DeleteResourceInstance(&resourcecontrollerv2.DeleteResourceInstanceOptions{ID: cosInstance.ID})

	return err
}

// destroyTransitGateway destroys transit gateway and associated connections
func destroyTransitGateway(ctx context.Context, logger logr.Logger, options *DestroyInfraOptions) error {
	tgapisv1, err := transitgatewayapisv1.NewTransitGatewayApisV1(&transitgatewayapisv1.TransitGatewayApisV1Options{
		Authenticator: getIAMAuth(),
		Version:       ptr.To(currentDate),
	})
	if err != nil {
		return err
	}

	if options.TransitGateway != "" {
		return nil
	}

	transitGatewayName := fmt.Sprintf("%s-%s", options.InfraID, transitGatewayNameSuffix)

	tg, err := validateTransitGatewayByName(ctx, tgapisv1, transitGatewayName, false)
	if err != nil {
		if err.Error() == transitGatewayNotFound(transitGatewayName).Error() {
			return nil
		}
		if err.Error() != transitGatewayConnectionError().Error() {
			return fmt.Errorf("error retrieving transit gateway by name: %s, err: %w", transitGatewayName, err)
		}
	}

	tgConnList, _, err := tgapisv1.ListTransitGatewayConnectionsWithContext(ctx, &transitgatewayapisv1.ListTransitGatewayConnectionsOptions{
		TransitGatewayID: tg.ID,
	})
	if err != nil {
		return fmt.Errorf("error retrieving transit gateway connection list: %v", err)
	}

	for _, tgConn := range tgConnList.Connections {
		logger.Info("Deleting transit gateway connection", "name", *tgConn.Name)
		if _, err = tgapisv1.DeleteTransitGatewayConnectionWithContext(ctx, &transitgatewayapisv1.DeleteTransitGatewayConnectionOptions{
			TransitGatewayID: tg.ID,
			ID:               tgConn.ID,
		}); err != nil {
			return fmt.Errorf("error deleting transit gateway connection %s, %v", *tgConn.Name, err)
		}
	}

	f := func(ctx2 context.Context) (bool, error) {
		tgConnList, _, err = tgapisv1.ListTransitGatewayConnectionsWithContext(ctx2, &transitgatewayapisv1.ListTransitGatewayConnectionsOptions{
			TransitGatewayID: tg.ID,
		})
		if err != nil {
			return false, fmt.Errorf("error retrieving transit gateway connection list: %v", err)
		}
		if len(tgConnList.Connections) > 0 {
			return false, nil
		}

		return true, nil
	}

	logger.Info("Waiting for transit gateway connections to get deleted")
	if err = wait.PollUntilContextTimeout(ctx, time.Minute*1, time.Minute*10, true, f); err != nil {
		return fmt.Errorf("error waiting for tranist gateway connections to get deleted: %v", err)
	}

	logger.Info("Deleting transit gateway", "name", *tg.Name)
	if _, err = tgapisv1.DeleteTransitGatewayWithContext(ctx, &transitgatewayapisv1.DeleteTransitGatewayOptions{
		ID: tg.ID,
	}); err != nil {
		return fmt.Errorf("error deleting transit gateway: %w", err)
	}

	return nil
}
