//go:build e2ev2

package lifecycle

import (
	"reflect"
	"testing"
)

var testRegistry = []ClusterSpec{
	{Variant: "public", ExtraArgs: []string{"--public-only"}},
	{Variant: "private", ExtraArgs: []string{"--endpoint-access=Private"}},
	{Variant: "upgrade", ReleaseImage: "registry.ci.openshift.org/ocp/release:4.16.0-0.nightly"},
}

func TestTestPlanValidate(t *testing.T) {
	tests := []struct {
		name      string
		plan      TestPlan
		wantError bool
	}{
		{
			name: "When all matrix variants exist in the registry, it should succeed",
			plan: TestPlan{
				Name:     "full",
				Platform: "test",
				TestMatrix: TestMatrix{
					Parallel: []TestGroup{
						{Name: "pub", Variant: "public", LabelFilter: "public"},
						{Name: "priv", Variant: "private", LabelFilter: "private"},
					},
				},
			},
		},
		{
			name: "When a parallel matrix variant is not in the registry, it should return an error",
			plan: TestPlan{
				Name:     "bad-parallel",
				Platform: "test",
				TestMatrix: TestMatrix{
					Parallel: []TestGroup{
						{Name: "pub", Variant: "public", LabelFilter: "public"},
						{Name: "missing", Variant: "nonexistent", LabelFilter: "missing"},
					},
				},
			},
			wantError: true,
		},
		{
			name: "When a sequential matrix variant is not in the registry, it should return an error",
			plan: TestPlan{
				Name:     "bad-sequential",
				Platform: "test",
				TestMatrix: TestMatrix{
					Sequential: []SequentialGroup{
						{Name: "seq", Steps: []TestGroup{
							{Name: "step1", Variant: "nonexistent", LabelFilter: "upgrade"},
						}},
					},
				},
			},
			wantError: true,
		},
		{
			name: "When test group names collide, it should return an error",
			plan: TestPlan{
				Name:     "dup-names",
				Platform: "test",
				TestMatrix: TestMatrix{
					Parallel: []TestGroup{
						{Name: "dup", Variant: "public", LabelFilter: "public"},
						{Name: "dup", Variant: "private", LabelFilter: "private"},
					},
				},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.plan.Validate(testRegistry)
			if tt.wantError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestTestMatrixValidate(t *testing.T) {
	tests := []struct {
		name      string
		matrix    TestMatrix
		wantError bool
	}{
		{
			name: "When all group names are unique, it should succeed",
			matrix: TestMatrix{
				Parallel: []TestGroup{
					{Name: "pub", Variant: "public"},
					{Name: "priv", Variant: "private"},
				},
				Sequential: []SequentialGroup{
					{Name: "seq", Steps: []TestGroup{
						{Name: "upgrade", Variant: "upgrade"},
					}},
				},
			},
		},
		{
			name: "When parallel groups have duplicate names, it should return an error",
			matrix: TestMatrix{
				Parallel: []TestGroup{
					{Name: "dup", Variant: "public"},
					{Name: "dup", Variant: "private"},
				},
			},
			wantError: true,
		},
		{
			name: "When a sequential step duplicates a parallel name, it should return an error",
			matrix: TestMatrix{
				Parallel: []TestGroup{
					{Name: "shared", Variant: "public"},
				},
				Sequential: []SequentialGroup{
					{Name: "seq", Steps: []TestGroup{
						{Name: "shared", Variant: "upgrade"},
					}},
				},
			},
			wantError: true,
		},
		{
			name: "When a group name contains a path separator, it should return an error",
			matrix: TestMatrix{
				Parallel: []TestGroup{
					{Name: "foo/bar", Variant: "public"},
				},
			},
			wantError: true,
		},
		{
			name: "When a group name contains a traversal sequence, it should return an error",
			matrix: TestMatrix{
				Parallel: []TestGroup{
					{Name: "..sneaky", Variant: "public"},
				},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.matrix.Validate()
			if tt.wantError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestTestMatrixVariants(t *testing.T) {
	tests := []struct {
		name   string
		matrix TestMatrix
		want   map[string]bool
	}{
		{
			name: "When the matrix has parallel groups, it should return their variants",
			matrix: TestMatrix{
				Parallel: []TestGroup{
					{Name: "pub", Variant: "public"},
					{Name: "priv", Variant: "private"},
				},
			},
			want: map[string]bool{"public": true, "private": true},
		},
		{
			name: "When the matrix has sequential groups, it should return their variants",
			matrix: TestMatrix{
				Sequential: []SequentialGroup{
					{Name: "seq", Steps: []TestGroup{
						{Name: "s1", Variant: "upgrade"},
						{Name: "s2", Variant: "public"},
					}},
				},
			},
			want: map[string]bool{"upgrade": true, "public": true},
		},
		{
			name: "When a variant appears in both parallel and sequential, it should be deduplicated",
			matrix: TestMatrix{
				Parallel: []TestGroup{
					{Name: "pub", Variant: "public"},
				},
				Sequential: []SequentialGroup{
					{Name: "seq", Steps: []TestGroup{
						{Name: "s1", Variant: "public"},
						{Name: "s2", Variant: "upgrade"},
					}},
				},
			},
			want: map[string]bool{"public": true, "upgrade": true},
		},
		{
			name:   "When the matrix is empty, it should return no variants",
			matrix: TestMatrix{},
			want:   map[string]bool{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.matrix.Variants()
			gotSet := make(map[string]bool, len(got))
			for _, v := range got {
				if gotSet[v] {
					t.Errorf("duplicate variant %q in result", v)
				}
				gotSet[v] = true
			}
			if !reflect.DeepEqual(gotSet, tt.want) {
				t.Errorf("got %v, want %v", gotSet, tt.want)
			}
		})
	}
}

func TestTestGroupJUnitFile(t *testing.T) {
	tests := []struct {
		name  string
		group TestGroup
		want  string
	}{
		{
			name:  "When the group name is simple, it should return junit_{name}.xml",
			group: TestGroup{Name: "public", Variant: "public"},
			want:  "junit_public.xml",
		},
		{
			name:  "When the group name differs from the variant, it should use the group name",
			group: TestGroup{Name: "control-plane-tls", Variant: "upgrade"},
			want:  "junit_control-plane-tls.xml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.group.JUnitFile()
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("When the group name contains path traversal, it should panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic, got none")
			}
		}()
		TestGroup{Name: "../../etc/passwd", Variant: "public"}.JUnitFile()
	})
}

func TestTestPlanFilterClusterSpecs(t *testing.T) {
	tests := []struct {
		name         string
		matrix       TestMatrix
		wantVariants []string
	}{
		{
			name: "When the matrix references a subset of the registry, it should return only those specs",
			matrix: TestMatrix{
				Parallel: []TestGroup{{Name: "pub", Variant: "public", LabelFilter: "public"}},
			},
			wantVariants: []string{"public"},
		},
		{
			name: "When the matrix references multiple variants, it should return all of them",
			matrix: TestMatrix{
				Parallel: []TestGroup{
					{Name: "upg", Variant: "upgrade", LabelFilter: "upgrade"},
					{Name: "pub", Variant: "public", LabelFilter: "public"},
				},
			},
			wantVariants: []string{"public", "upgrade"},
		},
		{
			name: "When the matrix references an unknown variant, it should silently skip it",
			matrix: TestMatrix{
				Parallel: []TestGroup{
					{Name: "pub", Variant: "public", LabelFilter: "public"},
					{Name: "bad", Variant: "nonexistent", LabelFilter: "bad"},
				},
			},
			wantVariants: []string{"public"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := TestPlan{TestMatrix: tt.matrix}
			got := plan.FilterClusterSpecs(testRegistry)
			gotVariants := make(map[string]bool, len(got))
			for _, s := range got {
				gotVariants[s.Variant] = true
			}
			wantSet := make(map[string]bool, len(tt.wantVariants))
			for _, v := range tt.wantVariants {
				wantSet[v] = true
			}
			if !reflect.DeepEqual(gotVariants, wantSet) {
				t.Errorf("got variants %v, want %v", gotVariants, wantSet)
			}
		})
	}
}

func TestParseTestPlan(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		yaml      bool
		want      *TestPlan
		wantError bool
	}{
		{
			name: "When parsing JSON with parallel groups, it should deserialize correctly",
			input: `{
  "name": "smoke",
  "platform": "aws",
  "testMatrix": {
    "parallel": [
      {"name": "smoke", "variant": "public", "labelFilter": "smoke"}
    ]
  }
}`,
			want: &TestPlan{
				Name:     "smoke",
				Platform: "aws",
				TestMatrix: TestMatrix{
					Parallel: []TestGroup{
						{Name: "smoke", Variant: "public", LabelFilter: "smoke"},
					},
				},
			},
		},
		{
			name: "When parsing JSON with sequential groups, it should deserialize correctly",
			input: `{
  "name": "upgrade",
  "platform": "azure",
  "testMatrix": {
    "sequential": [
      {"name": "upgrade-flow", "steps": [
        {"name": "pre", "variant": "upgrade", "labelFilter": "upgrade-pre"},
        {"name": "run", "variant": "upgrade", "labelFilter": "upgrade-run"}
      ]}
    ]
  }
}`,
			want: &TestPlan{
				Name:     "upgrade",
				Platform: "azure",
				TestMatrix: TestMatrix{
					Sequential: []SequentialGroup{
						{Name: "upgrade-flow", Steps: []TestGroup{
							{Name: "pre", Variant: "upgrade", LabelFilter: "upgrade-pre"},
							{Name: "run", Variant: "upgrade", LabelFilter: "upgrade-run"},
						}},
					},
				},
			},
		},
		{
			name: "When parsing JSON with an unknown field, it should return an error",
			input: `{
  "name": "typo",
  "platform": "aws",
  "testMatirx": {}
}`,
			wantError: true,
		},
		{
			name: "When parsing YAML with an unknown field, it should return an error",
			yaml: true,
			input: `name: typo
platform: aws
testMatirx: {}
`,
			wantError: true,
		},
		{
			name: "When parsing YAML with parallel groups, it should deserialize correctly",
			yaml: true,
			input: `name: smoke
platform: aws
testMatrix:
  parallel:
    - name: smoke
      variant: public
      labelFilter: smoke
`,
			want: &TestPlan{
				Name:     "smoke",
				Platform: "aws",
				TestMatrix: TestMatrix{
					Parallel: []TestGroup{
						{Name: "smoke", Variant: "public", LabelFilter: "smoke"},
					},
				},
			},
		},
		{
			name: "When parsing YAML with sequential groups, it should deserialize correctly",
			yaml: true,
			input: `name: upgrade
platform: azure
testMatrix:
  sequential:
    - name: upgrade-flow
      steps:
        - name: pre
          variant: upgrade
          labelFilter: upgrade-pre
        - name: run
          variant: upgrade
          labelFilter: upgrade-run
`,
			want: &TestPlan{
				Name:     "upgrade",
				Platform: "azure",
				TestMatrix: TestMatrix{
					Sequential: []SequentialGroup{
						{Name: "upgrade-flow", Steps: []TestGroup{
							{Name: "pre", Variant: "upgrade", LabelFilter: "upgrade-pre"},
							{Name: "run", Variant: "upgrade", LabelFilter: "upgrade-run"},
						}},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTestPlan([]byte(tt.input), tt.yaml)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTestPlan: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
