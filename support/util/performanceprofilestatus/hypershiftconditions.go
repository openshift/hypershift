package performanceprofilestatus

import (
	"fmt"

	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
)

const Prefix = "performance.openshift.io"

var (
	AvailableConditionType   = fmt.Sprintf("%s/%s", Prefix, conditionsv1.ConditionAvailable)
	ProgressingConditionType = fmt.Sprintf("%s/%s", Prefix, conditionsv1.ConditionProgressing)
	UpgradeableConditionType = fmt.Sprintf("%s/%s", Prefix, conditionsv1.ConditionUpgradeable)
	DegradedConditionType    = fmt.Sprintf("%s/%s", Prefix, conditionsv1.ConditionDegraded)

	ConditionTypeList = []string{AvailableConditionType, ProgressingConditionType, UpgradeableConditionType, DegradedConditionType}
)
