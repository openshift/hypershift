package v1beta1

import (
	"encoding/json"
	"reflect"
	"testing"

	"k8s.io/utils/ptr"
)

// clusterAutoscalingNMinus1 represents ClusterAutoscaling before kubeClientQPS
// and kubeClientBurst were added. Used to verify N-1 / N+1 JSON compatibility.
type clusterAutoscalingNMinus1 struct {
	// maxNodesTotal is retained so N-1 round-trips preserve an existing field.
	MaxNodesTotal *int32 `json:"maxNodesTotal,omitempty"` //nolint:kubeapilinter // test-only N-1 compat struct
}

func TestClusterAutoscalingSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		current       ClusterAutoscaling
		expectedJSON  string
		nMinus1Result clusterAutoscalingNMinus1
	}{
		{
			name: "When kubeClientQPS and kubeClientBurst are set it should round-trip to N-1 without those fields",
			current: ClusterAutoscaling{
				MaxNodesTotal:   ptr.To[int32](100),
				KubeClientQPS:   ptr.To[int32](50),
				KubeClientBurst: 100,
			},
			expectedJSON: `{"maxNodesTotal":100,"kubeClientQPS":50,"kubeClientBurst":100}`,
			nMinus1Result: clusterAutoscalingNMinus1{
				MaxNodesTotal: ptr.To[int32](100),
			},
		},
		{
			name: "When kubeClientQPS and kubeClientBurst are nil/zero it should omit them from JSON",
			current: ClusterAutoscaling{
				MaxNodesTotal: ptr.To[int32](10),
			},
			expectedJSON: `{"maxNodesTotal":10}`,
			nMinus1Result: clusterAutoscalingNMinus1{
				MaxNodesTotal: ptr.To[int32](10),
			},
		},
		{
			name: "When only kubeClientQPS is set to -1 it should serialize that field alone",
			current: ClusterAutoscaling{
				KubeClientQPS: ptr.To[int32](-1),
			},
			expectedJSON:  `{"kubeClientQPS":-1}`,
			nMinus1Result: clusterAutoscalingNMinus1{},
		},
		{
			name: "When kubeClientQPS is set to 0 it should serialize 0 and not omit the field",
			current: ClusterAutoscaling{
				KubeClientQPS: ptr.To[int32](0),
			},
			expectedJSON:  `{"kubeClientQPS":0}`,
			nMinus1Result: clusterAutoscalingNMinus1{},
		},
		{
			name: "When kubeClientQPS is set to 1000 it should serialize the maximum value",
			current: ClusterAutoscaling{
				KubeClientQPS: ptr.To[int32](1000),
			},
			expectedJSON:  `{"kubeClientQPS":1000}`,
			nMinus1Result: clusterAutoscalingNMinus1{},
		},
		{
			name: "When only kubeClientBurst is set it should serialize that field alone",
			current: ClusterAutoscaling{
				KubeClientBurst: 200,
			},
			expectedJSON:  `{"kubeClientBurst":200}`,
			nMinus1Result: clusterAutoscalingNMinus1{},
		},
		{
			name: "When kubeClientBurst is set to 1 it should serialize the minimum value",
			current: ClusterAutoscaling{
				KubeClientBurst: 1,
			},
			expectedJSON:  `{"kubeClientBurst":1}`,
			nMinus1Result: clusterAutoscalingNMinus1{},
		},
		{
			name: "When kubeClientBurst is set to 2000 it should serialize the maximum value",
			current: ClusterAutoscaling{
				KubeClientBurst: 2000,
			},
			expectedJSON:  `{"kubeClientBurst":2000}`,
			nMinus1Result: clusterAutoscalingNMinus1{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}
			if string(data) != tt.expectedJSON {
				t.Errorf("unexpected JSON output: got %s, want %s", string(data), tt.expectedJSON)
			}

			var nMinus1 clusterAutoscalingNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}
			if ptr.Deref(nMinus1.MaxNodesTotal, -1) != ptr.Deref(tt.nMinus1Result.MaxNodesTotal, -1) {
				t.Errorf("N-1 MaxNodesTotal mismatch: got %v, want %v", nMinus1.MaxNodesTotal, tt.nMinus1Result.MaxNodesTotal)
			}

			// Reverse: N-1 JSON (without new fields) deserializes into current with nil/zero.
			nMinus1Data, err := json.Marshal(tt.nMinus1Result)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTripped ClusterAutoscaling
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			if roundTripped.KubeClientQPS != nil {
				t.Errorf("KubeClientQPS should be nil after N-1 round-trip, got %v", *roundTripped.KubeClientQPS)
			}
			if roundTripped.KubeClientBurst != 0 {
				t.Errorf("KubeClientBurst should be 0 after N-1 round-trip, got %v", roundTripped.KubeClientBurst)
			}
			if ptr.Deref(roundTripped.MaxNodesTotal, -1) != ptr.Deref(tt.nMinus1Result.MaxNodesTotal, -1) {
				t.Errorf("MaxNodesTotal mismatch after N-1 round-trip: got %v, want %v", roundTripped.MaxNodesTotal, tt.nMinus1Result.MaxNodesTotal)
			}
		})
	}
}

// scaleDownConfigPreNodeBehavior is ScaleDownConfig before nodeTerminationPolicy was added.
type scaleDownConfigPreNodeBehavior struct {
	DelayAfterAddSeconds        *int32 `json:"delayAfterAddSeconds,omitempty"`        //nolint:kubeapilinter // test-only previous-version compat struct
	DelayAfterDeleteSeconds     *int32 `json:"delayAfterDeleteSeconds,omitempty"`     //nolint:kubeapilinter // test-only previous-version compat struct
	DelayAfterFailureSeconds    *int32 `json:"delayAfterFailureSeconds,omitempty"`    //nolint:kubeapilinter // test-only previous-version compat struct
	UnneededDurationSeconds     *int32 `json:"unneededDurationSeconds,omitempty"`     //nolint:kubeapilinter // test-only previous-version compat struct
	UtilizationThresholdPercent *int32 `json:"utilizationThresholdPercent,omitempty"` //nolint:kubeapilinter // test-only previous-version compat struct
}

// clusterAutoscalingPreNodeBehavior is ClusterAutoscaling before resourceLimits, scaleUp, logLevel,
// scaleDown.nodeTerminationPolicy, scaleDown.nodesWithLocalStoragePolicy,
// scaleDown.daemonSetUtilization and scaleUp.similarNodePoolsBalancing were added.
type clusterAutoscalingPreNodeBehavior struct {
	Scaling                       ScalingType                     `json:"scaling,omitempty"`                       //nolint:kubeapilinter // test-only previous-version compat struct
	ScaleDown                     *scaleDownConfigPreNodeBehavior `json:"scaleDown,omitempty"`                     //nolint:kubeapilinter // test-only previous-version compat struct
	BalancingIgnoredLabels        []string                        `json:"balancingIgnoredLabels,omitempty"`        //nolint:kubeapilinter // test-only previous-version compat struct
	MaxNodesTotal                 *int32                          `json:"maxNodesTotal,omitempty"`                 //nolint:kubeapilinter // test-only previous-version compat struct
	MaxPodGracePeriod             *int32                          `json:"maxPodGracePeriod,omitempty"`             //nolint:kubeapilinter // test-only previous-version compat struct
	MaxNodeProvisionTime          string                          `json:"maxNodeProvisionTime,omitempty"`          //nolint:kubeapilinter // test-only previous-version compat struct
	MaxFreeDifferenceRatioPercent *int32                          `json:"maxFreeDifferenceRatioPercent,omitempty"` //nolint:kubeapilinter // test-only previous-version compat struct
	PodPriorityThreshold          *int32                          `json:"podPriorityThreshold,omitempty"`          //nolint:kubeapilinter // test-only previous-version compat struct
	Expanders                     []ExpanderString                `json:"expanders,omitempty"`                     //nolint:kubeapilinter // test-only previous-version compat struct
	KubeClientQPS                 *int32                          `json:"kubeClientQPS,omitempty"`                 //nolint:kubeapilinter // test-only previous-version compat struct
	KubeClientBurst               int32                           `json:"kubeClientBurst,omitempty"`               //nolint:kubeapilinter // test-only previous-version compat struct
}

func TestClusterAutoscalingNodeBehaviorSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name         string
		current      ClusterAutoscaling
		expectedJSON string
		// previous is what an older consumer is expected to read from the current JSON.
		previous clusterAutoscalingPreNodeBehavior
	}{
		{
			name:         "When none of the new fields are set it should serialize identically to the previous version",
			current:      ClusterAutoscaling{Scaling: ScaleUpAndScaleDown, MaxNodesTotal: ptr.To[int32](10)},
			expectedJSON: `{"scaling":"ScaleUpAndScaleDown","maxNodesTotal":10}`,
			previous:     clusterAutoscalingPreNodeBehavior{Scaling: ScaleUpAndScaleDown, MaxNodesTotal: ptr.To[int32](10)},
		},
		{
			name:         "When resourceLimits and scaleUp are zero values it should omit them",
			current:      ClusterAutoscaling{ResourceLimits: ResourceLimits{}, ScaleUp: ScaleUpConfig{}},
			expectedJSON: `{}`,
			previous:     clusterAutoscalingPreNodeBehavior{},
		},
		{
			name: "When resourceLimits cores and memory have min and max it should serialize both ranges",
			current: ClusterAutoscaling{ResourceLimits: ResourceLimits{
				Cores:  ResourceRange{Min: ptr.To[int32](4), Max: 128},
				Memory: ResourceRange{Min: ptr.To[int32](16), Max: 512},
			}},
			expectedJSON: `{"resourceLimits":{"cores":{"min":4,"max":128},"memory":{"min":16,"max":512}}}`,
			previous:     clusterAutoscalingPreNodeBehavior{},
		},
		{
			name: "When resourceLimits min is an explicit zero it should keep min 0 and not omit it",
			current: ClusterAutoscaling{ResourceLimits: ResourceLimits{
				Cores: ResourceRange{Min: ptr.To[int32](0), Max: 64},
			}},
			expectedJSON: `{"resourceLimits":{"cores":{"min":0,"max":64}}}`,
			previous:     clusterAutoscalingPreNodeBehavior{},
		},
		{
			name: "When resourceLimits min is nil it should omit min",
			current: ClusterAutoscaling{ResourceLimits: ResourceLimits{
				Memory: ResourceRange{Max: 256},
			}},
			expectedJSON: `{"resourceLimits":{"memory":{"max":256}}}`,
			previous:     clusterAutoscalingPreNodeBehavior{},
		},
		{
			name: "When resourceLimits max is zero it should omit max and keep an explicit min 0",
			current: ClusterAutoscaling{ResourceLimits: ResourceLimits{
				Cores: ResourceRange{Min: ptr.To[int32](0)},
			}},
			expectedJSON: `{"resourceLimits":{"cores":{"min":0}}}`,
			previous:     clusterAutoscalingPreNodeBehavior{},
		},
		{
			name: "When every node behavior enum is the zero value it should omit all of them",
			current: ClusterAutoscaling{
				ScaleDown: &ScaleDownConfig{
					NodeTerminationPolicy:       "",
					NodesWithLocalStoragePolicy: "",
					DaemonSetUtilization:        "",
				},
				ScaleUp: ScaleUpConfig{SimilarNodePoolsBalancing: ""},
			},
			expectedJSON: `{"scaleDown":{}}`,
			previous:     clusterAutoscalingPreNodeBehavior{ScaleDown: &scaleDownConfigPreNodeBehavior{}},
		},
		{
			name: "When a GPU limit has max 0 it should keep max 0 and not omit it",
			current: ClusterAutoscaling{ResourceLimits: ResourceLimits{
				GPUs: []GPULimit{{Type: "nvidia-a100", Min: ptr.To[int32](0), Max: ptr.To[int32](0)}},
			}},
			expectedJSON: `{"resourceLimits":{"gpus":[{"type":"nvidia-a100","min":0,"max":0}]}}`,
			previous:     clusterAutoscalingPreNodeBehavior{},
		},
		{
			name: "When GPU limits have nil min and max it should omit them and keep multiple entries in order",
			current: ClusterAutoscaling{ResourceLimits: ResourceLimits{
				GPUs: []GPULimit{
					{Type: "nvidia-tesla-t4"},
					{Type: "nvidia-a100", Min: ptr.To[int32](1), Max: ptr.To[int32](8)},
				},
			}},
			expectedJSON: `{"resourceLimits":{"gpus":[{"type":"nvidia-tesla-t4"},{"type":"nvidia-a100","min":1,"max":8}]}}`,
			previous:     clusterAutoscalingPreNodeBehavior{},
		},
		{
			name:         "When logLevel is the zero value it should omit the field",
			current:      ClusterAutoscaling{LogLevel: ""},
			expectedJSON: `{}`,
			previous:     clusterAutoscalingPreNodeBehavior{},
		},
		{
			name:         "When logLevel is Normal it should serialize it as a string",
			current:      ClusterAutoscaling{LogLevel: Normal},
			expectedJSON: `{"logLevel":"Normal"}`,
			previous:     clusterAutoscalingPreNodeBehavior{},
		},
		{
			name:         "When logLevel is TraceAll it should serialize the most verbose value",
			current:      ClusterAutoscaling{LogLevel: TraceAll},
			expectedJSON: `{"logLevel":"TraceAll"}`,
			previous:     clusterAutoscalingPreNodeBehavior{},
		},
		{
			name: "When the node behavior policies are set it should serialize them as strings under scaleDown and scaleUp",
			current: ClusterAutoscaling{
				ScaleDown: &ScaleDownConfig{
					NodesWithLocalStoragePolicy: NodesWithLocalStorageSkip,
					DaemonSetUtilization:        DaemonSetUtilizationExclude,
				},
				ScaleUp: ScaleUpConfig{SimilarNodePoolsBalancing: SimilarNodePoolsBalancingNone},
			},
			expectedJSON: `{"scaleDown":{"nodesWithLocalStoragePolicy":"Skip","daemonSetUtilization":"Exclude"},"scaleUp":{"similarNodePoolsBalancing":"None"}}`,
			previous:     clusterAutoscalingPreNodeBehavior{ScaleDown: &scaleDownConfigPreNodeBehavior{}},
		},
		{
			name: "When the opposite node behavior policies are set it should serialize them as strings under scaleDown and scaleUp",
			current: ClusterAutoscaling{
				ScaleDown: &ScaleDownConfig{
					NodesWithLocalStoragePolicy: NodesWithLocalStorageDelete,
					DaemonSetUtilization:        DaemonSetUtilizationInclude,
				},
				ScaleUp: ScaleUpConfig{SimilarNodePoolsBalancing: SimilarNodePoolsBalancingAuto},
			},
			expectedJSON: `{"scaleDown":{"nodesWithLocalStoragePolicy":"Delete","daemonSetUtilization":"Include"},"scaleUp":{"similarNodePoolsBalancing":"Auto"}}`,
			previous:     clusterAutoscalingPreNodeBehavior{ScaleDown: &scaleDownConfigPreNodeBehavior{}},
		},
		{
			name: "When scaleDown nodeTerminationPolicy is Cordon it should keep existing scaleDown fields readable by the previous version",
			current: ClusterAutoscaling{ScaleDown: &ScaleDownConfig{
				DelayAfterDeleteSeconds:     ptr.To[int32](60),
				UtilizationThresholdPercent: ptr.To[int32](50),
				NodeTerminationPolicy:       NodeTerminationPolicyCordon,
			}},
			expectedJSON: `{"scaleDown":{"delayAfterDeleteSeconds":60,"utilizationThresholdPercent":50,"nodeTerminationPolicy":"Cordon"}}`,
			previous: clusterAutoscalingPreNodeBehavior{ScaleDown: &scaleDownConfigPreNodeBehavior{
				DelayAfterDeleteSeconds:     ptr.To[int32](60),
				UtilizationThresholdPercent: ptr.To[int32](50),
			}},
		},
		{
			name: "When scaleDown nodeTerminationPolicy is DoNotCordon with delayAfterDeleteSeconds 0 it should keep both",
			current: ClusterAutoscaling{ScaleDown: &ScaleDownConfig{
				DelayAfterDeleteSeconds: ptr.To[int32](0),
				NodeTerminationPolicy:   NodeTerminationPolicyDoNotCordon,
			}},
			expectedJSON: `{"scaleDown":{"delayAfterDeleteSeconds":0,"nodeTerminationPolicy":"DoNotCordon"}}`,
			previous: clusterAutoscalingPreNodeBehavior{ScaleDown: &scaleDownConfigPreNodeBehavior{
				DelayAfterDeleteSeconds: ptr.To[int32](0),
			}},
		},
		{
			name:         "When scaleDown is set without nodeTerminationPolicy it should omit the policy field",
			current:      ClusterAutoscaling{ScaleDown: &ScaleDownConfig{UnneededDurationSeconds: ptr.To[int32](600)}},
			expectedJSON: `{"scaleDown":{"unneededDurationSeconds":600}}`,
			previous: clusterAutoscalingPreNodeBehavior{ScaleDown: &scaleDownConfigPreNodeBehavior{
				UnneededDurationSeconds: ptr.To[int32](600),
			}},
		},
		{
			name:         "When scaleUp newPodScaleUpDelaySeconds is an explicit 0 it should serialize 0 and not omit the field",
			current:      ClusterAutoscaling{ScaleUp: ScaleUpConfig{NewPodScaleUpDelaySeconds: ptr.To[int32](0)}},
			expectedJSON: `{"scaleUp":{"newPodScaleUpDelaySeconds":0}}`,
			previous:     clusterAutoscalingPreNodeBehavior{},
		},
		{
			name:         "When scaleUp newPodScaleUpDelaySeconds is 3600 it should serialize the maximum value",
			current:      ClusterAutoscaling{ScaleUp: ScaleUpConfig{NewPodScaleUpDelaySeconds: ptr.To[int32](3600)}},
			expectedJSON: `{"scaleUp":{"newPodScaleUpDelaySeconds":3600}}`,
			previous:     clusterAutoscalingPreNodeBehavior{},
		},
		{
			name:         "When scaleUp sets only similarNodePoolsBalancing it should omit newPodScaleUpDelaySeconds",
			current:      ClusterAutoscaling{ScaleUp: ScaleUpConfig{SimilarNodePoolsBalancing: SimilarNodePoolsBalancingAuto}},
			expectedJSON: `{"scaleUp":{"similarNodePoolsBalancing":"Auto"}}`,
			previous:     clusterAutoscalingPreNodeBehavior{},
		},
		{
			name: "When new and existing fields are all set it should round-trip with the previous version reading only existing fields",
			current: ClusterAutoscaling{
				Scaling:       ScaleUpAndScaleDown,
				MaxNodesTotal: ptr.To[int32](100),
				KubeClientQPS: ptr.To[int32](50),
				ScaleDown: &ScaleDownConfig{
					DelayAfterDeleteSeconds:     ptr.To[int32](60),
					NodeTerminationPolicy:       NodeTerminationPolicyCordon,
					NodesWithLocalStoragePolicy: NodesWithLocalStorageSkip,
				},
				ResourceLimits: ResourceLimits{
					Cores: ResourceRange{Min: ptr.To[int32](2), Max: 32},
					GPUs:  []GPULimit{{Type: "nvidia-a100", Max: ptr.To[int32](0)}},
				},
				ScaleUp:  ScaleUpConfig{NewPodScaleUpDelaySeconds: ptr.To[int32](30), SimilarNodePoolsBalancing: SimilarNodePoolsBalancingNone},
				LogLevel: Debug,
			},
			expectedJSON: `{"scaling":"ScaleUpAndScaleDown","scaleDown":{"delayAfterDeleteSeconds":60,"nodeTerminationPolicy":"Cordon","nodesWithLocalStoragePolicy":"Skip"},"maxNodesTotal":100,"kubeClientQPS":50,"resourceLimits":{"cores":{"min":2,"max":32},"gpus":[{"type":"nvidia-a100","max":0}]},"scaleUp":{"newPodScaleUpDelaySeconds":30,"similarNodePoolsBalancing":"None"},"logLevel":"Debug"}`,
			previous: clusterAutoscalingPreNodeBehavior{
				Scaling:       ScaleUpAndScaleDown,
				MaxNodesTotal: ptr.To[int32](100),
				KubeClientQPS: ptr.To[int32](50),
				ScaleDown:     &scaleDownConfigPreNodeBehavior{DelayAfterDeleteSeconds: ptr.To[int32](60)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}
			if string(data) != tt.expectedJSON {
				t.Errorf("unexpected JSON output:\n got %s\nwant %s", data, tt.expectedJSON)
			}

			// N-1 (rollback): the previous version reads current JSON, ignores the new fields
			// and preserves every field it knows about.
			var previous clusterAutoscalingPreNodeBehavior
			if err := json.Unmarshal(data, &previous); err != nil {
				t.Fatalf("previous version failed to unmarshal JSON from current: %v", err)
			}
			if !reflect.DeepEqual(previous, tt.previous) {
				t.Errorf("previous version read unexpected content:\n got %+v\nwant %+v", previous, tt.previous)
			}

			// N+1 (forward): current reads JSON written by the previous version with the new
			// fields nil or zero and the existing fields intact.
			previousData, err := json.Marshal(tt.previous)
			if err != nil {
				t.Fatalf("failed to marshal previous struct: %v", err)
			}
			var forward ClusterAutoscaling
			if err := json.Unmarshal(previousData, &forward); err != nil {
				t.Fatalf("current failed to unmarshal JSON from previous: %v", err)
			}
			if forward.LogLevel != "" {
				t.Errorf("new fields should be unset after reading previous-version JSON, got %+v", forward)
			}
			if !reflect.DeepEqual(forward.ResourceLimits, ResourceLimits{}) {
				t.Errorf("ResourceLimits should be zero after reading previous-version JSON, got %+v", forward.ResourceLimits)
			}
			if forward.ScaleUp != (ScaleUpConfig{}) {
				t.Errorf("ScaleUp should be zero after reading previous-version JSON, got %+v", forward.ScaleUp)
			}
			if forward.ScaleDown != nil && (forward.ScaleDown.NodeTerminationPolicy != "" ||
				forward.ScaleDown.NodesWithLocalStoragePolicy != "" || forward.ScaleDown.DaemonSetUtilization != "") {
				t.Errorf("new scaleDown fields should be unset after reading previous-version JSON, got %+v", forward.ScaleDown)
			}
			if forward.Scaling != tt.previous.Scaling || ptr.Deref(forward.MaxNodesTotal, -1) != ptr.Deref(tt.previous.MaxNodesTotal, -1) {
				t.Errorf("existing fields not preserved reading previous-version JSON, got %+v", forward)
			}

			// Current-to-current round trip preserves everything, including explicit zeros.
			var roundTripped ClusterAutoscaling
			if err := json.Unmarshal(data, &roundTripped); err != nil {
				t.Fatalf("current failed to unmarshal its own JSON: %v", err)
			}
			if !reflect.DeepEqual(roundTripped, tt.current) {
				t.Errorf("current round-trip mismatch:\n got %+v\nwant %+v", roundTripped, tt.current)
			}
		})
	}
}

// Stored data written outside the API server (for example by a consumer that serializes these
// types itself) may contain empty-string enums. Reading it must not fail and must yield the zero value.
func TestClusterAutoscalingEmptyStringEnumsDeserialize(t *testing.T) {
	input := `{"scaleDown":{"nodeTerminationPolicy":"","nodesWithLocalStoragePolicy":"","daemonSetUtilization":""},"scaleUp":{"similarNodePoolsBalancing":""}}`

	var got ClusterAutoscaling
	if err := json.Unmarshal([]byte(input), &got); err != nil {
		t.Fatalf("failed to unmarshal empty-string enums: %v", err)
	}
	if got.ScaleUp.SimilarNodePoolsBalancing != "" {
		t.Errorf("expected zero-value similarNodePoolsBalancing, got %+v", got.ScaleUp)
	}
	if got.ScaleDown == nil || got.ScaleDown.NodeTerminationPolicy != "" ||
		got.ScaleDown.NodesWithLocalStoragePolicy != "" || got.ScaleDown.DaemonSetUtilization != "" {
		t.Errorf("expected scaleDown with zero-value enums, got %+v", got.ScaleDown)
	}

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if want := `{"scaleDown":{}}`; string(data) != want {
		t.Errorf("empty-string enums should be omitted on re-serialization: got %s, want %s", data, want)
	}
}
