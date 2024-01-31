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

	"golang.org/x/net/http/httpproxy"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	utilpointer "k8s.io/utils/pointer"

	"github.com/IBM-Cloud/power-go-client/clients/instance"
	"github.com/IBM-Cloud/power-go-client/ibmpisession"
	"github.com/IBM-Cloud/power-go-client/power/models"
	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/awserr"
	"github.com/IBM/ibm-cos-sdk-go/aws/credentials/ibmiam"
	cosSession "github.com/IBM/ibm-cos-sdk-go/aws/session"
	"github.com/IBM/ibm-cos-sdk-go/service/s3"
	"github.com/IBM/ibm-cos-sdk-go/service/s3/s3manager"
	"github.com/IBM/networking-go-sdk/dnsrecordsv1"
	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"
	"github.com/IBM/vpc-go-sdk/vpcv1"
	regionutils "github.com/ppc64le-cloud/powervs-utils"
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
	dhcpInstanceShutOffState         = "SHUTOFF"

	// Resource name prefix
	vpcLbNamePrefix = "kube"

	// IAM endpoint for creating COS session
	iamEndpoint = "https://iam.cloud.ibm.com/identity/token"
)

// DestroyInfraOptions command line options to destroy infra created in IBMCloud for Hypershift
type DestroyInfraOptions struct {
	Name               string
	Namespace          string
	InfraID            string
	InfrastructureJson string
	BaseDomain         string
	CISCRN             string
	CISDomainID        string
	ResourceGroup      string
	Region             string
	Zone               string
	CloudInstanceID    string
	DHCPID             string
	CloudConnection    string
	VPCRegion          string
	VPC                string
	Debug              bool
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
	cmd.Flags().StringVar(&opts.CloudConnection, "cloud-connection", opts.CloudConnection, "IBM Cloud PowerVS Cloud Connection. Use this flag to reuse an existing Cloud Connection resource for cluster's infra")
	cmd.Flags().StringVar(&opts.CloudInstanceID, "cloud-instance-id", opts.CloudInstanceID, "IBM PowerVS Cloud Instance ID. Use this flag to reuse an existing PowerVS Cloud Instance resource for cluster's infra")
	cmd.Flags().BoolVar(&opts.Debug, "debug", opts.Debug, "Enabling this will result in debug logs will be printed")

	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("resource-group")
	cmd.MarkFlagRequired("base-domain")
	cmd.MarkFlagRequired("infra-id")
	cmd.MarkFlagRequired("region")
	cmd.MarkFlagRequired("zone")
	cmd.MarkFlagRequired("vpc-region")

	// these options are only for development and testing purpose, user can pass these flags
	// to destroy the resource created inside these resources for hypershift infra purpose
	cmd.Flags().MarkHidden("vpc")
	cmd.Flags().MarkHidden("cloud-connection")
	cmd.Flags().MarkHidden("cloud-instance-id")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context()); err != nil {
			log(opts.InfraID).Error(err, "Failed to destroy infrastructure")
			return err
		}
		log(opts.InfraID).Info("Successfully destroyed infrastructure")
		return nil
	}

	return cmd
}

// Run Hypershift Infra Destroy
func (options *DestroyInfraOptions) Run(ctx context.Context) error {
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

	return options.DestroyInfra(ctx, infra)
}

// DestroyInfra infra destruction orchestration
func (options *DestroyInfraOptions) DestroyInfra(ctx context.Context, infra *Infra) error {
	log(options.InfraID).Info("Destroy Infra Started")
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

	if err = deleteDNSRecords(ctx, options); err != nil {
		errL = append(errL, fmt.Errorf("error deleting dns record from cis domain: %w", err))
		log(options.InfraID).Error(err, "error deleting dns record from cis domain")
	}

	if err = deleteCOS(ctx, options, resourceGroupID); err != nil {
		errL = append(errL, fmt.Errorf("error deleting cos buckets: %w", err))
		log(options.InfraID).Error(err, "error deleting cos buckets")
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
		log(options.InfraID).Error(err, "error deleting secrets")
	}

	var session *ibmpisession.IBMPISession
	if !skipPowerVs {
		session, err = createPowerVSSession(accountID, options.Region, options.Zone, options.Debug)
		if err != nil {
			return err
		}

		if err = destroyPowerVsCloudConnection(ctx, options, infra, powerVsCloudInstanceID, session); err != nil {
			errL = append(errL, fmt.Errorf("error destroying powervs cloud connection: %w", err))
			log(options.InfraID).Error(err, "error destroying powervs cloud connection")
		}
	}

	v1, err := createVpcService(options.VPCRegion, options.InfraID)
	if err != nil {
		return err
	}

	if err = destroyVpcSubnet(ctx, options, infra, resourceGroupID, v1, options.InfraID); err != nil {
		errL = append(errL, fmt.Errorf("error destroying vpc subnet: %w", err))
		log(options.InfraID).Error(err, "error destroying vpc subnet")
	}

	if err = destroyVpc(ctx, options, infra, resourceGroupID, v1, options.InfraID); err != nil {
		errL = append(errL, fmt.Errorf("error destroying vpc: %w", err))
		log(options.InfraID).Error(err, "error destroying vpc")
	}

	if !skipPowerVs {
		if err = destroyPowerVsCloudInstance(ctx, options, infra, powerVsCloudInstanceID, session); err != nil {
			errL = append(errL, fmt.Errorf("error destroying powervs cloud instance: %w", err))
			log(options.InfraID).Error(err, "error destroying powervs cloud instance")
		}
	}

	log(options.InfraID).Info("Destroy Infra Completed")

	if err = errors.NewAggregate(errL); err != nil {
		return fmt.Errorf("error in destroying infra: %w", err)
	}

	return nil
}

// deleteDNSRecords deletes DNS records from CIS domain
func deleteDNSRecords(ctx context.Context, options *DestroyInfraOptions) error {

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
		log(options.InfraID).Info("No matching DNS Records present in CIS Domain")
		return nil
	}

	record := dnsRecordsL.Result[0]
	log(options.InfraID).Info("Deleting DNS", "record", recordName)
	deleteRecordOpt := &dnsrecordsv1.DeleteDnsRecordOptions{DnsrecordIdentifier: record.ID}
	if _, _, err = dnsRecordsV1.DeleteDnsRecordWithContext(ctx, deleteRecordOpt); err != nil {
		return err
	}

	return nil
}

// deleteSecrets delete secrets generated for control plane components
func deleteSecrets(name, namespace, cloudInstanceID string, accountID string, resourceGroupID string) error {

	kubeCloudControllerManagerCR = updateCRYaml(kubeCloudControllerManagerCR, "kubeCloudControllerManagerCRTemplate", cloudInstanceID)
	err := deleteServiceID(name, cloudApiKey, accountID, resourceGroupID,
		kubeCloudControllerManagerCR, kubeCloudControllerManagerCreds, namespace)
	if err != nil {
		return fmt.Errorf("error deleting kube cloud controller manager secret: %w", err)
	}

	nodePoolManagementCR = updateCRYaml(nodePoolManagementCR, "nodePoolManagementCRTemplate", cloudInstanceID)
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

	storageOperatorCR = updateCRYaml(storageOperatorCR, "storageOperatorCRTemplate", cloudInstanceID)
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

// destroyPowerVsCloudInstance destroying powervs cloud instance
func destroyPowerVsCloudInstance(ctx context.Context, options *DestroyInfraOptions, infra *Infra, cloudInstanceID string, session *ibmpisession.IBMPISession) error {
	rcv2, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{Authenticator: getIAMAuth()})
	if err != nil {
		return err
	}

	// Attempt cloud instance deletion only when it is created by hypershift
	// Nothing to clean up on user provided PowerVS instance
	if options.CloudInstanceID == "" {
		for retry := 0; retry < 5; retry++ {
			log(options.InfraID).Info("Deleting PowerVS cloud instance", "id", cloudInstanceID)
			if _, err = rcv2.DeleteResourceInstanceWithContext(ctx, &resourcecontrollerv2.DeleteResourceInstanceOptions{ID: &cloudInstanceID}); err != nil {
				log(options.InfraID).Error(err, "error in deleting powervs cloud instance")
				continue
			}

			f := func() (bool, error) {
				resourceInst, resp, err := rcv2.GetResourceInstanceWithContext(ctx, &resourcecontrollerv2.GetResourceInstanceOptions{ID: &cloudInstanceID})
				if err != nil {
					log(options.InfraID).Error(err, "error in querying deleted cloud instance", "resp", resp.String())
					return false, err
				}

				if resp.StatusCode >= 400 {
					return false, fmt.Errorf("retrying due to resp code is %d and message is %s", resp.StatusCode, resp.String())
				}
				if resourceInst != nil {
					if *resourceInst.State == powerVSCloudInstanceRemovedState {
						return true, nil
					}

					log(options.InfraID).Info("Waiting for PowerVS cloud instance deletion", "status", *resourceInst.State, "lastOp", resourceInst.LastOperation)
				}

				return false, nil
			}

			if err = wait.PollImmediate(pollingInterval, cloudInstanceDeletionTimeout, f); err == nil {
				break
			}
			log(options.InfraID).Info("Retrying cloud instance deletion ...")
		}
	}
	return err
}

// monitorPowerVsJob monitoring the submitted deletion job
func monitorPowerVsJob(id string, client *instance.IBMPIJobClient, infraID string, timeout time.Duration) error {
	f := func() (bool, error) {
		job, err := client.Get(id)
		if err != nil {
			if err = isNotRetryableError(err, timeoutErrorKeywords); err != nil {
				return false, err
			}
			return false, nil
		}
		if job == nil {
			return false, fmt.Errorf("job returned for %s is nil", id)
		}
		log(infraID).Info("Waiting for PowerVS job to complete", "id", id, "status", job.Status.State, "operation_action", *job.Operation.Action, "operation_target", *job.Operation.Target)

		if *job.Status.State == powerVSJobCompletedState {
			return true, nil
		}
		if *job.Status.State == powerVSJobFailedState {
			return false, fmt.Errorf("powerVS job failed. id: %s, message: %s", id, job.Status.Message)
		}
		return false, nil
	}

	return wait.PollImmediate(pollingInterval, timeout, f)
}

// destroyPowerVsCloudConnection destroying powervs cloud connection
func destroyPowerVsCloudConnection(ctx context.Context, options *DestroyInfraOptions, infra *Infra, cloudInstanceID string, session *ibmpisession.IBMPISession) error {
	client := instance.NewIBMPICloudConnectionClient(ctx, session, cloudInstanceID)
	jobClient := instance.NewIBMPIJobClient(ctx, session, cloudInstanceID)
	var err error

	var cloudConnName string
	// Destroying resources created for Hypershift infra creation
	if options.CloudConnection != "" {
		cloudConnName = options.CloudConnection
		var cloudConnL *models.CloudConnections
		cloudConnL, err = client.GetAll()
		if err != nil || cloudConnL == nil {
			return err
		}

		if len(cloudConnL.CloudConnections) < 1 {
			log(options.InfraID).Info("No Cloud Connection available to delete in PowerVS")
			return nil
		}

		for _, cloudConn := range cloudConnL.CloudConnections {
			if *cloudConn.Name == cloudConnName {
				// De-linking the VPC in cloud connection
				var vpc *models.CloudConnectionEndpointVPC
				var vpcL []*models.CloudConnectionVPC
				if cloudConn.Vpc != nil && cloudConn.Vpc.Enabled {
					for _, v := range cloudConn.Vpc.Vpcs {
						vpcName := fmt.Sprintf("%s-%s", options.InfraID, vpcNameSuffix)
						if (options.VPC != "" && v.Name == options.VPC) || v.Name == vpcName {
							continue
						}
						vpcL = append(vpcL, v)
					}
					vpc.Enabled = true
					vpc.Vpcs = vpcL
				}

				if _, _, err = client.Update(*cloudConn.CloudConnectionID, &models.CloudConnectionUpdate{Name: cloudConn.Name, Vpc: vpc}); err != nil {
					return err
				}

				// Removing the DHCP network from cloud connection
				if cloudConn.Networks != nil {
					for _, nw := range cloudConn.Networks {
						nwName := strings.ToLower(*nw.Name)
						if strings.Contains(nwName, "dhcp") && strings.Contains(nwName, "private") {
							if _, _, err = client.DeleteNetwork(*cloudConn.CloudConnectionID, *nw.NetworkID); err != nil {
								return err
							}
						}
					}
				}
			}
		}
	} else {

		deleteCloudConnection := func(id string) error {
			for retry := 0; retry < 5; retry++ {
				if err = deletePowerVsCloudConnection(options, id, client, jobClient); err == nil {
					return nil
				}
				log(options.InfraID).Info("retrying cloud connection deletion")
			}
			return err
		}

		if infra != nil && infra.CloudConnectionID != "" {
			return deleteCloudConnection(infra.CloudConnectionID)
		}
		var cloudConnL *models.CloudConnections
		cloudConnL, err = client.GetAll()
		if err != nil || cloudConnL == nil {
			return err
		}

		if len(cloudConnL.CloudConnections) < 1 {
			log(options.InfraID).Info("No Cloud Connection available to delete in PowerVS")
			return nil
		}

		cloudConnName = fmt.Sprintf("%s-%s", options.InfraID, cloudConnNameSuffix)

		for _, cloudConn := range cloudConnL.CloudConnections {
			if *cloudConn.Name == cloudConnName {
				return deleteCloudConnection(*cloudConn.CloudConnectionID)
			}
		}
	}
	return nil
}

// deletePowerVsCloudConnection deletes cloud connection id passed
func deletePowerVsCloudConnection(options *DestroyInfraOptions, id string, client *instance.IBMPICloudConnectionClient, jobClient *instance.IBMPIJobClient) error {
	log(options.InfraID).Info("Deleting cloud connection", "id", id)
	deleteJob, err := client.Delete(id)
	if err != nil {
		return err
	}
	if deleteJob == nil {
		return fmt.Errorf("error while deleting cloud connection, delete job returned is nil")
	}
	return monitorPowerVsJob(*deleteJob.ID, jobClient, options.InfraID, powerVSResourceDeletionTimeout)
}

// destroyVpc destroying vpc
func destroyVpc(ctx context.Context, options *DestroyInfraOptions, infra *Infra, resourceGroupID string, v1 *vpcv1.VpcV1, infraID string) error {
	if options.VPC != "" {
		log(infraID).Info("Skipping VPC deletion since its user provided")
		return nil
	}

	if infra != nil && infra.VPCID != "" {
		return deleteVpc(ctx, infra.VPCID, v1, infraID)
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
			log(infraID).Info("No VPC available to delete")
			return true, "", nil
		}

		// Will attempt VPC deletion only if the VPC is created by this script by matching the VPC naming convention followed by this script
		vpcName := fmt.Sprintf("%s-%s", options.InfraID, vpcNameSuffix)

		for _, vpc := range vpcL.Vpcs {
			if *vpc.Name == vpcName && strings.Contains(*vpc.CRN, options.VPCRegion) {
				if err = deleteVpc(ctx, *vpc.ID, v1, infraID); err != nil {
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
func deleteVpc(ctx context.Context, id string, v1 *vpcv1.VpcV1, infraID string) error {
	log(infraID).Info("Deleting VPC", "id", id)
	_, err := v1.DeleteVPCWithContext(ctx, &vpcv1.DeleteVPCOptions{ID: &id})
	return err
}

// destroyVpcSubnet destroying vpc subnet
func destroyVpcSubnet(ctx context.Context, options *DestroyInfraOptions, infra *Infra, resourceGroupID string, v1 *vpcv1.VpcV1, infraID string) error {
	if infra != nil && infra.VPCSubnetID != "" {
		return deleteVpcSubnet(ctx, infra.VPCSubnetID, v1, options)
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
			log(infraID).Info("No VPC Subnets available to delete")
			return true, "", nil
		}

		// Consider deleting only if the subnet created by this script by matching the VPC naming convention followed by this script
		vpcName := fmt.Sprintf("%s-%s", options.InfraID, vpcNameSuffix)

		for _, subnet := range subnetL.Subnets {
			if (*subnet.VPC.Name == vpcName || *subnet.VPC.Name == options.VPC) && strings.Contains(*subnet.Zone.Name, options.VPCRegion) {
				if err = deleteVpcSubnet(ctx, *subnet.ID, v1, options); err != nil {
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
func deleteVpcSubnet(ctx context.Context, id string, v1 *vpcv1.VpcV1, options *DestroyInfraOptions) error {
	// deleting the load balancer before proceeding to subnet deletion, since LB is an attached resource to the subnet
	var err error
	for retry := 0; retry < 5; retry++ {
		if err = destroyVpcLB(ctx, options, id, v1); err == nil {
			break
		}
		log(options.InfraID).Info("retrying VPC Load Balancer deletion")
	}

	if err != nil {
		log(options.InfraID).Error(err, "error destroying VPC Load Balancer")
		return fmt.Errorf("error destroying VPC Load Balancer %w", err)
	}

	// In case of user provided VPC delete only load balancer and not the subnet
	if options.VPC != "" {
		return nil
	}

	log(options.InfraID).Info("Deleting VPC subnet", "subnetId", id)

	if _, err := v1.DeleteSubnetWithContext(ctx, &vpcv1.DeleteSubnetOptions{ID: &id}); err != nil {
		return err
	}

	f := func() (bool, error) {
		if _, _, err := v1.GetSubnetWithContext(ctx, &vpcv1.GetSubnetOptions{ID: &id}); err != nil {
			if strings.Contains(err.Error(), "Subnet not found") {
				return true, nil
			} else {
				return false, err
			}
		}
		return false, nil
	}

	return wait.PollImmediate(pollingInterval, vpcResourceDeletionTimeout, f)
}

// destroyVpcLB destroys VPC Load Balancer
func destroyVpcLB(ctx context.Context, options *DestroyInfraOptions, subnetID string, v1 *vpcv1.VpcV1) error {

	deleteLB := func(id string) error {
		log(options.InfraID).Info("Deleting VPC LoadBalancer:", "id", id)
		if _, err := v1.DeleteLoadBalancerWithContext(ctx, &vpcv1.DeleteLoadBalancerOptions{ID: &id}); err != nil {
			return err
		}

		f := func() (bool, error) {
			_, _, err := v1.GetLoadBalancerWithContext(ctx, &vpcv1.GetLoadBalancerOptions{ID: &id})
			if err != nil && strings.Contains(err.Error(), "cannot be found") {
				return true, nil
			}
			return false, err
		}

		return wait.PollImmediate(pollingInterval, vpcResourceDeletionTimeout, f)
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
			log(options.InfraID).Info("No Load Balancers available to delete")
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
			ResourcePlanID:  utilpointer.String(cosResourcePlanID),
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
func deleteCOS(ctx context.Context, options *DestroyInfraOptions, resourceGroupID string) error {
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
			log(options.InfraID).Info("No COS Instance available to delete")
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

	log(options.InfraID).Info("Deleting COS Instance", "name", cosInstanceName)
	_, err = rcv2.DeleteResourceInstance(&resourcecontrollerv2.DeleteResourceInstanceOptions{ID: cosInstance.ID})

	return err
}
