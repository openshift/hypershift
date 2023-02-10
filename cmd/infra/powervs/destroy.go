package powervs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/IBM-Cloud/power-go-client/clients/instance"
	"github.com/IBM-Cloud/power-go-client/ibmpisession"
	"github.com/IBM-Cloud/power-go-client/power/models"
	"github.com/IBM/networking-go-sdk/dnsrecordsv1"
	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"
	"github.com/IBM/vpc-go-sdk/vpcv1"
)

const (
	// Time duration for monitoring the resource readiness
	resourceDeletionTimeout    = time.Minute * 5
	vpcResourceDeletionTimeout = time.Minute * 2

	// Resource desired states
	cloudInstanceRemovedState = "removed"
	piJobCompletedState       = "completed"
	piJobFailedState          = "failed"
	dhcpInstanceShutOffState  = "SHUTOFF"

	// Resource name prefix
	vpcLBNamePrefix = "kube"
)

// DestroyInfraOptions command line options to destroy infra created in IBMCloud for hypershift
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
	cmd.Flags().StringVar(&opts.VPC, "vpc", opts.VPC, "IBM Cloud VPC")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "IBM Cloud PowerVS Region")
	cmd.Flags().StringVar(&opts.Zone, "zone", opts.Zone, "IBM Cloud PowerVS Zone")
	cmd.Flags().StringVar(&opts.CloudConnection, "cloud-connection", opts.CloudConnection, "IBM Cloud PowerVS Cloud Connection")
	cmd.Flags().StringVar(&opts.CloudInstanceID, "cloud-instance-id", opts.CloudInstanceID, "IBM PowerVS Cloud Instance ID")
	cmd.Flags().BoolVar(&opts.Debug, "debug", opts.Debug, "Enabling this will result in debug logs will be printed")

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
	_ = cmd.Flags().MarkHidden("cloud-connection")
	_ = cmd.Flags().MarkHidden("cloud-instance-id")

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

// Run destroys Infra
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

	cloudAPIKey, err = GetAPIKey()
	if err != nil {
		return fmt.Errorf("error retrieving IBM Cloud API Key: %w", err)
	}

	// if CLOUD API KEY is not set, infra cannot be set up.
	if cloudAPIKey == "" {
		return fmt.Errorf("cloud API Key not set. Set it with IBMCLOUD_API_KEY env var or set file path containing API Key credential in IBMCLOUD_CREDENTIALS")
	}

	accountID, err := getAccountID(ctx, getIAMAuth())
	if err != nil {
		return fmt.Errorf("error retrieving account ID %w", err)
	}

	resourceGroupID, err := getResourceGroupID(ctx, options.ResourceGroup, accountID)
	if err != nil {
		return err
	}

	serviceID, servicePlanID, err := getServiceInfo(ctx, powerVSServiceName, powerVSServicePlanName)
	if err != nil {
		return err
	}

	errL := make([]error, 0)

	if err = deleteDNSRecords(ctx, options); err != nil {
		errL = append(errL, fmt.Errorf("error deleting dns record from cis domain: %w", err))
		log(options.InfraID).Error(err, "error deleting dns record from cis domain")
	}

	if err = deleteSecrets(options.Name, options.Namespace, accountID, resourceGroupID); err != nil {
		errL = append(errL, fmt.Errorf("error deleting secrets: %w", err))
		log(options.InfraID).Error(err, "error deleting secrets")
	}

	var cloudInstanceID string
	var skipPowerVS bool

	// getting the cloud instance id
	if infra != nil && infra.CloudInstanceID != "" {
		cloudInstanceID = infra.CloudInstanceID
	} else if options.CloudInstanceID != "" {
		if _, err = validateCloudInstanceByID(ctx, options.CloudInstanceID); err != nil {
			errL = append(errL, err)
			skipPowerVS = true
		}
		cloudInstanceID = options.CloudInstanceID
	} else {
		cloudInstanceName := fmt.Sprintf("%s-%s", options.InfraID, cloudInstanceNameSuffix)
		var cloudInstance *resourcecontrollerv2.ResourceInstance
		cloudInstance, err = validateCloudInstanceByName(ctx, cloudInstanceName, resourceGroupID, options.Zone, serviceID, servicePlanID)
		if err != nil {
			if err.Error() == cloudInstanceNotFound(cloudInstanceName).Error() {
				log(options.InfraID).Info("No Cloud Instance available to delete")
			} else {
				errL = append(errL, err)
			}
			skipPowerVS = true
		}

		if cloudInstance != nil {
			cloudInstanceID = *cloudInstance.GUID
		}
	}

	var session *ibmpisession.IBMPISession
	if !skipPowerVS {
		session, err = createPowerVSSession(accountID, options.Region, options.Zone, options.Debug)
		if err != nil {
			return err
		}

		if err = destroyCloudConnection(ctx, options, infra, cloudInstanceID, session); err != nil {
			errL = append(errL, fmt.Errorf("error destroying powervs cloud connection: %w", err))
			log(options.InfraID).Error(err, "error destroying cloud connection")
		}
	}

	v1, err := createVPCService(options.VPCRegion)
	if err != nil {
		return err
	}

	if err = destroyVPCSubnet(ctx, options, infra, resourceGroupID, v1, options.InfraID); err != nil {
		errL = append(errL, fmt.Errorf("error destroying vpc subnet: %w", err))
		log(options.InfraID).Error(err, "error destroying vpc subnet")
	}

	if err = destroyVPC(ctx, options, infra, resourceGroupID, v1, options.InfraID); err != nil {
		errL = append(errL, fmt.Errorf("error destroying vpc: %w", err))
		log(options.InfraID).Error(err, "error destroying vpc")
	}

	if !skipPowerVS {
		if err = destroyCloudInstance(ctx, options, infra, cloudInstanceID, session); err != nil {
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
	listDNSRecordsOpt := &dnsrecordsv1.ListAllDnsRecordsOptions{Name: &recordName}

	dnsRecordsL, _, err := dnsRecordsV1.ListAllDnsRecordsWithContext(ctx, listDNSRecordsOpt)
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
func deleteSecrets(name, namespace, accountID string, resourceGroupID string) error {
	err := deleteServiceID(name, cloudAPIKey, accountID, resourceGroupID,
		kubeCloudControllerManagerCR, kubeCloudControllerManagerCreds, namespace)
	if err != nil {
		return fmt.Errorf("error deleting kube cloud controller manager secret: %w", err)
	}

	err = deleteServiceID(name, cloudAPIKey, accountID, resourceGroupID,
		nodePoolManagementCR, nodePoolManagementCreds, namespace)
	if err != nil {
		return fmt.Errorf("error deleting nodepool management secret: %w", err)
	}

	err = deleteServiceID(name, cloudAPIKey, accountID, "",
		ingressOperatorCR, ingressOperatorCreds, namespace)
	if err != nil {
		return fmt.Errorf("error deleting ingress operator secret: %w", err)
	}

	err = deleteServiceID(name, cloudAPIKey, accountID, resourceGroupID,
		storageOperatorCR, storageOperatorCreds, namespace)
	if err != nil {
		return fmt.Errorf("error deleting ingress operator secret: %w", err)
	}

	return nil
}

// destroyDHCPServer destroying powervs dhcp server
func destroyDHCPServer(ctx context.Context, infra *Infra, cloudInstanceID string, session *ibmpisession.IBMPISession, infraID string) error {
	client := instance.NewIBMPIDhcpClient(ctx, session, cloudInstanceID)
	if infra != nil && infra.DHCPID != "" {
		log(infraID).Info("Deleting DHCP server", "id", infra.DHCPID)
		return client.Delete(infra.DHCPID)
	}

	dhcpServers, err := client.GetAll()
	if err != nil {
		return err
	}

	if dhcpServers == nil || len(dhcpServers) < 1 {
		log(infraID).Info("No DHCP servers available to delete in PowerVS")
		return nil
	}

	dhcpID := *dhcpServers[0].ID
	log(infraID).Info("Deleting DHCP server", "id", dhcpID)
	err = client.Delete(dhcpID)
	if err != nil {
		return err
	}

	instanceClient := instance.NewIBMPIInstanceClient(ctx, session, cloudInstanceID)

	// TO-DO: need to replace the logic of waiting for dhcp service deletion by using jobReference.
	// jobReference is not yet added in SDK
	f := func() (bool, error) {
		dhcpInstance, err := instanceClient.Get(dhcpID)
		if err != nil {
			if err = isNotRetryableError(err, timeoutErrorKeywords); err == nil {
				return false, nil
			}
			errMsg := err.Error()
			// when instance becomes does not exist, infra destroy can proceed
			if strings.Contains(errMsg, "pvm-instance does not exist") {
				err = nil
				return true, nil
			}
			return false, err
		}

		if dhcpInstance == nil {
			return false, fmt.Errorf("dhcpInstance is nil")
		}

		log(infraID).Info("Waiting for DhcpServer to destroy", "id", *dhcpInstance.PvmInstanceID, "status", *dhcpInstance.Status)
		if *dhcpInstance.Status == dhcpInstanceShutOffState || *dhcpInstance.Status == dhcpServiceErrorState {
			return true, nil
		}

		return false, nil
	}

	return wait.PollImmediate(pollingInterval, resourceDeletionTimeout, f)
}

// destroyCloudInstance destroying cloud instance
func destroyCloudInstance(ctx context.Context, options *DestroyInfraOptions, infra *Infra, cloudInstanceID string, session *ibmpisession.IBMPISession) error {
	rcv2, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{Authenticator: getIAMAuth()})
	if err != nil {
		return err
	}

	if options.CloudInstanceID != "" {
		// In case of user provided cloud instance delete only DHCP server
		err = destroyDHCPServer(ctx, infra, cloudInstanceID, session, options.InfraID)
	} else {
		for retry := 0; retry < 5; retry++ {
			log(options.InfraID).Info("Deleting cloud instance", "id", cloudInstanceID)
			if _, err = rcv2.DeleteResourceInstanceWithContext(ctx, &resourcecontrollerv2.DeleteResourceInstanceOptions{ID: &cloudInstanceID}); err != nil {
				log(options.InfraID).Error(err, "error in deleting cloud instance")
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
					if *resourceInst.State == cloudInstanceRemovedState {
						return true, nil
					}

					log(options.InfraID).Info("Waiting for cloud instance deletion", "status", *resourceInst.State, "lastOp", resourceInst.LastOperation)
				}

				return false, nil
			}

			if err = wait.PollImmediate(pollingInterval, resourceDeletionTimeout, f); err == nil {
				break
			}
			log(options.InfraID).Info("Retrying cloud instance deletion ...")
		}
	}
	return err
}

// monitorPIJob monitoring the job running in background created by IBM PI clients
func monitorPIJob(id string, client *instance.IBMPIJobClient, infraID string, timeout time.Duration) error {
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
		log(infraID).Info("Waiting for job to get to completed", "id", id, "status", job.Status.State, "operation_action", *job.Operation.Action, "operation_target", *job.Operation.Target)

		if *job.Status.State == piJobCompletedState || *job.Status.State == piJobFailedState {
			return true, nil
		}
		return false, nil
	}

	return wait.PollImmediate(pollingInterval, timeout, f)
}

// destroyCloudConnection destroying powervs cloud connection
func destroyCloudConnection(ctx context.Context, options *DestroyInfraOptions, infra *Infra, cloudInstanceID string, session *ibmpisession.IBMPISession) error {
	client := instance.NewIBMPICloudConnectionClient(ctx, session, cloudInstanceID)
	jobClient := instance.NewIBMPIJobClient(ctx, session, cloudInstanceID)
	var err error

	var cloudConnName string
	// Destroying resources created for hypershift infra creation
	if options.CloudConnection != "" {
		cloudConnName = options.CloudConnection
		var cloudConnL *models.CloudConnections

		cloudConnL, err = client.GetAll()
		if err != nil || cloudConnL == nil {
			return err
		}

		if len(cloudConnL.CloudConnections) < 1 {
			log(options.InfraID).Info("No Cloud Connection available to delete")
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
		deleteCC := func(id string) error {
			for retry := 0; retry < 5; retry++ {
				if err = deleteCloudConnection(options, id, client, jobClient); err == nil {
					return nil
				}
				log(options.InfraID).Info("retrying cloud connection deletion")
			}
			return err
		}

		if infra != nil && infra.CloudConnectionID != "" {
			return deleteCC(infra.CloudConnectionID)
		}

		var cloudConnL *models.CloudConnections
		cloudConnL, err = client.GetAll()
		if err != nil || cloudConnL == nil {
			return err
		}

		if len(cloudConnL.CloudConnections) < 1 {
			log(options.InfraID).Info("No Cloud Connection available to delete")
			return nil
		}

		cloudConnName = fmt.Sprintf("%s-%s", options.InfraID, cloudConnNameSuffix)

		for _, cloudConn := range cloudConnL.CloudConnections {
			if *cloudConn.Name == cloudConnName {
				return deleteCC(*cloudConn.CloudConnectionID)
			}
		}
	}
	return nil
}

// deleteCloudConnection deletes cloud connection id passed
func deleteCloudConnection(options *DestroyInfraOptions, id string, client *instance.IBMPICloudConnectionClient, jobClient *instance.IBMPIJobClient) error {
	log(options.InfraID).Info("Deleting cloud connection", "id", id)

	deleteJob, err := client.Delete(id)
	if err != nil {
		return err
	}
	if deleteJob == nil {
		return fmt.Errorf("error while deleting cloud connection, delete job returned is nil")
	}

	return monitorPIJob(*deleteJob.ID, jobClient, options.InfraID, resourceDeletionTimeout)
}

// destroyVPC destroying vpc
func destroyVPC(ctx context.Context, options *DestroyInfraOptions, infra *Infra, resourceGroupID string, v1 *vpcv1.VpcV1, infraID string) error {
	if infra != nil && infra.VPCID != "" {
		return deleteVPC(ctx, infra.VPCID, v1, infraID)
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
				if err = deleteVPC(ctx, *vpc.ID, v1, infraID); err != nil {
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

// deleteVPC deletes the vpc id passed
func deleteVPC(ctx context.Context, id string, v1 *vpcv1.VpcV1, infraID string) error {
	log(infraID).Info("Deleting VPC", "id", id)
	_, err := v1.DeleteVPCWithContext(ctx, &vpcv1.DeleteVPCOptions{ID: &id})
	return err
}

// destroyVPCSubnet destroying vpc subnet
func destroyVPCSubnet(ctx context.Context, options *DestroyInfraOptions, infra *Infra, resourceGroupID string, v1 *vpcv1.VpcV1, infraID string) error {
	if infra != nil && infra.VPCSubnetID != "" {
		return deleteVPCSubnet(ctx, infra.VPCSubnetID, v1, options)
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
			if *subnet.VPC.Name == vpcName && strings.Contains(*subnet.Zone.Name, options.VPCRegion) {
				if err = deleteVPCSubnet(ctx, *subnet.ID, v1, options); err != nil {
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

// deleteVPCSubnet deletes the subnet id passed and LB attached to it
func deleteVPCSubnet(ctx context.Context, id string, v1 *vpcv1.VpcV1, options *DestroyInfraOptions) error {
	// deleting the load balancer before proceeding to subnet deletion, since LB is an attached resource to the subnet
	var err error
	for retry := 0; retry < 5; retry++ {
		if err = destroyVPCLB(ctx, options, id, v1); err == nil {
			break
		}
		log(options.InfraID).Info("retrying VPC Load Balancer deletion")
	}

	if err != nil {
		log(options.InfraID).Error(err, "error destroying VPC Load Balancer")
		return fmt.Errorf("error destroying VPC Load Balancer %w", err)
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

// destroyVPCLB destroys VPC Load Balancer
func destroyVPCLB(ctx context.Context, options *DestroyInfraOptions, subnetID string, v1 *vpcv1.VpcV1) error {
	deleteLB := func(id string) error {
		log(options.InfraID).Info("Deleting VPC load balancer:", "id", id)
		if _, err := v1.DeleteLoadBalancerWithContext(ctx, &vpcv1.DeleteLoadBalancerOptions{ID: &id}); err != nil {
			return err
		}

		f := func() (bool, error) {
			lb, _, err := v1.GetLoadBalancerWithContext(ctx, &vpcv1.GetLoadBalancerOptions{ID: &id})
			if err != nil && strings.Contains(err.Error(), "cannot be found") {
				return true, nil
			}
			log(options.InfraID).Info("Waiting for VPC load balancer deletion", "status", *lb.ProvisioningStatus)
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
		lbName := fmt.Sprintf("%s-%s", vpcLBNamePrefix, options.Name)

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
