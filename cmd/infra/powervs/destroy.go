package powervs

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/IBM-Cloud/power-go-client/power/models"
	"io/ioutil"
	"k8s.io/apimachinery/pkg/util/errors"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/IBM-Cloud/power-go-client/clients/instance"
	"github.com/IBM-Cloud/power-go-client/ibmpisession"
	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"
	"github.com/IBM/vpc-go-sdk/vpcv1"
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
)

// DestroyInfraOptions command line options to destroy infra created in IBMCloud for Hypershift
type DestroyInfraOptions struct {
	InfraID                string
	InfrastructureJson     string
	ResourceGroup          string
	PowerVSRegion          string
	PowerVSZone            string
	PowerVSCloudInstanceID string
	PowerVSDhcpID          string
	PowerVSCloudConnection string
	VpcRegion              string
	Vpc                    string
	Debug                  bool
}

func NewDestroyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "powervs",
		Short:        "Destroys PowerVS infrastructure resources for a cluster",
		SilenceUsage: true,
	}

	opts := DestroyInfraOptions{}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID with which to tag IBM Cloud resources")
	cmd.Flags().StringVar(&opts.InfrastructureJson, "infra-json", opts.InfrastructureJson, "Result of ./hypershift infra create powervs")
	cmd.Flags().StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "IBM Cloud Resource Group")
	cmd.Flags().StringVar(&opts.VpcRegion, "vpc-region", opts.VpcRegion, "IBM Cloud VPC Infra Region")
	cmd.Flags().StringVar(&opts.Vpc, "vpc", opts.Vpc, "IBM Cloud VPC")
	cmd.Flags().StringVar(&opts.PowerVSRegion, "powervs-region", opts.PowerVSRegion, "PowerVS Region")
	cmd.Flags().StringVar(&opts.PowerVSZone, "powervs-zone", opts.PowerVSZone, "IBM Cloud PowerVS Zone")
	cmd.Flags().StringVar(&opts.PowerVSCloudConnection, "powervs-cloud-connection", opts.PowerVSCloudConnection, "IBM Cloud PowerVS Cloud Connection")
	cmd.Flags().StringVar(&opts.PowerVSCloudInstanceID, "powervs-cloud-instance-id", opts.PowerVSCloudInstanceID, "IBM PowerVS Cloud Instance ID")
	cmd.Flags().BoolVar(&opts.Debug, "debug", opts.Debug, "Enabling this will result in debug logs will be printed")

	cmd.MarkFlagRequired("resource-group")
	cmd.MarkFlagRequired("infra-id")
	cmd.MarkFlagRequired("powervs-region")
	cmd.MarkFlagRequired("powervs-zone")
	cmd.MarkFlagRequired("vpc-region")

	// these options are only for development and testing purpose, user can pass these flags
	// to destroy the resource created inside these resources for hypershift infra purpose
	cmd.Flags().MarkHidden("vpc")
	cmd.Flags().MarkHidden("powervs-cloud-connection")
	cmd.Flags().MarkHidden("powervs-cloud-instance-id")

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
func (options *DestroyInfraOptions) Run(ctx context.Context) (err error) {
	var infra *Infra
	if len(options.InfrastructureJson) > 0 {
		rawInfra, err := ioutil.ReadFile(options.InfrastructureJson)
		if err != nil {
			return fmt.Errorf("failed to read infra json file: %w", err)
		}
		infra = &Infra{}
		if err = json.Unmarshal(rawInfra, infra); err != nil {
			return fmt.Errorf("failed to load infra json: %w", err)
		}
	}
	err = options.DestroyInfra(infra)
	if err != nil {
		return err
	}
	return nil
}

// DestroyInfra infra destruction orchestration
func (options *DestroyInfraOptions) DestroyInfra(infra *Infra) (err error) {
	log(options.InfraID).Info("Destroy Infra Started")

	// if IBMCLOUD_API_KEY is not set, infra cannot be set up.
	if cloudApiKey == "" {
		return fmt.Errorf("IBMCLOUD_API_KEY not set")
	}

	accountID, err := getAccount(getIAMAuth())
	if err != nil {
		return fmt.Errorf("error retrieving account ID %w", err)
	}

	resourceGroupID, err := getResourceGroupID(options.ResourceGroup, accountID)
	if err != nil {
		return err
	}

	var powerVsCloudInstanceID string

	serviceID, servicePlanID, err := getServiceInfo(powerVSService, powerVSServicePlan)
	if err != nil {
		return err
	}

	var skipPowerVs bool
	errL := make([]error, 0)

	// getting the powervs cloud instance id
	if infra != nil && infra.PowerVSCloudInstanceID != "" {
		powerVsCloudInstanceID = infra.PowerVSCloudInstanceID
	} else if options.PowerVSCloudInstanceID != "" {
		_, err := validateCloudInstanceByID(options.PowerVSCloudInstanceID)
		if err != nil {
			errL = append(errL, err)
			skipPowerVs = true
		}
		powerVsCloudInstanceID = options.PowerVSCloudInstanceID
	} else {
		cloudInstanceName := fmt.Sprintf("%s-%s", options.InfraID, cloudInstanceNameSuffix)
		cloudInstance, err := validateCloudInstanceByName(cloudInstanceName, resourceGroupID, options.PowerVSZone, serviceID, servicePlanID)
		if err != nil {
			errL = append(errL, err)
			skipPowerVs = true
		}
		if cloudInstance != nil {
			powerVsCloudInstanceID = *cloudInstance.GUID
		}
	}

	var session *ibmpisession.IBMPISession
	if !skipPowerVs {
		session, err = createPowerVSSession(accountID, options.PowerVSRegion, options.PowerVSZone, options.Debug)
		if err != nil {
			return err
		}

		err = destroyPowerVsCloudConnection(options, infra, powerVsCloudInstanceID, session)
		if err != nil {
			errL = append(errL, fmt.Errorf("error destroying powervs cloud connection: %w", err))
			log(options.InfraID).Error(err, "error destroying powervs cloud connection")
		}
	}

	v1, err := createVpcService(options.VpcRegion, options.InfraID)
	if err != nil {
		return err
	}

	err = destroyVpcSubnet(options, infra, resourceGroupID, v1, options.InfraID)
	if err != nil {
		errL = append(errL, fmt.Errorf("error destroying vpc subnet: %w", err))
		log(options.InfraID).Error(err, "error destroying vpc subnet")
	}

	err = destroyVpc(options, infra, resourceGroupID, v1, options.InfraID)
	if err != nil {
		errL = append(errL, fmt.Errorf("error destroying vpc: %w", err))
		log(options.InfraID).Error(err, "error destroying vpc")
	}

	if !skipPowerVs {
		err = destroyPowerVsCloudInstance(options, infra, powerVsCloudInstanceID, session)
		if err != nil {
			errL = append(errL, fmt.Errorf("error destroying powervs cloud instance: %w", err))
			log(options.InfraID).Error(err, "error destroying powervs cloud instance")
		}
	}

	log(options.InfraID).Info("Destroy Infra Completed")

	if err := errors.NewAggregate(errL); err != nil {
		return fmt.Errorf("error in destroying infra: %w", err)
	}

	return
}

// destroyPowerVsDhcpServer destroying powervs dhcp server
func destroyPowerVsDhcpServer(infra *Infra, cloudInstanceID string, session *ibmpisession.IBMPISession, infraID string) (err error) {
	client := instance.NewIBMPIDhcpClient(context.Background(), session, cloudInstanceID)
	if infra != nil && infra.PowerVSDhcpID != "" {
		log(infraID).Info("Deleting DHCP server", "id", infra.PowerVSDhcpID)
		return client.Delete(infra.PowerVSDhcpID)
	}

	dhcpServers, err := client.GetAll()
	if err != nil {
		return
	}

	if dhcpServers == nil || len(dhcpServers) < 1 {
		return fmt.Errorf("no dhcp servers available to delete in powervs")
	}

	dhcpID := *dhcpServers[0].ID
	log(infraID).Info("Deleting DHCP server", "id", dhcpID)
	err = client.Delete(dhcpID)
	if err != nil {
		return
	}

	instanceClient := instance.NewIBMPIInstanceClient(context.Background(), session, cloudInstanceID)

	// TO-DO: need to replace the logic of waiting for dhcp service deletion by using jobReference.
	// jobReference is not yet added in SDK
	f := func() (cond bool, err error) {
		dhcpInstance, err := instanceClient.Get(dhcpID)
		if err != nil {
			errMsg := err.Error()
			// when instance becomes does not exist, infra destroy can proceed
			if strings.Contains(errMsg, "pvm-instance does not exist") {
				err = nil
				cond = true
			}
			return
		}

		if dhcpInstance == nil {
			err = fmt.Errorf("dhcpInstance is nil")
			return
		}

		log(infraID).Info("Waiting for DhcpServer to destroy", "id", *dhcpInstance.PvmInstanceID, "status", *dhcpInstance.Status)
		if *dhcpInstance.Status == dhcpInstanceShutOffState || *dhcpInstance.Status == dhcpServiceErrorState {
			cond = true
			return
		}

		return
	}

	err = wait.PollImmediate(pollingInterval, dhcpServerDeletionTimeout, f)

	return
}

// destroyPowerVsCloudInstance destroying powervs cloud instance
func destroyPowerVsCloudInstance(options *DestroyInfraOptions, infra *Infra, cloudInstanceID string, session *ibmpisession.IBMPISession) (err error) {
	rcv2, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{Authenticator: getIAMAuth()})
	if err != nil {
		return
	}

	if options.PowerVSCloudInstanceID != "" {
		// In case of user provided cloud instance delete only DHCP server
		err = destroyPowerVsDhcpServer(infra, cloudInstanceID, session, options.InfraID)
	} else {
		for retry := 0; retry < 5; retry++ {
			log(options.InfraID).Info("Deleting PowerVS cloud instance", "id", cloudInstanceID)
			_, err = rcv2.DeleteResourceInstance(&resourcecontrollerv2.DeleteResourceInstanceOptions{ID: &cloudInstanceID})

			if err != nil {
				log(options.InfraID).Error(err, "error in deleting powervs cloud instance")
				continue
			}

			f := func() (cond bool, err error) {
				resourceInst, resp, err := rcv2.GetResourceInstance(&resourcecontrollerv2.GetResourceInstanceOptions{ID: &cloudInstanceID})
				if err != nil {
					log(options.InfraID).Error(err, "error in querying deleted cloud instance", "resp", resp.String())
					return
				}

				log(options.InfraID).Info("resp", "code", resp.StatusCode, "message", resp.String())

				if resp.StatusCode >= 400 {
					err = fmt.Errorf("retrying due to resp code is %d and message is %s", resp.StatusCode, resp.String())
					return
				}
				if resourceInst != nil {
					if *resourceInst.State == powerVSCloudInstanceRemovedState {
						cond = true
						return
					}

					log(options.InfraID).Info("Waiting for PowerVS cloud instance deletion", "status", *resourceInst.State, "lastOp", resourceInst.LastOperation)
				}

				return
			}

			err = wait.PollImmediate(pollingInterval, cloudInstanceDeletionTimeout, f)
			if err == nil {
				break
			}
			log(options.InfraID).Info("Retrying cloud instance deletion ...")
		}
	}
	return
}

// monitorPowerVsJob monitoring the submitted deletion job
func monitorPowerVsJob(id string, client *instance.IBMPIJobClient, infraID string, timeout time.Duration) (err error) {

	f := func() (cond bool, err error) {
		job, err := client.Get(id)
		if err != nil {
			return
		}
		if job == nil {
			err = fmt.Errorf("job returned for %s is nil", id)
			return
		}
		log(infraID).Info("Waiting for PowerVS job to complete", "id", id, "status", job.Status.State, "operation_action", *job.Operation.Action, "operation_target", *job.Operation.Target)

		if *job.Status.State == powerVSJobCompletedState || *job.Status.State == powerVSJobFailedState {
			cond = true
			return
		}
		return
	}

	return wait.PollImmediate(pollingInterval, timeout, f)
}

// destroyPowerVsCloudConnection destroying powervs cloud connection
func destroyPowerVsCloudConnection(options *DestroyInfraOptions, infra *Infra, cloudInstanceID string, session *ibmpisession.IBMPISession) (err error) {
	client := instance.NewIBMPICloudConnectionClient(context.Background(), session, cloudInstanceID)
	jobClient := instance.NewIBMPIJobClient(context.Background(), session, cloudInstanceID)

	var cloudConnName string
	// Destroying resources created for Hypershift infra creation
	if options.PowerVSCloudConnection != "" {
		cloudConnName = options.PowerVSCloudConnection
		var cloudConnL *models.CloudConnections
		cloudConnL, err = client.GetAll()
		if err != nil || cloudConnL == nil {
			return
		}

		if len(cloudConnL.CloudConnections) < 1 {
			return fmt.Errorf("no cloud connection available to unset in powervs")
		}

		for _, cloudConn := range cloudConnL.CloudConnections {
			if *cloudConn.Name == cloudConnName {
				// De-linking the VPC in cloud connection
				var vpc *models.CloudConnectionEndpointVPC
				var vpcL []*models.CloudConnectionVPC
				if cloudConn.Vpc != nil && cloudConn.Vpc.Enabled {
					for _, v := range cloudConn.Vpc.Vpcs {
						vpcName := fmt.Sprintf("%s-%s", options.InfraID, vpcNameSuffix)
						if (options.Vpc != "" && v.Name == options.Vpc) || v.Name == vpcName {
							continue
						}
						vpcL = append(vpcL, v)
					}
					vpc.Enabled = true
					vpc.Vpcs = vpcL
				}

				_, _, err = client.Update(*cloudConn.CloudConnectionID, &models.CloudConnectionUpdate{Name: cloudConn.Name, Vpc: vpc})
				if err != nil {
					return
				}

				// Removing the DHCP network from cloud connection
				if cloudConn.Networks != nil {
					for _, nw := range cloudConn.Networks {
						nwName := strings.ToLower(*nw.Name)
						if strings.Contains(nwName, "dhcp") && strings.Contains(nwName, "private") {
							_, _, err = client.DeleteNetwork(*cloudConn.CloudConnectionID, *nw.NetworkID)
							if err != nil {
								return
							}
						}
					}
				}
			}
		}
	} else {
		if infra != nil && infra.PowerVSCloudConnectionID != "" {
			return deletePowerVsCloudConnection(options, infra.PowerVSCloudConnectionID, client, jobClient)
		}
		var cloudConnL *models.CloudConnections
		cloudConnL, err = client.GetAll()
		if err != nil || cloudConnL == nil {
			return
		}

		if len(cloudConnL.CloudConnections) < 1 {
			return fmt.Errorf("no cloud connection available to delete in powervs")
		}

		cloudConnName = fmt.Sprintf("%s-%s", options.InfraID, cloudConnNameSuffix)

		for _, cloudConn := range cloudConnL.CloudConnections {
			if *cloudConn.Name == cloudConnName {
				return deletePowerVsCloudConnection(options, *cloudConn.CloudConnectionID, client, jobClient)
			}
		}
	}
	return
}

// deletePowerVsCloudConnection deletes cloud connection id passed
func deletePowerVsCloudConnection(options *DestroyInfraOptions, id string, client *instance.IBMPICloudConnectionClient, jobClient *instance.IBMPIJobClient) (err error) {
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
func destroyVpc(options *DestroyInfraOptions, infra *Infra, resourceGroupID string, v1 *vpcv1.VpcV1, infraID string) (err error) {
	if infra != nil && infra.VpcID != "" {
		return deleteVpc(infra.VpcID, v1, infraID)
	}

	f := func(start string) (isDone bool, nextUrl string, err error) {
		vpcListOpt := vpcv1.ListVpcsOptions{ResourceGroupID: &resourceGroupID}
		if start != "" {
			vpcListOpt.Start = &start
		}

		vpcL, _, err := v1.ListVpcs(&vpcListOpt)
		if err != nil {
			return
		}

		if vpcL == nil || len(vpcL.Vpcs) <= 0 {
			err = fmt.Errorf("no vpcs available")
			return
		}

		// Will attempt VPC deletion only if the VPC is created by this script by matching the VPC naming convention followed by this script
		vpcName := fmt.Sprintf("%s-%s", options.InfraID, vpcNameSuffix)

		for _, vpc := range vpcL.Vpcs {
			if *vpc.Name == vpcName && strings.Contains(*vpc.CRN, options.VpcRegion) {
				err = deleteVpc(*vpc.ID, v1, infraID)
				isDone = true
				return
			}
		}
		if vpcL.Next != nil && *vpcL.Next.Href != "" {
			nextUrl = *vpcL.Next.Href
			return
		}
		isDone = true
		return
	}

	return pagingHelper(f)
}

// deleteVpc deletes the vpc id passed
func deleteVpc(id string, v1 *vpcv1.VpcV1, infraID string) (err error) {
	log(infraID).Info("Deleting VPC", "id", id)
	_, err = v1.DeleteVPC(&vpcv1.DeleteVPCOptions{ID: &id})
	return err
}

// destroyVpcSubnet destroying vpc subnet
func destroyVpcSubnet(options *DestroyInfraOptions, infra *Infra, resourceGroupID string, v1 *vpcv1.VpcV1, infraID string) (err error) {
	if infra != nil && infra.VpcSubnetID != "" {
		return deleteVpcSubnet(infra.VpcSubnetID, v1, infraID)
	}

	f := func(start string) (isDone bool, nextUrl string, err error) {

		listSubnetOpt := vpcv1.ListSubnetsOptions{ResourceGroupID: &resourceGroupID}
		if start != "" {
			listSubnetOpt.Start = &start
		}
		subnetL, _, err := v1.ListSubnets(&listSubnetOpt)
		if err != nil {
			return
		}

		if subnetL == nil || len(subnetL.Subnets) <= 0 {
			err = fmt.Errorf("no subnets available")
			return
		}

		// Consider deleting only if the subnet created by this script by matching the VPC naming convention followed by this script
		vpcName := fmt.Sprintf("%s-%s", options.InfraID, vpcNameSuffix)

		for _, subnet := range subnetL.Subnets {
			if *subnet.VPC.Name == vpcName && strings.Contains(*subnet.Zone.Name, options.VpcRegion) {
				err = deleteVpcSubnet(*subnet.ID, v1, infraID)
				isDone = true
				return
			}
		}

		// For paging over next set of resources getting the start token and passing it for next iteration
		if subnetL.Next != nil && *subnetL.Next.Href != "" {
			nextUrl = *subnetL.Next.Href
			return
		}

		isDone = true
		return
	}

	return pagingHelper(f)
}

// deleteVpcSubnet deletes the subnet id passed
func deleteVpcSubnet(id string, v1 *vpcv1.VpcV1, infraID string) (err error) {
	log(infraID).Info("Deleting VPC subnet", "subnetId", id)
	_, err = v1.DeleteSubnet(&vpcv1.DeleteSubnetOptions{ID: &id})

	if err != nil {
		return err
	}

	f := func() (cond bool, err error) {
		_, _, err = v1.GetSubnet(&vpcv1.GetSubnetOptions{ID: &id})
		if err != nil && strings.Contains(err.Error(), "Subnet not found") {
			err = nil
			cond = true
			return
		}
		return
	}

	err = wait.PollImmediate(pollingInterval, vpcResourceDeletionTimeout, f)
	return
}
