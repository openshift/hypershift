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

	hyperapi "github.com/openshift/hypershift/support/api"
	gmp "github.com/openshift/hypershift/support/gmpmonitoring"
	"github.com/openshift/hypershift/support/metrics"
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
	"github.com/stretchr/testify/require"
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
	scheme := runtime.NewScheme()
	require.NoError(t, gmp.AddToScheme(scheme))
	for _, kind := range []string{"PodMonitoring", "PodMonitoringList", "Rules", "RulesList"} {
		t.Run("When "+kind+" is registered, it should construct a Kubernetes object", func(t *testing.T) {
			obj, err := scheme.New(gmp.SchemeGroupVersion.WithKind(kind))
			require.NoError(t, err)
			require.NotNil(t, obj.DeepCopyObject())
		})
	}
	require.False(t, scheme.Recognizes(gmp.SchemeGroupVersion.WithKind("ClusterPodMonitoring")))
	require.False(t, scheme.Recognizes(gmp.SchemeGroupVersion.WithKind("ClusterRules")))
}

func TestDeepCopy(t *testing.T) {
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
	require.Equal(t, originalMonitor, monitor)
	copiedRules := rules.DeepCopy()
	copiedRules.Spec.Groups[0].Rules[0].Labels["component"] = "changed"
	copiedRules.Spec.Groups[0].Rules[1].Annotations["summary"] = "changed"
	copiedRules.Status.Conditions[0].Reason = "changed"
	require.Equal(t, originalRules, rules)
	monitorList := &gmp.PodMonitoringList{Items: []gmp.PodMonitoring{*monitor}}
	monitorList.DeepCopy().Items[0].Spec.Endpoints[0].TLS.CA.Secret.Name = "changed"
	require.Equal(t, *originalMonitor, monitorList.Items[0])
	rulesList := &gmp.RulesList{Items: []gmp.Rules{*rules}}
	rulesList.DeepCopy().Items[0].Spec.Groups[0].Rules[0].Labels["component"] = "changed"
	require.Equal(t, *originalRules, rulesList.Items[0])
}

func TestCompatibleYAMLEncode(t *testing.T) {
	monitor, rules := monitoringFixtures()
	objects := []runtime.Object{monitor, rules,
		&gmp.PodMonitoringList{TypeMeta: metav1.TypeMeta{APIVersion: gmp.SchemeGroupVersion.String(), Kind: "PodMonitoringList"}, Items: []gmp.PodMonitoring{*monitor}},
		&gmp.RulesList{TypeMeta: metav1.TypeMeta{APIVersion: gmp.SchemeGroupVersion.String(), Kind: "RulesList"}, Items: []gmp.Rules{*rules}},
	}
	for _, obj := range objects {
		t.Run("When "+obj.GetObjectKind().GroupVersionKind().Kind+" is serialized, it should round trip specs and status", func(t *testing.T) {
			for _, serializer := range []*runtimeSerializer{{encode: func(obj runtime.Object) ([]byte, error) {
				return hyperapi.CompatibleYAMLEncode(obj, hyperapi.YamlSerializer)
			}, decode: hyperapi.YamlSerializer.Decode},
				{encode: func(obj runtime.Object) ([]byte, error) {
					var buf bytes.Buffer
					err := hyperapi.JsonSerializer.Encode(obj, &buf)
					return buf.Bytes(), err
				}, decode: hyperapi.JsonSerializer.Decode}} {
				encoded, err := serializer.encode(obj.DeepCopyObject())
				require.NoError(t, err)
				decoded, gvk, err := serializer.decode(encoded, nil, nil)
				require.NoError(t, err)
				require.Equal(t, gmp.SchemeGroupVersion, gvk.GroupVersion())
				require.True(t, apiequality.Semantic.DeepEqual(obj, decoded), "round trip changed %T", obj)
				decodedAll, _, err := hyperapi.AllMonitoringYamlSerializer.Decode(encoded, nil, nil)
				require.NoError(t, err)
				require.True(t, apiequality.Semantic.DeepEqual(obj, decodedAll), "all-monitoring round trip changed %T", obj)
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
			fixture, err := os.ReadFile(filepath.Join("testdata", tc.fixture+".crd.yaml"))
			require.NoError(t, err)
			var crd struct {
				Spec struct {
					Versions []struct {
						Name   string
						Schema struct{ OpenAPIV3Schema spec.Schema }
					}
				}
			}
			require.NoError(t, yaml.Unmarshal(fixture, &crd))
			var schema *spec.Schema
			for i := range crd.Spec.Versions {
				if crd.Spec.Versions[i].Name == "v1" {
					schema = &crd.Spec.Versions[i].Schema.OpenAPIV3Schema
				}
			}
			require.NotNil(t, schema)
			encoded, err := json.Marshal(tc.object)
			require.NoError(t, err)
			var obj map[string]interface{}
			require.NoError(t, json.Unmarshal(encoded, &obj))
			require.NoError(t, validate.AgainstSchema(schema, obj, strfmt.Default))
			tc.invalid(obj)
			require.Error(t, validate.AgainstSchema(schema, obj, strfmt.Default), "negative control must be rejected")
		})
	}
}

func TestSchemeInitialization(t *testing.T) {
	if os.Getenv("GMP_SCHEME_TEST_CHILD") == "1" {
		for _, scheme := range []*runtime.Scheme{hyperapi.Scheme, hyperapi.AllMonitoringScheme} {
			require.True(t, scheme.Recognizes(gmp.SchemeGroupVersion.WithKind("PodMonitoring")))
			require.True(t, scheme.Recognizes(gmp.SchemeGroupVersion.WithKind("Rules")))
		}
		coreos := schema.GroupVersion{Group: "monitoring.coreos.com", Version: "v1"}.WithKind("ServiceMonitor")
		rhobs := rhobsmonitoring.SchemeGroupVersion.WithKind("ServiceMonitor")
		require.Equal(t, os.Getenv(rhobsmonitoring.EnvironmentVariable) != "1", hyperapi.Scheme.Recognizes(coreos))
		require.Equal(t, os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1", hyperapi.Scheme.Recognizes(rhobs))
		require.True(t, hyperapi.AllMonitoringScheme.Recognizes(coreos))
		require.Equal(t, os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1", hyperapi.AllMonitoringScheme.Recognizes(rhobs))
		return
	}
	for _, tc := range []struct{ name, group, rhobs string }{
		{name: "When the default process initializes, it should retain CoreOS registration"},
		{name: "When a GMP process initializes, it should register GMP additively", group: string(metrics.MonitoringAPIGroupGMP)},
		{name: "When a RHOBS process initializes, it should retain RHOBS registration", rhobs: "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSchemeInitialization$")
			for _, env := range os.Environ() {
				if !strings.HasPrefix(env, metrics.MonitoringAPIGroupEnvVar+"=") && !strings.HasPrefix(env, rhobsmonitoring.EnvironmentVariable+"=") && !strings.HasPrefix(env, "GMP_SCHEME_TEST_CHILD=") {
					cmd.Env = append(cmd.Env, env)
				}
			}
			cmd.Env = append(cmd.Env, "GMP_SCHEME_TEST_CHILD=1", metrics.MonitoringAPIGroupEnvVar+"="+tc.group, rhobsmonitoring.EnvironmentVariable+"="+tc.rhobs)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
		})
	}
}
