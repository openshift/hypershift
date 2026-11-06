package gmpmonitoring_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperapi "github.com/openshift/hypershift/support/api"
	gmp "github.com/openshift/hypershift/support/gmpmonitoring"
	"github.com/openshift/hypershift/support/rhobsmonitoring"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"

	"sigs.k8s.io/yaml"

	"github.com/go-openapi/spec"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
	"github.com/prometheus/common/model"
)

func monitoringFixtures() (*gmp.PodMonitoring, *gmp.Rules) {
	metadata := []string{"pod", "container"}
	running, count, lastError := true, int32(1), "connection refused"
	secret := func(name, key string) *gmp.SecretSelector {
		return &gmp.SecretSelector{Secret: &gmp.SecretKeySelector{Name: name, Key: key}}
	}
	timestamp := metav1.NewTime(time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC))
	status := gmp.MonitoringStatus{ObservedGeneration: 2, Conditions: []gmp.MonitoringCondition{{Type: gmp.ConfigurationCreateSuccess, Status: corev1.ConditionTrue, Reason: "ValidConfig", LastUpdateTime: timestamp, LastTransitionTime: timestamp}}}
	monitor := &gmp.PodMonitoring{
		TypeMeta:   metav1.TypeMeta{APIVersion: gmp.SchemeGroupVersion.String(), Kind: "PodMonitoring"},
		ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: "clusters-gmp", Labels: map[string]string{"component": "etcd"}},
		Spec: gmp.PodMonitoringSpec{
			Selector:      metav1.LabelSelector{MatchLabels: map[string]string{"app": "etcd"}, MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "role", Operator: metav1.LabelSelectorOpIn, Values: []string{"control-plane"}}}},
			FilterRunning: &running,
			Limits:        &gmp.ScrapeLimits{Samples: 10000, Labels: 100, LabelNameLength: 100, LabelValueLength: 1000},
			TargetLabels:  gmp.TargetLabels{Metadata: &metadata, FromPod: []gmp.LabelMapping{{From: "app", To: "component"}}},
			Endpoints: []gmp.ScrapeEndpoint{{
				Port: intstr.FromString("metrics"), Interval: "30s", Timeout: "10s", Scheme: "https", Path: "/metrics", Params: map[string][]string{"format": {"prometheus"}},
				MetricRelabeling: []gmp.RelabelingRule{{Action: "keep", SourceLabels: []string{"__name__"}, Regex: "etcd_.*"}},
				HTTPClientConfig: gmp.HTTPClientConfig{TLS: &gmp.TLS{ServerName: "etcd-client", MinVersion: "TLS12", CA: secret("gmp-etcd-ca", "ca.crt"), Cert: secret("etcd-metrics-client-tls", "etcd-client.crt"), Key: secret("etcd-metrics-client-tls", "etcd-client.key")}},
			}},
		},
		Status: gmp.PodMonitoringStatus{MonitoringStatus: status, EndpointStatuses: []gmp.ScrapeEndpointStatus{{Name: "metrics", LastUpdateTime: timestamp, ActiveTargets: 3, UnhealthyTargets: 1, CollectorsFraction: "1", SampleGroups: []gmp.SampleGroup{{Count: &count, SampleTargets: []gmp.SampleTarget{{Labels: model.LabelSet{"pod": "etcd-0"}, LastError: &lastError, LastScrapeDurationSeconds: "0.1", Health: "down"}}}}}}},
	}
	rules := &gmp.Rules{
		TypeMeta:   metav1.TypeMeta{APIVersion: gmp.SchemeGroupVersion.String(), Kind: "Rules"},
		ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: monitor.Namespace},
		Spec: gmp.RulesSpec{Groups: []gmp.RuleGroup{{Name: "etcd", Interval: "30s", Rules: []gmp.Rule{
			{Record: "etcd:database_size_bytes:sum", Expr: "sum(etcd_mvcc_db_total_size_in_bytes)", Labels: map[string]string{"component": "etcd"}},
			{Alert: "EtcdDatabaseLarge", Expr: "etcd_mvcc_db_total_size_in_bytes > 1e9", For: "5m", Labels: map[string]string{"severity": "warning"}, Annotations: map[string]string{"summary": "Database size is high"}},
		}}}},
		Status: gmp.RulesStatus{MonitoringStatus: status},
	}
	return monitor, rules
}

func TestAddToScheme(t *testing.T) {
	g := NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(gmp.AddToScheme(scheme)).To(Succeed())
	for _, kind := range []string{"PodMonitoring", "PodMonitoringList", "Rules", "RulesList"} {
		t.Run("When "+kind+" is registered, it should construct a Kubernetes object", func(t *testing.T) {
			g := NewWithT(t)
			obj, err := scheme.New(gmp.SchemeGroupVersion.WithKind(kind))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(obj.DeepCopyObject()).ToNot(BeNil())
		})
	}
	g.Expect(scheme.Recognizes(gmp.SchemeGroupVersion.WithKind("ClusterPodMonitoring"))).To(BeFalse())
	g.Expect(scheme.Recognizes(gmp.SchemeGroupVersion.WithKind("ClusterRules"))).To(BeFalse())
}

func TestDeepCopy(t *testing.T) {
	g := NewWithT(t)
	monitor, rules := monitoringFixtures()
	originalMonitor, originalRules := monitor.DeepCopy(), rules.DeepCopy()
	copy := monitor.DeepCopy()
	copy.Labels["component"] = "changed"
	copy.Spec.Selector.MatchLabels["app"] = "changed"
	copy.Spec.Selector.MatchExpressions[0].Values[0] = "changed"
	*copy.Spec.FilterRunning = false
	copy.Spec.Limits.Samples = 1
	(*copy.Spec.TargetLabels.Metadata)[0] = "node"
	copy.Spec.TargetLabels.FromPod[0].From = "changed"
	copy.Spec.Endpoints[0].Params["format"][0] = "changed"
	copy.Spec.Endpoints[0].TLS.CA.Secret.Name = "changed"
	copy.Spec.Endpoints[0].TLS.Cert.Secret.Key = "changed"
	copy.Spec.Endpoints[0].TLS.Key.Secret.Key = "changed"
	copy.Spec.Endpoints[0].MetricRelabeling[0].SourceLabels[0] = "changed"
	copy.Status.Conditions[0].Reason = "changed"
	group := &copy.Status.EndpointStatuses[0].SampleGroups[0]
	*group.Count = 99
	group.SampleTargets[0].Labels["pod"] = "changed"
	*group.SampleTargets[0].LastError = "changed"
	g.Expect(monitor).To(Equal(originalMonitor))
	copiedRules := rules.DeepCopy()
	copiedRules.Spec.Groups[0].Rules[0].Labels["component"] = "changed"
	copiedRules.Spec.Groups[0].Rules[1].Annotations["summary"] = "changed"
	copiedRules.Status.Conditions[0].Reason = "changed"
	g.Expect(rules).To(Equal(originalRules))
	monitorList := &gmp.PodMonitoringList{Items: []gmp.PodMonitoring{*monitor}}
	monitorList.DeepCopy().Items[0].Spec.Endpoints[0].TLS.CA.Secret.Name = "changed"
	g.Expect(monitorList.Items[0]).To(Equal(*originalMonitor))
	rulesList := &gmp.RulesList{Items: []gmp.Rules{*rules}}
	rulesList.DeepCopy().Items[0].Spec.Groups[0].Rules[0].Labels["component"] = "changed"
	g.Expect(rulesList.Items[0]).To(Equal(*originalRules))
}

func TestCompatibleYAMLEncode(t *testing.T) {
	monitor, rules := monitoringFixtures()
	objects := []runtime.Object{monitor, rules,
		&gmp.PodMonitoringList{TypeMeta: metav1.TypeMeta{APIVersion: gmp.SchemeGroupVersion.String(), Kind: "PodMonitoringList"}, Items: []gmp.PodMonitoring{*monitor}},
		&gmp.RulesList{TypeMeta: metav1.TypeMeta{APIVersion: gmp.SchemeGroupVersion.String(), Kind: "RulesList"}, Items: []gmp.Rules{*rules}},
	}
	for _, obj := range objects {
		t.Run("When "+obj.GetObjectKind().GroupVersionKind().Kind+" is serialized, it should round trip specs and status", func(t *testing.T) {
			g := NewWithT(t)
			for _, serializer := range []*runtimeSerializer{{encode: func(obj runtime.Object) ([]byte, error) {
				return hyperapi.CompatibleYAMLEncode(obj, hyperapi.YamlSerializer)
			}, decode: hyperapi.YamlSerializer.Decode},
				{encode: func(obj runtime.Object) ([]byte, error) {
					var buf bytes.Buffer
					err := hyperapi.JsonSerializer.Encode(obj, &buf)
					return buf.Bytes(), err
				}, decode: hyperapi.JsonSerializer.Decode}} {
				encoded, err := serializer.encode(obj.DeepCopyObject())
				g.Expect(err).ToNot(HaveOccurred())
				decoded, gvk, err := serializer.decode(encoded, nil, nil)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(gvk.GroupVersion()).To(Equal(gmp.SchemeGroupVersion))
				g.Expect(apiequality.Semantic.DeepEqual(obj, decoded)).To(BeTrue(), "round trip changed %T", obj)
				decodedAll, _, err := hyperapi.AllMonitoringYamlSerializer.Decode(encoded, nil, nil)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(apiequality.Semantic.DeepEqual(obj, decodedAll)).To(BeTrue(), "all-monitoring round trip changed %T", obj)
			}
		})
	}
}

type runtimeSerializer struct {
	encode func(runtime.Object) ([]byte, error)
	decode func([]byte, *schema.GroupVersionKind, runtime.Object) (runtime.Object, *schema.GroupVersionKind, error)
}

func TestValidateSchema(t *testing.T) {
	monitor, rules := monitoringFixtures()
	for _, tc := range []struct {
		name, fixture string
		object        runtime.Object
		invalid       func(map[string]interface{})
	}{
		{name: "When a typed PodMonitoring is emitted, it should conform to the upstream schema", fixture: "podmonitorings", object: monitor, invalid: func(obj map[string]interface{}) { obj["spec"].(map[string]interface{})["endpoints"] = []interface{}{} }},
		{name: "When typed Rules are emitted, it should conform to the upstream schema", fixture: "rules", object: rules, invalid: func(obj map[string]interface{}) { delete(obj["spec"].(map[string]interface{}), "groups") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			fixture, err := os.ReadFile(filepath.Join("testdata", tc.fixture+".crd.yaml"))
			g.Expect(err).ToNot(HaveOccurred())
			var crd struct {
				Spec struct {
					Versions []struct {
						Name   string
						Schema struct{ OpenAPIV3Schema spec.Schema }
					}
				}
			}
			g.Expect(yaml.Unmarshal(fixture, &crd)).To(Succeed())
			var schema *spec.Schema
			for i := range crd.Spec.Versions {
				if crd.Spec.Versions[i].Name == "v1" {
					schema = &crd.Spec.Versions[i].Schema.OpenAPIV3Schema
				}
			}
			g.Expect(schema).ToNot(BeNil())
			encoded, err := json.Marshal(tc.object)
			g.Expect(err).ToNot(HaveOccurred())
			var obj map[string]interface{}
			g.Expect(json.Unmarshal(encoded, &obj)).To(Succeed())
			g.Expect(validate.AgainstSchema(schema, obj, strfmt.Default)).To(Succeed())
			tc.invalid(obj)
			g.Expect(validate.AgainstSchema(schema, obj, strfmt.Default)).ToNot(Succeed(), "negative control must be rejected")
		})
	}
}

func TestSchemeInitialization(t *testing.T) {
	if os.Getenv("GMP_SCHEME_TEST_CHILD") == "1" {
		g := NewWithT(t)
		for _, scheme := range []*runtime.Scheme{hyperapi.Scheme, hyperapi.AllMonitoringScheme} {
			g.Expect(scheme.Recognizes(gmp.SchemeGroupVersion.WithKind("PodMonitoring"))).To(BeTrue())
			g.Expect(scheme.Recognizes(gmp.SchemeGroupVersion.WithKind("Rules"))).To(BeTrue())
		}
		coreos := schema.GroupVersion{Group: "monitoring.coreos.com", Version: "v1"}.WithKind("ServiceMonitor")
		rhobs := rhobsmonitoring.SchemeGroupVersion.WithKind("ServiceMonitor")
		rhobsEnabled := os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1"
		g.Expect(hyperapi.Scheme.Recognizes(coreos)).To(Equal(!rhobsEnabled))
		g.Expect(hyperapi.Scheme.Recognizes(rhobs)).To(Equal(rhobsEnabled))
		g.Expect(hyperapi.AllMonitoringScheme.Recognizes(coreos)).To(BeTrue())
		g.Expect(hyperapi.AllMonitoringScheme.Recognizes(rhobs)).To(Equal(rhobsEnabled))
		return
	}
	for _, tc := range []struct{ name, rhobs string }{
		{name: "When the default process initializes, it should register GMP alongside CoreOS"},
		{name: "When a RHOBS process initializes, it should register GMP alongside RHOBS", rhobs: "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSchemeInitialization$")
			for _, env := range os.Environ() {
				if !strings.HasPrefix(env, rhobsmonitoring.EnvironmentVariable+"=") && !strings.HasPrefix(env, "GMP_SCHEME_TEST_CHILD=") {
					cmd.Env = append(cmd.Env, env)
				}
			}
			cmd.Env = append(cmd.Env, "GMP_SCHEME_TEST_CHILD=1", rhobsmonitoring.EnvironmentVariable+"="+tc.rhobs)
			output, err := cmd.CombinedOutput()
			g.Expect(err).ToNot(HaveOccurred(), "%s", output)
		})
	}
}
