package powervs

import (
	"context"
	"fmt"
	"github.com/IBM-Cloud/power-go-client/clients/instance"
	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"
	"github.com/IBM/vpc-go-sdk/vpcv1"
)

var (
	cloudConNotFound              = func(cloudConnName string) error { return fmt.Errorf("%s cloud connection not found", cloudConnName) }
	cloudInstanceNotFound         = func(cloudInstance string) error { return fmt.Errorf("%s cloud instance not found", cloudInstance) }
	cloudInstanceNotInActiveState = func(state string) error {
		return fmt.Errorf("provided cloud instance id is not in active state, current state: %s", state)
	}
)

// validateCloudInstanceByID
// validates cloud instance's existence by id
func validateCloudInstanceByID(ctx context.Context, cloudInstanceID string) (*resourcecontrollerv2.ResourceInstance, error) {
	rcv2, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{Authenticator: getIAMAuth()})
	if err != nil {
		return nil, err
	}

	resourceInstance, _, err := rcv2.GetResourceInstanceWithContext(ctx, &resourcecontrollerv2.GetResourceInstanceOptions{ID: &cloudInstanceID})
	if err != nil {
		return nil, err
	}

	if resourceInstance == nil {
		return nil, cloudInstanceNotFound(cloudInstanceID)
	}

	if *resourceInstance.State != "active" {
		return resourceInstance, cloudInstanceNotInActiveState(*resourceInstance.State)
	}

	return resourceInstance, nil
}

// validateCloudInstanceByName
// validates cloud instance's existence by name
func validateCloudInstanceByName(ctx context.Context, cloudInstance string, resourceGroupID string, powerVsZone string, serviceID string, servicePlanID string) (*resourcecontrollerv2.ResourceInstance, error) {
	rcv2, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{Authenticator: getIAMAuth()})
	if err != nil {
		return nil, err
	}
	var resourceInstance *resourcecontrollerv2.ResourceInstance

	f := func(start string) (bool, string, error) {
		listResourceInstOpt := resourcecontrollerv2.ListResourceInstancesOptions{
			Name:            &cloudInstance,
			ResourceGroupID: &resourceGroupID,
			ResourceID:      &serviceID,
			ResourcePlanID:  &servicePlanID}

		if start != "" {
			listResourceInstOpt.Start = &start
		}

		resourceInstanceL, _, err := rcv2.ListResourceInstancesWithContext(ctx, &listResourceInstOpt)

		if err != nil {
			return false, "", err
		}

		for _, resourceIns := range resourceInstanceL.Resources {
			if *resourceIns.Name == cloudInstance && *resourceIns.RegionID == powerVsZone {
				resourceInstance = &resourceIns
				return true, "", nil
			}
		}

		// For paging over next set of resources getting the start token
		if resourceInstanceL.NextURL != nil && *resourceInstanceL.NextURL != "" {
			return false, *resourceInstanceL.NextURL, nil
		}

		return true, "", nil
	}

	if err = pagingHelper(f); err != nil {
		return nil, err
	}

	if resourceInstance == nil {
		return nil, cloudInstanceNotFound(cloudInstance)
	}

	if *resourceInstance.State != "active" {
		return resourceInstance, cloudInstanceNotInActiveState(*resourceInstance.State)
	}
	return resourceInstance, nil
}

// validateVpc
// validates vpc's existence by name and validate its default security group's inbound rules to allow http & https
func validateVpc(ctx context.Context, vpcName string, resourceGroupID string, v1 *vpcv1.VpcV1) (*vpcv1.VPC, error) {
	var vpc *vpcv1.VPC

	f := func(start string) (bool, string, error) {
		vpcListOpt := vpcv1.ListVpcsOptions{ResourceGroupID: &resourceGroupID}
		if start != "" {
			vpcListOpt.Start = &start
		}
		vpcList, _, err := v1.ListVpcsWithContext(ctx, &vpcListOpt)
		if err != nil {
			return false, "", err
		}
		for _, v := range vpcList.Vpcs {
			if *v.Name == vpcName {
				vpc = &v
				return true, "", nil
			}
		}

		if vpcList.Next != nil && *vpcList.Next.Href != "" {
			return false, *vpcList.Next.Href, nil
		}

		return true, "", nil
	}
	if err := pagingHelper(f); err != nil {
		return nil, err
	}

	if vpc == nil {
		return nil, fmt.Errorf("%s vpc not found", vpcName)
	}

	vpcSg, _, err := v1.GetSecurityGroupWithContext(ctx, &vpcv1.GetSecurityGroupOptions{ID: vpc.DefaultSecurityGroup.ID})
	if err != nil {
		return nil, fmt.Errorf("error retrieving security group of vpc %w", err)
	}

	var httpOk, httpsOk bool
	for _, ruleInf := range vpcSg.Rules {
		switch rule := ruleInf.(type) {
		case *vpcv1.SecurityGroupRuleSecurityGroupRuleProtocolTcpudp:
			if *rule.PortMin <= 80 && *rule.PortMax >= 80 {
				httpOk = true
			}
			if *rule.PortMin <= 443 && *rule.PortMax >= 443 {
				httpsOk = true
			}
		}
	}

	if !httpOk || !httpsOk {
		return nil, fmt.Errorf("vpc security group does not have the required inbound rules, ports 80 and 443 should be allowed")
	}

	return vpc, nil
}

// listAndGetCloudConnection
// helper func will list the cloud connection and return the matched cloud connection id and total cloud connection count
func listAndGetCloudConnection(cloudConnName string, client *instance.IBMPICloudConnectionClient) (int, string, error) {
	cloudConnL, err := client.GetAll()
	if err != nil {
		return 0, "", err
	}

	if cloudConnL == nil {
		return 0, "", fmt.Errorf("cloud connection list returned is nil")
	}

	var cloudConnID string
	cloudConnectionCount := len(cloudConnL.CloudConnections)
	for _, cc := range cloudConnL.CloudConnections {
		if cc != nil && *cc.Name == cloudConnName {
			cloudConnID = *cc.CloudConnectionID
			return cloudConnectionCount, cloudConnID, nil
		}
	}

	return 0, "", cloudConNotFound(cloudConnName)
}

// validateCloudConnectionByName
// validates cloud connection's existence by name
func validateCloudConnectionByName(name string, client *instance.IBMPICloudConnectionClient) (string, error) {
	_, cloudConnID, err := listAndGetCloudConnection(name, client)
	return cloudConnID, err
}

// validateCloudConnectionInPowerVSZone
// while creating a new cloud connection this func validates whether to create a new cloud connection
// with respect to powervs zone's existing cloud connections
func validateCloudConnectionInPowerVSZone(name string, client *instance.IBMPICloudConnectionClient) (string, error) {
	cloudConnCount, cloudConnID, err := listAndGetCloudConnection(name, client)
	if err != nil && err.Error() != cloudConNotFound(name).Error() {
		return "", fmt.Errorf("failed to list cloud connections %w", err)
	}

	// explicitly setting err to nil since main objective here is to validate the number of cloud connections
	err = nil

	if cloudConnCount > 2 || (cloudConnCount == 2 && cloudConnID == "") {
		err = fmt.Errorf("cannot create new cloud connection in powervs zone. only 2 cloud connections allowed")
	}

	return cloudConnID, err
}
