package collectprofiles

import (
	"fmt"
	"math/big"

	component "github.com/openshift/hypershift/support/controlplane-component"

	batchv1 "k8s.io/api/batch/v1"
)

func adaptCronJob(cpContext component.WorkloadContext, cronJob *batchv1.CronJob) error {
	cronJob.Spec.Schedule = generateModularDailyCronSchedule([]byte(cronJob.Namespace))
	return nil
}

// generateModularDailyCronSchedule returns a daily crontab schedule
// where, given a is input's integer representation, the minute is a % 60
// and hour is a % 24.
func generateModularDailyCronSchedule(input []byte) string {
	a := big.NewInt(0).SetBytes(input)
	var hi, mi big.Int
	m := mi.Mod(a, big.NewInt(60))
	h := hi.Mod(a, big.NewInt(24))
	return fmt.Sprintf("%d %d * * *", m.Int64(), h.Int64())
}
