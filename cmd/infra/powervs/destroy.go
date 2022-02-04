package powervs

import (
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/openshift/hypershift/cmd/log"
)

const (
	// Time duration for monitoring the resource readiness
	cloudInstanceDeletionTimeout   = time.Minute * 10
	powerVSResourceDeletionTimeout = time.Minute * 5
	dhcpServerDeletionTimeout      = time.Minute * 10

	// Resource desired states
	powerVSCloudInstanceRemovedState = "removed"
	powerVSJobCompletedState         = "completed"
	powerVSJobFailedState            = "failed"
	dhcpInstanceShutOffState         = "SHUTOFF"
)

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

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID with which to tag AWS resources (required)")
	cmd.Flags().StringVar(&opts.InfrastructureJson, "infrastructure-json", opts.InfrastructureJson, "Result of ./hypershift infra create powervs")
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

	// these options are only for development and testing purpose, user can destroy these resources by passing these flags
	cmd.Flags().MarkHidden("vpc")
	cmd.Flags().MarkHidden("powervs-cloud-connection")
	cmd.Flags().MarkHidden("powervs-cloud-instance-id")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context()); err != nil {
			log.Log.Error(err, "Failed to destroy infrastructure")
			return err
		}
		log.Log.Info("Successfully destroyed infrastructure")
		return nil
	}

	return cmd
}

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

// DestroyInfra ...
// infra destruction orchestration
func (options *DestroyInfraOptions) DestroyInfra(infra *Infra) (err error) {
	log.Log.WithName(options.InfraID).Info("Destroy Infra Started")

	// if IBMCLOUD_API_KEY is not set, infra cannot be set up.
	if cloudApiKey == "" {
		return fmt.Errorf("IBMCLOUD_API_KEY not set")
	}

	resourceGroupID, err := getResourceGroupID(options.ResourceGroup)
	if err != nil {
		return err
	}

	var powerVsCloudInstanceID string

	serviceID, servicePlanID, err := getServiceInfo(powerVSService, powerVSServicePlan)
	if err != nil {
		return err
	}

	// getting the powervs cloud instance id
	if infra != nil && infra.PowerVSCloudInstanceID != "" {
		powerVsCloudInstanceID = infra.PowerVSCloudInstanceID
	} else if options.PowerVSCloudInstanceID != "" {
		_, err := validateCloudInstanceByID(options.PowerVSCloudInstanceID)
		if err != nil {
			return err
		}
		powerVsCloudInstanceID = options.PowerVSCloudInstanceID
	} else {
		cloudInstance, err := validateCloudInstanceByName(fmt.Sprintf("%s-%s", options.InfraID, cloudInstanceNameSuffix), resourceGroupID, options.PowerVSZone, serviceID, servicePlanID)
		if err != nil {
			return err
		}
		powerVsCloudInstanceID = *cloudInstance.GUID
	}

	session, err := createPowerVSSession(options.PowerVSRegion, options.PowerVSZone, options.Debug)
	if err != nil {
		return err
	}

	v1, err := createVpcService(options.VpcRegion, options.InfraID)
	if err != nil {
		return err
	}
	errL := make([]error, 0)

	err = destroyPowerVsCloudConnection(options, infra, powerVsCloudInstanceID, session)
	if err != nil {
		errL = append(errL, fmt.Errorf("error destroying powervs cloud connection: %w", err))
		log.Log.WithName(options.InfraID).Error(err, "error destroying powervs cloud connection")
	}

	err = destroyVpcSubnet(options, infra, resourceGroupID, v1, options.InfraID)
	if err != nil {
		errL = append(errL, fmt.Errorf("error destroying vpc subnet: %w", err))
		log.Log.WithName(options.InfraID).Error(err, "error destroying vpc subnet")
	}

	err = destroyVpc(options, infra, resourceGroupID, v1, options.InfraID)
	if err != nil {
		errL = append(errL, fmt.Errorf("error destroying vpc: %w", err))
		log.Log.WithName(options.InfraID).Error(err, "error destroying vpc")
	}

	err = destroyPowerVsDhcpServer(infra, powerVsCloudInstanceID, session, options.InfraID)
	if err != nil {
		errL = append(errL, fmt.Errorf("error destroying powervs dhcp server: %w", err))
		log.Log.WithName(options.InfraID).Error(err, "error destroying powervs dhcp server")
	}

	err = destroyPowerVsCloudInstance(powerVsCloudInstanceID, options.InfraID)
	if err != nil {
		errL = append(errL, fmt.Errorf("error destroying powervs cloud instance: %w", err))
		log.Log.WithName(options.InfraID).Error(err, "error destroying powervs cloud instance")
	}

	log.Log.WithName(options.InfraID).Info("Destroy Infra Completed")

	if err := errors.NewAggregate(errL); err != nil {
		return fmt.Errorf("error in destroying infra: %w", err)
	}

	return
}

// destroyPowerVsCloudInstance ...
// destroying powervs cloud instance
func destroyPowerVsCloudInstance(cloudInstanceID string, infraID string) (err error) {
	rcv2, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{Authenticator: getIAMAuth()})
	if err != nil {
		return
	}

	for retry := 0; retry < 5; retry++ {
		log.Log.WithName(infraID).Info("Deleting PowerVS cloud instance", "id", cloudInstanceID)
		_, err = rcv2.DeleteResourceInstance(&resourcecontrollerv2.DeleteResourceInstanceOptions{ID: &cloudInstanceID})

		if err != nil {
			log.Log.Error(err, "error in deleting powervs cloud instance")
			continue
		}

		f := func() (cond bool, err error) {
			resourceInst, resp, err := rcv2.GetResourceInstance(&resourcecontrollerv2.GetResourceInstanceOptions{ID: &cloudInstanceID})
			if err != nil {
				log.Log.WithName(infraID).Error(err, "error in querying deleted cloud instance", "resp", resp.String())
				return
			}

			if resourceInst != nil {
				if *resourceInst.State == powerVSCloudInstanceRemovedState {
					cond = true
					return
				}

				log.Log.WithName(infraID).Info("Waiting for PowerVS cloud instance deletion", "status", *resourceInst.State, "lastOp", resourceInst.LastOperation)
			}

			return
		}

		err = wait.PollImmediate(pollingInterval, cloudInstanceDeletionTimeout, f)
		if err == nil {
			break
		}
		log.Log.WithName(infraID).Info("Retrying cloud instance deletion ...")
	}
	return
}

// monitorPowerVsJob ...
// monitoring the submitted deletion job
func monitorPowerVsJob(id string, client *instance.IBMPIJobClient, infraID string, timeout time.Duration) (err error) {

	f := func() (cond bool, err error) {
		job, err := client.Get(id)
		log.Log.WithName(infraID).Info("Waiting for PowerVS job to complete", "id", id, "status", job.Status.State, "operation_action", *job.Operation.Action, "operation_target", *job.Operation.Target)
		if err != nil {
			return
		}
		if *job.Status.State == powerVSJobCompletedState || *job.Status.State == powerVSJobFailedState {
			cond = true
			return
		}
		return
	}

	return wait.PollImmediate(pollingInterval, timeout, f)
}

// destroyPowerVsCloudConnection ...
// destroying powervs cloud connection
func destroyPowerVsCloudConnection(options *DestroyInfraOptions, infra *Infra, cloudInstanceID string, session *ibmpisession.IBMPISession) (err error) {
	client := instance.NewIBMPICloudConnectionClient(context.Background(), session, cloudInstanceID)
	jobClient := instance.NewIBMPIJobClient(context.Background(), session, cloudInstanceID)

	if infra != nil && infra.PowerVSCloudConnectionID != "" {
		return deletePowerVsCloudConnection(options, infra.PowerVSCloudConnectionID, client, jobClient)
	}

	cloudConnL, err := client.GetAll()
	if err != nil || cloudConnL == nil {
		return
	}

	if len(cloudConnL.CloudConnections) < 1 {
		return fmt.Errorf("no cloud connection available to delete in powervs")
	}
	var cloudConnName string
	if options.Vpc != "" {
		cloudConnName = options.PowerVSCloudConnection
	} else {
		cloudConnName = fmt.Sprintf("%s-%s", options.InfraID, cloudConnNameSuffix)
	}

	for _, cloudConn := range cloudConnL.CloudConnections {
		if *cloudConn.Name == cloudConnName {
			return deletePowerVsCloudConnection(options, *cloudConn.CloudConnectionID, client, jobClient)
		}
	}
	return
}

func deletePowerVsCloudConnection(options *DestroyInfraOptions, id string, client *instance.IBMPICloudConnectionClient, jobClient *instance.IBMPIJobClient) (err error) {
	log.Log.WithName(options.InfraID).Info("Deleting cloud connection", "id", id)
	deleteJob, err := client.Delete(id)
	if err != nil {
		return err
	}
	if deleteJob == nil {
		return fmt.Errorf("error while deleting cloud connection, delete job returned is nil")
	}
	return monitorPowerVsJob(*deleteJob.ID, jobClient, options.InfraID, powerVSResourceDeletionTimeout)
}

// destroyPowerVsDhcpServer ...
// destroying powervs dhcp server
func destroyPowerVsDhcpServer(infra *Infra, cloudInstanceID string, session *ibmpisession.IBMPISession, infraID string) (err error) {
	client := instance.NewIBMPIDhcpClient(context.Background(), session, cloudInstanceID)
	if infra != nil && infra.PowerVSDhcpID != "" {
		log.Log.WithName(infraID).Info("Deleting DHCP server", "id", infra.PowerVSDhcpID)
		return client.Delete(infra.PowerVSDhcpID)
	}

	dhcpServers, err := client.GetAll()
	if err != nil {
		return
	}

	if dhcpServers == nil || len(dhcpServers) < 1 {
		return fmt.Errorf("no dhcp servers available to delete in powervs")
	}

	dhcpID := ""
	for _, dhcp := range dhcpServers {
		log.Log.WithName(infraID).Info("Deleting DHCP server", "id", *dhcp.ID)
		dhcpID = *dhcp.ID
		err = client.Delete(*dhcp.ID)
		if err != nil {
			return
		}
		break
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

		log.Log.WithName(infraID).Info("Waiting for DhcpServer to destroy", "id", *dhcpInstance.PvmInstanceID, "status", *dhcpInstance.Status)
		if *dhcpInstance.Status == dhcpInstanceShutOffState || *dhcpInstance.Status == dhcpServiceErrorState {
			cond = true
			return
		}

		return
	}

	err = wait.PollImmediate(pollingInterval, dhcpServerDeletionTimeout, f)

	return
}

// destroyVpc ...
// destroying vpc
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

		var vpcName string
		if options.Vpc != "" {
			vpcName = options.Vpc
		} else {
			vpcName = fmt.Sprintf("%s-%s", options.InfraID, vpcNameSuffix)
		}

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

// deleteVpc ...
// deletes the vpc id passed
func deleteVpc(id string, v1 *vpcv1.VpcV1, infraID string) (err error) {
	log.Log.WithName(infraID).Info("Deleting VPC", "id", id)
	_, err = v1.DeleteVPC(&vpcv1.DeleteVPCOptions{ID: &id})
	return err
}

// destroyVpcSubnet ...
// destroying vpc subnet
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

		var vpcName string
		if options.Vpc != "" {
			vpcName = options.Vpc
		} else {
			vpcName = fmt.Sprintf("%s-%s", options.InfraID, vpcNameSuffix)
		}

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

// deleteVpcSubnet ...
// deletes the subnet id passed
func deleteVpcSubnet(id string, v1 *vpcv1.VpcV1, infraID string) (err error) {
	log.Log.WithName(infraID).Info("Deleting VPC subnet", "subnetId", id)
	_, err = v1.DeleteSubnet(&vpcv1.DeleteSubnetOptions{ID: &id})
	return err
}
