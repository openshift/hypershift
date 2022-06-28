package powervs

import (
	"fmt"
	"github.com/IBM-Cloud/power-go-client/clients/instance"
	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"
	"github.com/IBM/vpc-go-sdk/vpcv1"
)

var (
	cloudConNotFound      = func(cloudConnName string) error { return fmt.Errorf("%s cloud connection not found", cloudConnName) }
	cloudInstanceNotFound = func(cloudInstance string) error { return fmt.Errorf("%s cloud instance not found", cloudInstance) }
)

// validateCloudInstanceByID
// validates cloud instance's existence by id
func validateCloudInstanceByID(cloudInstanceID string) (resourceInstance *resourcecontrollerv2.ResourceInstance, err error) {
	rcv2, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{Authenticator: getIAMAuth()})
	if err != nil {
		return
	}

	resourceInstance, _, err = rcv2.GetResourceInstance(&resourcecontrollerv2.GetResourceInstanceOptions{ID: &cloudInstanceID})
	if err != nil {
		return
	}

	if resourceInstance == nil {
		err = fmt.Errorf("%s cloud instance not found", cloudInstanceID)
		return
	}

	if *resourceInstance.State != "active" {
		err = fmt.Errorf("provided cloud instance id is not in active state, current state: %s", *resourceInstance.State)
		return
	}

	return
}

// validateCloudInstanceByName
// validates cloud instance's existence by name
func validateCloudInstanceByName(cloudInstance string, resourceGroupID string, powerVsZone string, serviceID string, servicePlanID string) (resourceInstance *resourcecontrollerv2.ResourceInstance, err error) {
	rcv2, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{Authenticator: getIAMAuth()})
	if err != nil {
		return
	}

	f := func(start string) (isDone bool, nextUrl string, err error) {
		listResourceInstOpt := resourcecontrollerv2.ListResourceInstancesOptions{
			Name:            &cloudInstance,
			ResourceGroupID: &resourceGroupID,
			ResourceID:      &serviceID,
			ResourcePlanID:  &servicePlanID}

		if start != "" {
			listResourceInstOpt.Start = &start
		}

		resourceInstanceL, _, err := rcv2.ListResourceInstances(&listResourceInstOpt)

		if err != nil {
			return
		}

		for _, resourceIns := range resourceInstanceL.Resources {
			if *resourceIns.Name == cloudInstance && *resourceIns.RegionID == powerVsZone {
				resourceInstance = &resourceIns
				isDone = true
				return
			}
		}

		// For paging over next set of resources getting the start token
		if resourceInstanceL.NextURL != nil && *resourceInstanceL.NextURL != "" {
			nextUrl = *resourceInstanceL.NextURL
			return
		}

		isDone = true
		return
	}

	err = pagingHelper(f)
	if err != nil {
		return
	}

	if resourceInstance == nil {
		err = cloudInstanceNotFound(cloudInstance)
		return
	}

	if *resourceInstance.State != "active" {
		err = fmt.Errorf("provided cloud instance id is not in active state, current state: %s", *resourceInstance.State)
		return
	}
	return
}

// validateVpc
// validates vpc's existence by name and validate its default security group's inbound rules to allow http & https
func validateVpc(vpcName string, resourceGroupID string, v1 *vpcv1.VpcV1) (vpc *vpcv1.VPC, err error) {
	f := func(start string) (isDone bool, nextUrl string, err error) {
		vpcListOpt := vpcv1.ListVpcsOptions{ResourceGroupID: &resourceGroupID}
		if start != "" {
			vpcListOpt.Start = &start
		}
		vpcList, _, err := v1.ListVpcs(&vpcListOpt)
		if err != nil {
			return
		}
		for _, v := range vpcList.Vpcs {
			if *v.Name == vpcName {
				vpc = &v
				isDone = true
				return
			}
		}

		if vpcList.Next != nil && *vpcList.Next.Href != "" {
			nextUrl = *vpcList.Next.Href
			return
		}

		isDone = true
		return
	}
	err = pagingHelper(f)
	if err != nil {
		return
	}

	if vpc == nil {
		err = fmt.Errorf("%s vpc not found", vpcName)
		return
	}

	vpcSg, _, err := v1.GetSecurityGroup(&vpcv1.GetSecurityGroupOptions{ID: vpc.DefaultSecurityGroup.ID})
	if err != nil {
		err = fmt.Errorf("error retrieving security group of vpc %w", err)
		return
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
		err = fmt.Errorf("vpc security group does not have the required inbound rules, ports 80 and 443 should be allowed")
	}

	return
}

// listAndGetCloudConnection
// helper func will list the cloud connection and return the matched cloud connection id and total cloud connection count
func listAndGetCloudConnection(cloudConnName string, client *instance.IBMPICloudConnectionClient) (cloudConnectionCount int, cloudConnID string, err error) {
	cloudConnL, err := client.GetAll()
	if err != nil {
		return
	}

	if cloudConnL == nil {
		err = fmt.Errorf("cloud connection list returned is nil")
		return
	}

	cloudConnectionCount = len(cloudConnL.CloudConnections)
	for _, cc := range cloudConnL.CloudConnections {
		if cc != nil && *cc.Name == cloudConnName {
			cloudConnID = *cc.CloudConnectionID
			return
		}
	}

	err = cloudConNotFound(cloudConnName)
	return
}

// validateCloudConnectionByName
// validates cloud connection's existence by name
func validateCloudConnectionByName(name string, client *instance.IBMPICloudConnectionClient) (cloudConnID string, err error) {
	_, cloudConnID, err = listAndGetCloudConnection(name, client)
	return
}

// validateCloudConnectionInPowerVSZone
// while creating a new cloud connection this func validates whether to create a new cloud connection
// with respect to powervs zone's existing cloud connections
func validateCloudConnectionInPowerVSZone(name string, client *instance.IBMPICloudConnectionClient) (cloudConnID string, err error) {
	cloudConnCount, cloudConnID, err := listAndGetCloudConnection(name, client)
	if err != nil && err.Error() != cloudConNotFound(name).Error() {
		err = fmt.Errorf("failed to list cloud connections %w", err)
		return
	}

	// explicitly setting err to nil since main objective here is to validate the number of cloud connections
	err = nil

	if cloudConnCount > 2 || (cloudConnCount == 2 && cloudConnID == "") {
		err = fmt.Errorf("cannot create new cloud connection in powervs zone. only 2 cloud connections allowed")
	}

	return
}
