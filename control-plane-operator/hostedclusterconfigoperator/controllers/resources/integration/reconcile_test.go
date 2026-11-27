package integration

import (
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/internal/testtrace"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

func TestReconcile(t *testing.T) {
	for _, stage := range []struct {
		name  string
		stage Stage
	}{
		{name: "When every API service input is present, it should return nil instead of an empty aggregate", stage: APIServiceProjections},
		{name: "When every OAuth input is present, it should return nil instead of an empty aggregate", stage: OAuthProjections},
	} {
		t.Run(stage.name, func(t *testing.T) {
			g := NewWithT(t)
			run := newProjectionRun(projectionFixture{controlPlane: controlPlaneInputs()})

			err := Reconcile(t.Context(), run.hosted, run.controlPlane, enabledParams(), stage.stage)

			g.Expect(err).To(BeNil())
		})
	}

	t.Run("When API service projections fail, it should return one aggregate with each failure in order", func(t *testing.T) {
		g := NewWithT(t)
		apiServiceFailure, endpointsFailure, hashFailure := errors.New("apiservice failure"), errors.New("endpoints failure"), errors.New("hash failure")
		run := newProjectionRun(projectionFixture{controlPlane: controlPlaneInputs(), upsertFailures: map[string]error{
			testtrace.ObjectKey(manifests.OpenShiftAPIServerAPIService("apps")): apiServiceFailure,
			testtrace.ObjectKey(manifests.OpenShiftAPIServerClusterEndpoints()): endpointsFailure,
			testtrace.ObjectKey(manifests.KubeadminPasswordHashSecret()):        hashFailure,
		}})

		err := Reconcile(t.Context(), run.hosted, run.controlPlane, enabledParams(), APIServiceProjections)

		var aggregate utilerrors.Aggregate
		g.Expect(errors.As(err, &aggregate)).To(BeTrue())
		g.Expect(errorMessages(aggregate.Errors())).To(Equal([]string{
			"failed to reconcile openshift apiserver service: failed to reconcile openshift apiserver apiservice (apps): apiservice failure",
			"failed to reconcile openshift apiserver endpoints: endpoints failure",
			"failed to reconcile kubeadmin password hash secret: hash failure",
		}))
		for _, failure := range []error{apiServiceFailure, endpointsFailure, hashFailure} {
			g.Expect(errors.Is(err, failure)).To(BeTrue())
		}
	})

	t.Run("When OAuth projections fail, it should return one aggregate with each failure in order", func(t *testing.T) {
		g := NewWithT(t)
		browserFailure, roleFailure := errors.New("browser failure"), errors.New("role failure")
		run := newProjectionRun(projectionFixture{controlPlane: controlPlaneInputs(), upsertFailures: map[string]error{
			testtrace.ObjectKey(manifests.OAuthServerBrowserClient()): browserFailure,
			testtrace.ObjectKey(manifests.OAuthServingCertRole()):     roleFailure,
		}})

		err := Reconcile(t.Context(), run.hosted, run.controlPlane, enabledParams(), OAuthProjections)

		var aggregate utilerrors.Aggregate
		g.Expect(errors.As(err, &aggregate)).To(BeTrue())
		g.Expect(errorMessages(aggregate.Errors())).To(Equal([]string{
			"failed to reconcile oauth browser client: browser failure",
			"failed to reconcile oauth serving cert role: role failure",
		}))
	})

	t.Run("When the stage is unknown, it should return an error without touching either cluster", func(t *testing.T) {
		g := NewWithT(t)
		run := newProjectionRun(projectionFixture{controlPlane: controlPlaneInputs()})

		err := Reconcile(t.Context(), run.hosted, run.controlPlane, enabledParams(), Stage(99))

		g.Expect(err).To(MatchError("unknown integration reconciliation stage: 99"))
		g.Expect(run.trace.Operations()).To(BeEmpty())
	})
}
