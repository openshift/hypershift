package powervs

import (
	"context"
	"fmt"

	"github.com/IBM/networking-go-sdk/transitgatewayapisv1"
	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"
	"github.com/IBM/vpc-go-sdk/vpcv1"
)

var (
	cloudInstanceNotFound         = func(cloudInstance string) error { return fmt.Errorf("%s cloud instance not found", cloudInstance) }
	cloudInstanceNotInActiveState = func(state string) error {
		return fmt.Errorf("provided cloud instance id is not in active state, current state: %s", state)
	}
	transitGatewayNotFound        = func(transitGateway string) error { return fmt.Errorf("%s tranist gateway not found", transitGateway) }
	transitGatewayConnectionError = func() error {
		return fmt.Errorf("transit gateway connections are not proper, either connection itself is not there or connection might not be in attached status")
	}
	transitGatewayUnavailableError = func(transitGateway, status string) error {
		return fmt.Errorf("%s transit gateway exist but it is not in available state, current state: %s", transitGateway, status)
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

// validateTransitGatewayByName validates transit gateway by name
// If transit gateway exists, then validates vpc and powervs connections are in attached status or not
func validateTransitGatewayByName(ctx context.Context, tgapisv1 *transitgatewayapisv1.TransitGatewayApisV1, name string, validateStatus bool) (*transitgatewayapisv1.TransitGateway, error) {
	var transitGateway *transitgatewayapisv1.TransitGateway

	f := func(start string) (bool, string, error) {
		tgList, _, err := tgapisv1.ListTransitGatewaysWithContext(ctx, &transitgatewayapisv1.ListTransitGatewaysOptions{})
		if err != nil {
			return false, "", fmt.Errorf("failed to list transit gateway %w", err)
		}

		for _, tg := range tgList.TransitGateways {
			if *tg.Name == name {
				transitGateway = &tg
				return true, "", nil
			}
		}

		if tgList.Next != nil && *tgList.Next.Href != "" {
			return false, *tgList.Next.Href, nil
		}

		return true, "", nil
	}

	if err := pagingHelper(f); err != nil {
		return nil, err
	}

	if transitGateway == nil {
		return nil, transitGatewayNotFound(name)
	}

	if !validateStatus {
		return transitGateway, nil
	}

	if *transitGateway.Status != "available" {
		return nil, transitGatewayUnavailableError(name, *transitGateway.Status)
	}

	tgConns, _, err := tgapisv1.ListTransitGatewayConnectionsWithContext(ctx, &transitgatewayapisv1.ListTransitGatewayConnectionsOptions{
		TransitGatewayID: transitGateway.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("error listing transit gateway connections: %w", err)
	}

	var isVPCConnOk, isPowerVSConnOk bool
	for _, conn := range tgConns.Connections {
		if *conn.NetworkType == "vpc" && *conn.Status == "attached" {
			isVPCConnOk = true
		}
		if *conn.NetworkType == "power_virtual_server" && *conn.Status == "attached" {
			isPowerVSConnOk = true
		}
	}

	if !isVPCConnOk || !isPowerVSConnOk {
		return nil, transitGatewayConnectionError()
	}

	return transitGateway, nil
}
