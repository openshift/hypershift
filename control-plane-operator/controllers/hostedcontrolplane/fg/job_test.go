package fg

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	batchv1 "k8s.io/api/batch/v1"
)

func TestReconcileFeatureGateGenerationJob(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		setDefaultSecurityContext bool
	}{
		{
			name:                      "no default security context",
			setDefaultSecurityContext: false,
		},
		{
			name:                      "with default security context",
			setDefaultSecurityContext: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := &batchv1.Job{}
			job.Name = "featuregate-generator"
			job.Namespace = "test-namespace"

			hcp := &hyperv1.HostedControlPlane{}

			err := ReconcileFeatureGateGenerationJob(context.Background(), job, hcp, "4.19.0", "example.org/config-image", "example.org/cpo-image", tc.setDefaultSecurityContext)
			g := NewGomegaWithT(t)
			g.Expect(err).ToNot(HaveOccurred())

			jobYAML, err := util.SerializeResource(job, hyperapi.Scheme)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			testutil.CompareWithFixture(t, jobYAML)
		})
	}
}
