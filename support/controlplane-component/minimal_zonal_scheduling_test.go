package controlplanecomponent

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

type minimalTestComponent struct {
	requestServing bool
}

func (c *minimalTestComponent) IsRequestServing() bool         { return c.requestServing }
func (c *minimalTestComponent) MultiZoneSpread() bool          { return true }
func (c *minimalTestComponent) NeedsManagementKASAccess() bool { return false }

func newMinimalWorkload(name string, requestServing bool) *controlPlaneWorkload[*appsv1.Deployment] {
	return &controlPlaneWorkload[*appsv1.Deployment]{
		name:             name,
		workloadProvider: &deploymentProvider{},
		ComponentOptions: &minimalTestComponent{requestServing: requestServing},
	}
}

func minimalHCP(placement hyperv1.NonZonalPlacementPolicy) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{}
	hcp.Spec.ControllerAvailabilityPolicy = hyperv1.HighlyAvailable
	hcp.Spec.ControlPlaneAvailabilityZoneScheduling = hyperv1.ControlPlaneAvailabilityZoneScheduling{
		Policy:            hyperv1.ControlPlaneAvailabilityZoneSchedulingMinimal,
		NonZonalPlacement: placement,
	}
	return hcp
}

func TestZoneSpreadCritical(t *testing.T) {
	tests := []struct {
		name           string
		component      string
		requestServing bool
		want           bool
	}{
		{name: "When the component is etcd, it should be zone-critical", component: "etcd", want: true},
		{name: "When the component is request-serving, it should be zone-critical", component: "kube-apiserver", requestServing: true, want: true},
		{name: "When the component is openshift-apiserver, it should be zone-critical", component: "openshift-apiserver", want: true},
		{name: "When the component is openshift-oauth-apiserver, it should be zone-critical", component: "openshift-oauth-apiserver", want: true},
		{name: "When the component is a request-serving router, it should be zone-critical", component: "router", requestServing: true, want: true},
		{name: "When the component is kube-controller-manager, it should be float", component: "kube-controller-manager", want: false},
		{name: "When the component is kube-scheduler, it should be float", component: "kube-scheduler", want: false},
		{name: "When the component is packageserver, it should be float", component: "packageserver", want: false},
		{name: "When the component is cluster-version-operator, it should be float", component: "cluster-version-operator", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(newMinimalWorkload(test.component, test.requestServing).zoneSpreadCritical()).To(Equal(test.want))
		})
	}
}

func TestDefaultReplicasMinimalPolicy(t *testing.T) {
	tests := []struct {
		name           string
		component      string
		availability   hyperv1.AvailabilityPolicy
		minimal        bool
		requestServing bool
		want           int32
	}{
		{name: "When Minimal and the component is etcd, it should keep three replicas", component: "etcd", availability: hyperv1.HighlyAvailable, minimal: true, want: 3},
		{name: "When legacy and the component is etcd, it should keep three replicas", component: "etcd", availability: hyperv1.HighlyAvailable, minimal: false, want: 3},
		{name: "When Minimal and the component is kube-apiserver, it should run two replicas", component: "kube-apiserver", availability: hyperv1.HighlyAvailable, minimal: true, requestServing: true, want: 2},
		{name: "When legacy and the component is kube-apiserver, it should run three replicas", component: "kube-apiserver", availability: hyperv1.HighlyAvailable, minimal: false, requestServing: true, want: 3},
		{name: "When Minimal and the component is openshift-apiserver, it should run two replicas", component: "openshift-apiserver", availability: hyperv1.HighlyAvailable, minimal: true, want: 2},
		{name: "When legacy and the component is openshift-apiserver, it should run three replicas", component: "openshift-apiserver", availability: hyperv1.HighlyAvailable, minimal: false, want: 3},
		{name: "When Minimal and the component is kube-controller-manager, it should run two replicas", component: "kube-controller-manager", availability: hyperv1.HighlyAvailable, minimal: true, want: 2},
		{name: "When legacy and the component is kube-controller-manager, it should run two replicas", component: "kube-controller-manager", availability: hyperv1.HighlyAvailable, minimal: false, want: 2},
		{name: "When SingleReplica, it should run one replica regardless of policy", component: "kube-apiserver", availability: hyperv1.SingleReplica, minimal: true, requestServing: true, want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hcp := &hyperv1.HostedControlPlane{}
			hcp.Spec.ControllerAvailabilityPolicy = test.availability
			if test.minimal {
				hcp.Spec.ControlPlaneAvailabilityZoneScheduling = hyperv1.ControlPlaneAvailabilityZoneScheduling{
					Policy: hyperv1.ControlPlaneAvailabilityZoneSchedulingMinimal,
				}
			}
			g.Expect(DefaultReplicas(hcp, &minimalTestComponent{requestServing: test.requestServing}, test.component)).To(Equal(test.want))
		})
	}
}

// findZoneTSC returns the topology.kubernetes.io/zone spread constraint, if any.
func findZoneTSC(cs []corev1.TopologySpreadConstraint) *corev1.TopologySpreadConstraint {
	for i := range cs {
		if cs[i].TopologyKey == corev1.LabelTopologyZone {
			return &cs[i]
		}
	}
	return nil
}

func findHostTSC(cs []corev1.TopologySpreadConstraint) *corev1.TopologySpreadConstraint {
	for i := range cs {
		if cs[i].TopologyKey == corev1.LabelHostname {
			return &cs[i]
		}
	}
	return nil
}

func TestSetMinimalZonalSpread(t *testing.T) {
	t.Run("etcd is strict with minDomains=3 and no matchLabelKeys", func(t *testing.T) {
		g := NewGomegaWithT(t)
		w := newMinimalWorkload("etcd", false)
		pt := &corev1.PodTemplateSpec{}
		pt.Labels = map[string]string{hyperv1.ControlPlaneComponentLabel: "etcd"}
		w.setMinimalZonalSpread(pt)

		zone := findZoneTSC(pt.Spec.TopologySpreadConstraints)
		g.Expect(zone).ToNot(BeNil())
		g.Expect(zone.WhenUnsatisfiable).To(Equal(corev1.DoNotSchedule))
		g.Expect(zone.MinDomains).ToNot(BeNil())
		g.Expect(*zone.MinDomains).To(Equal(int32(3)))
		g.Expect(zone.MatchLabelKeys).To(BeEmpty())

		host := findHostTSC(pt.Spec.TopologySpreadConstraints)
		g.Expect(host).ToNot(BeNil())
		g.Expect(host.WhenUnsatisfiable).To(Equal(corev1.DoNotSchedule))
		g.Expect(host.MatchLabelKeys).To(BeEmpty())
	})

	t.Run("non-quorum zone-critical pair uses matchLabelKeys and no minDomains", func(t *testing.T) {
		g := NewGomegaWithT(t)
		w := newMinimalWorkload("kube-apiserver", true)
		pt := &corev1.PodTemplateSpec{}
		pt.Labels = map[string]string{hyperv1.ControlPlaneComponentLabel: "kube-apiserver"}
		w.setMinimalZonalSpread(pt)

		zone := findZoneTSC(pt.Spec.TopologySpreadConstraints)
		g.Expect(zone).ToNot(BeNil())
		g.Expect(zone.WhenUnsatisfiable).To(Equal(corev1.DoNotSchedule))
		g.Expect(zone.MinDomains).To(BeNil())
		g.Expect(zone.MatchLabelKeys).To(ContainElement("pod-template-hash"))
	})

	t.Run("float uses best-effort zone spread and hard host spread", func(t *testing.T) {
		g := NewGomegaWithT(t)
		w := newMinimalWorkload("kube-controller-manager", false)
		pt := &corev1.PodTemplateSpec{}
		pt.Labels = map[string]string{hyperv1.ControlPlaneComponentLabel: "kube-controller-manager"}
		w.setMinimalZonalSpread(pt)

		zone := findZoneTSC(pt.Spec.TopologySpreadConstraints)
		g.Expect(zone).ToNot(BeNil())
		g.Expect(zone.WhenUnsatisfiable).To(Equal(corev1.ScheduleAnyway))

		host := findHostTSC(pt.Spec.TopologySpreadConstraints)
		g.Expect(host).ToNot(BeNil())
		g.Expect(host.WhenUnsatisfiable).To(Equal(corev1.DoNotSchedule))
	})
}

// nodeAffinityHasRoleRequired reports whether the required node affinity contains a
// control-plane-node-role In [value] requirement.
func nodeAffinityHasRoleRequired(na *corev1.NodeAffinity, value string) bool {
	if na == nil || na.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		return false
	}
	for _, term := range na.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
		for _, req := range term.MatchExpressions {
			if req.Key == hyperv1.ControlPlaneNodeRoleLabel && req.Operator == corev1.NodeSelectorOpIn && len(req.Values) == 1 && req.Values[0] == value {
				return true
			}
		}
	}
	return false
}

func nodeAffinityHasRolePreferred(na *corev1.NodeAffinity, value string) bool {
	if na == nil {
		return false
	}
	for _, term := range na.PreferredDuringSchedulingIgnoredDuringExecution {
		for _, req := range term.Preference.MatchExpressions {
			if req.Key == hyperv1.ControlPlaneNodeRoleLabel && len(req.Values) == 1 && req.Values[0] == value {
				return true
			}
		}
	}
	return false
}

func tolerationsHaveZonalTaint(tols []corev1.Toleration) bool {
	for _, tol := range tols {
		if tol.Key == hyperv1.ControlPlaneNodeRoleLabel && tol.Value == hyperv1.ControlPlaneNodeRoleZonal && tol.Effect == corev1.TaintEffectNoSchedule {
			return true
		}
	}
	return false
}

func TestSetMinimalZonalNodePlacement(t *testing.T) {
	t.Run("zone-critical requires zonal role and tolerates the zonal taint", func(t *testing.T) {
		g := NewGomegaWithT(t)
		w := newMinimalWorkload("kube-apiserver", true)
		pt := &corev1.PodTemplateSpec{}
		w.setMinimalZonalNodePlacement(pt, minimalHCP(hyperv1.NonZonalPlacementRequired))
		g.Expect(nodeAffinityHasRoleRequired(pt.Spec.Affinity.NodeAffinity, hyperv1.ControlPlaneNodeRoleZonal)).To(BeTrue())
		g.Expect(tolerationsHaveZonalTaint(pt.Spec.Tolerations)).To(BeTrue())
	})

	t.Run("float hard placement requires overflow role", func(t *testing.T) {
		g := NewGomegaWithT(t)
		w := newMinimalWorkload("kube-controller-manager", false)
		pt := &corev1.PodTemplateSpec{}
		w.setMinimalZonalNodePlacement(pt, minimalHCP(hyperv1.NonZonalPlacementRequired))
		g.Expect(nodeAffinityHasRoleRequired(pt.Spec.Affinity.NodeAffinity, hyperv1.ControlPlaneNodeRoleOverflow)).To(BeTrue())
	})

	t.Run("float soft placement prefers overflow role", func(t *testing.T) {
		g := NewGomegaWithT(t)
		w := newMinimalWorkload("kube-controller-manager", false)
		pt := &corev1.PodTemplateSpec{}
		w.setMinimalZonalNodePlacement(pt, minimalHCP(hyperv1.NonZonalPlacementPreferred))
		g.Expect(nodeAffinityHasRolePreferred(pt.Spec.Affinity.NodeAffinity, hyperv1.ControlPlaneNodeRoleOverflow)).To(BeTrue())
		g.Expect(pt.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution).To(BeNil())
	})
}

func TestSetColocationPerTier(t *testing.T) {
	t.Run("minimal policy scopes colocation to the scheduling tier", func(t *testing.T) {
		g := NewGomegaWithT(t)
		w := newMinimalWorkload("kube-controller-manager", false)
		pt := &corev1.PodTemplateSpec{}
		hcp := minimalHCP(hyperv1.NonZonalPlacementPreferred)
		w.setColocation(pt, hcp)
		term := pt.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0]
		g.Expect(term.PodAffinityTerm.LabelSelector.MatchLabels).To(HaveKeyWithValue(hyperv1.ControlPlaneSchedulingTierLabel, hyperv1.ControlPlaneNodeRoleOverflow))
	})

	t.Run("legacy path does not add a tier selector", func(t *testing.T) {
		g := NewGomegaWithT(t)
		w := newMinimalWorkload("kube-controller-manager", false)
		pt := &corev1.PodTemplateSpec{}
		w.setColocation(pt, &hyperv1.HostedControlPlane{})
		term := pt.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution[0]
		g.Expect(term.PodAffinityTerm.LabelSelector.MatchLabels).ToNot(HaveKey(hyperv1.ControlPlaneSchedulingTierLabel))
	})
}
