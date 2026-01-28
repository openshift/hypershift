package docs

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestExtractFlagMapFromCommand_LocalFlags(t *testing.T) {
	tests := []struct {
		name          string
		setupCmd      func() *cobra.Command
		expectedFlags map[string]bool
		expectedReq   map[string]bool
	}{
		{
			name: "When command has local flags it should extract them",
			setupCmd: func() *cobra.Command {
				cmd := &cobra.Command{Use: "test"}
				cmd.Flags().String("local-flag", "default", "A local flag")
				return cmd
			},
			expectedFlags: map[string]bool{"local-flag": true},
			expectedReq:   map[string]bool{},
		},
		{
			name: "When local flag is marked required it should detect annotation",
			setupCmd: func() *cobra.Command {
				cmd := &cobra.Command{Use: "test"}
				cmd.Flags().String("required-flag", "", "A required flag")
				_ = cmd.MarkFlagRequired("required-flag")
				return cmd
			},
			expectedFlags: map[string]bool{"required-flag": true},
			expectedReq:   map[string]bool{"required-flag": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.setupCmd()
			flags := extractFlagMapFromCommand(cmd)

			for flagName := range tt.expectedFlags {
				if _, ok := flags[flagName]; !ok {
					t.Errorf("expected flag %q to be extracted", flagName)
				}
			}

			for flagName, shouldBeReq := range tt.expectedReq {
				flag, ok := flags[flagName]
				if !ok {
					t.Errorf("expected flag %q to exist", flagName)
					continue
				}
				// Check RequiredInHcp since addFlagToMap sets both fields the same
				isRequired := flag.RequiredInHcp
				if isRequired != shouldBeReq {
					t.Errorf("flag %q: expected required=%v, got required=%v", flagName, shouldBeReq, isRequired)
				}
			}
		})
	}
}

func TestExtractFlagMapFromCommand_PersistentFlags(t *testing.T) {
	tests := []struct {
		name          string
		setupCmd      func() *cobra.Command
		expectedFlags map[string]bool
		expectedReq   map[string]bool
	}{
		{
			name: "When command has persistent flags it should extract them",
			setupCmd: func() *cobra.Command {
				cmd := &cobra.Command{Use: "test"}
				cmd.PersistentFlags().String("persistent-flag", "default", "A persistent flag")
				return cmd
			},
			expectedFlags: map[string]bool{"persistent-flag": true},
			expectedReq:   map[string]bool{},
		},
		{
			name: "When persistent flag is marked required it should detect annotation",
			setupCmd: func() *cobra.Command {
				cmd := &cobra.Command{Use: "test"}
				cmd.PersistentFlags().String("name", "", "A required persistent flag")
				_ = cmd.MarkPersistentFlagRequired("name")
				return cmd
			},
			expectedFlags: map[string]bool{"name": true},
			expectedReq:   map[string]bool{"name": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.setupCmd()
			flags := extractFlagMapFromCommand(cmd)

			for flagName := range tt.expectedFlags {
				if _, ok := flags[flagName]; !ok {
					t.Errorf("expected flag %q to be extracted", flagName)
				}
			}

			for flagName, shouldBeReq := range tt.expectedReq {
				flag, ok := flags[flagName]
				if !ok {
					t.Errorf("expected flag %q to exist", flagName)
					continue
				}
				// Check RequiredInHcp since addFlagToMap sets both fields the same
				isRequired := flag.RequiredInHcp
				if isRequired != shouldBeReq {
					t.Errorf("flag %q: expected required=%v, got required=%v", flagName, shouldBeReq, isRequired)
				}
			}
		})
	}
}

func TestExtractFlagMapFromCommand_InheritedPersistentFlags(t *testing.T) {
	tests := []struct {
		name          string
		setupCmd      func() *cobra.Command
		expectedFlags map[string]bool
		expectedReq   map[string]bool
	}{
		{
			name: "When parent has persistent flags it should extract them from child",
			setupCmd: func() *cobra.Command {
				parent := &cobra.Command{Use: "parent"}
				parent.PersistentFlags().String("parent-flag", "default", "A parent flag")

				child := &cobra.Command{Use: "child"}
				parent.AddCommand(child)
				return child
			},
			expectedFlags: map[string]bool{"parent-flag": true},
			expectedReq:   map[string]bool{},
		},
		{
			name: "When parent persistent flag is marked required it should detect annotation on child",
			setupCmd: func() *cobra.Command {
				parent := &cobra.Command{Use: "parent"}
				parent.PersistentFlags().String("name", "", "A required parent flag")
				_ = parent.MarkPersistentFlagRequired("name")

				child := &cobra.Command{Use: "child"}
				parent.AddCommand(child)
				return child
			},
			expectedFlags: map[string]bool{"name": true},
			expectedReq:   map[string]bool{"name": true},
		},
		{
			name: "When grandparent persistent flag is marked required it should detect annotation on grandchild",
			setupCmd: func() *cobra.Command {
				grandparent := &cobra.Command{Use: "grandparent"}
				grandparent.PersistentFlags().String("pull-secret", "", "A required grandparent flag")
				_ = grandparent.MarkPersistentFlagRequired("pull-secret")

				parent := &cobra.Command{Use: "parent"}
				grandparent.AddCommand(parent)

				child := &cobra.Command{Use: "child"}
				parent.AddCommand(child)
				return child
			},
			expectedFlags: map[string]bool{"pull-secret": true},
			expectedReq:   map[string]bool{"pull-secret": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.setupCmd()
			flags := extractFlagMapFromCommand(cmd)

			for flagName := range tt.expectedFlags {
				if _, ok := flags[flagName]; !ok {
					t.Errorf("expected flag %q to be extracted", flagName)
				}
			}

			for flagName, shouldBeReq := range tt.expectedReq {
				flag, ok := flags[flagName]
				if !ok {
					t.Errorf("expected flag %q to exist", flagName)
					continue
				}
				// Check RequiredInHcp since addFlagToMap sets both fields the same
				isRequired := flag.RequiredInHcp
				if isRequired != shouldBeReq {
					t.Errorf("flag %q: expected required=%v, got required=%v", flagName, shouldBeReq, isRequired)
				}
			}
		})
	}
}

func TestExtractFlagMapFromCommand_MixedFlags(t *testing.T) {
	t.Run("When command has local, persistent, and inherited flags it should extract all with correct required status", func(t *testing.T) {
		// Setup: grandparent -> parent -> child with various flags
		grandparent := &cobra.Command{Use: "grandparent"}
		grandparent.PersistentFlags().String("name", "", "Required from grandparent")
		_ = grandparent.MarkPersistentFlagRequired("name")

		parent := &cobra.Command{Use: "parent"}
		parent.PersistentFlags().String("namespace", "clusters", "Persistent from parent")
		grandparent.AddCommand(parent)

		child := &cobra.Command{Use: "child"}
		child.Flags().String("region", "us-east-1", "Local flag on child")
		child.Flags().String("instance-type", "", "Required local flag")
		_ = child.MarkFlagRequired("instance-type")
		parent.AddCommand(child)

		flags := extractFlagMapFromCommand(child)

		// Verify all flags are extracted
		expectedFlags := []string{"name", "namespace", "region", "instance-type"}
		for _, flagName := range expectedFlags {
			if _, ok := flags[flagName]; !ok {
				t.Errorf("expected flag %q to be extracted", flagName)
			}
		}

		// Verify required status - check RequiredInHcp since addFlagToMap sets both fields the same
		if !flags["name"].RequiredInHcp {
			t.Errorf("flag 'name' should be required")
		}
		if !flags["instance-type"].RequiredInHcp {
			t.Errorf("flag 'instance-type' should be required")
		}
		if flags["namespace"].RequiredInHcp {
			t.Errorf("flag 'namespace' should not be required")
		}
		if flags["region"].RequiredInHcp {
			t.Errorf("flag 'region' should not be required")
		}
	})
}

func TestAddFlagToMap(t *testing.T) {
	t.Run("When flag has required annotation it should set RequiredInHcp and RequiredInHypershift", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("test-flag", "", "Test description")
		f := fs.Lookup("test-flag")
		f.Annotations = map[string][]string{
			BashCompOneRequiredFlag: {"true"},
		}

		result := make(map[string]*FlagInfo)
		addFlagToMap(result, f)

		flagInfo := result["test-flag"]
		if !flagInfo.RequiredInHcp || !flagInfo.RequiredInHypershift {
			t.Errorf("expected RequiredInHcp and RequiredInHypershift to be true")
		}
		// Category should be "Other" since "test-flag" is not in FlagCategories
		if flagInfo.Category != "Other" {
			t.Errorf("expected category 'Other', got %q", flagInfo.Category)
		}
	})

	t.Run("When flag has no annotation it should use category from FlagCategories", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("region", "us-east-1", "AWS region")
		f := fs.Lookup("region")

		result := make(map[string]*FlagInfo)
		addFlagToMap(result, f)

		flagInfo := result["region"]
		if flagInfo.Category != "AWS Infrastructure" {
			t.Errorf("expected category 'AWS Infrastructure', got %q", flagInfo.Category)
		}
		if flagInfo.RequiredInHcp || flagInfo.RequiredInHypershift {
			t.Errorf("expected RequiredInHcp and RequiredInHypershift to be false")
		}
	})

	t.Run("When flag is not in FlagCategories it should default to Other", func(t *testing.T) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("unknown-flag", "", "Unknown flag")
		f := fs.Lookup("unknown-flag")

		result := make(map[string]*FlagInfo)
		addFlagToMap(result, f)

		flagInfo := result["unknown-flag"]
		if flagInfo.Category != "Other" {
			t.Errorf("expected category 'Other', got %q", flagInfo.Category)
		}
	})
}

func TestCleanDefault(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "When default is empty array it should return empty string",
			input:    "[]",
			expected: "",
		},
		{
			name:     "When default is empty map it should return empty string",
			input:    "map[]",
			expected: "",
		},
		{
			name:     "When default is zero duration it should return empty string",
			input:    "0s",
			expected: "",
		},
		{
			name:     "When default is regular value it should return as-is",
			input:    "us-east-1",
			expected: "us-east-1",
		},
		{
			name:     "When default is empty string it should return empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanDefault(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestMergeFlags(t *testing.T) {
	t.Run("When merging hcp and hypershift flags it should mark availability correctly", func(t *testing.T) {
		hcpFlags := map[string]*FlagInfo{
			"name":      {Name: "name", Category: "Cluster Identity", RequiredInHcp: true, RequiredInHypershift: true},
			"namespace": {Name: "namespace", Category: "Cluster Identity"},
		}

		hypershiftFlags := map[string]*FlagInfo{
			"name":       {Name: "name", Category: "Cluster Identity", RequiredInHcp: true, RequiredInHypershift: true},
			"aws-creds":  {Name: "aws-creds", Category: "Developer-Only"},
			"infra-json": {Name: "infra-json", Category: "Developer-Only"},
		}

		merged := mergeFlags(hcpFlags, hypershiftFlags)

		// Find flags by name
		flagMap := make(map[string]FlagInfo)
		for _, f := range merged {
			flagMap[f.Name] = f
		}

		// name should be in both
		if !flagMap["name"].InHcp || !flagMap["name"].InHypershift {
			t.Errorf("flag 'name' should be in both CLIs")
		}

		// namespace should be hcp only
		if !flagMap["namespace"].InHcp || flagMap["namespace"].InHypershift {
			t.Errorf("flag 'namespace' should be in hcp only")
		}

		// aws-creds should be hypershift only
		if flagMap["aws-creds"].InHcp || !flagMap["aws-creds"].InHypershift {
			t.Errorf("flag 'aws-creds' should be in hypershift only")
		}
	})

	t.Run("When merging flags it should preserve per-CLI required status", func(t *testing.T) {
		// hcp has name required, hypershift has name required
		hcpFlags := map[string]*FlagInfo{
			"name": {Name: "name", Category: "Cluster Identity", RequiredInHcp: true, RequiredInHypershift: true},
		}
		hypershiftFlags := map[string]*FlagInfo{
			"name": {Name: "name", Category: "Cluster Identity", RequiredInHcp: true, RequiredInHypershift: true},
		}

		merged := mergeFlags(hcpFlags, hypershiftFlags)
		flagMap := make(map[string]FlagInfo)
		for _, f := range merged {
			flagMap[f.Name] = f
		}

		// name should be required in both
		if !flagMap["name"].RequiredInHcp {
			t.Errorf("flag 'name' should be RequiredInHcp")
		}
		if !flagMap["name"].RequiredInHypershift {
			t.Errorf("flag 'name' should be RequiredInHypershift")
		}
		if flagMap["name"].Category != "Required" {
			t.Errorf("flag 'name' should have category 'Required', got %q", flagMap["name"].Category)
		}
	})

	t.Run("When flag is required in hcp only it should set RequiredInHcp only", func(t *testing.T) {
		hcpFlags := map[string]*FlagInfo{
			"hcp-only-req": {Name: "hcp-only-req", Category: "Other", RequiredInHcp: true, RequiredInHypershift: true},
		}
		hypershiftFlags := map[string]*FlagInfo{
			"hcp-only-req": {Name: "hcp-only-req", Category: "Other", RequiredInHcp: false, RequiredInHypershift: false},
		}

		merged := mergeFlags(hcpFlags, hypershiftFlags)
		flagMap := make(map[string]FlagInfo)
		for _, f := range merged {
			flagMap[f.Name] = f
		}

		// Should be required in hcp only
		if !flagMap["hcp-only-req"].RequiredInHcp {
			t.Errorf("flag 'hcp-only-req' should be RequiredInHcp")
		}
		if flagMap["hcp-only-req"].RequiredInHypershift {
			t.Errorf("flag 'hcp-only-req' should NOT be RequiredInHypershift")
		}
		// Category should still be Required since it's required in at least one CLI
		if flagMap["hcp-only-req"].Category != "Required" {
			t.Errorf("flag 'hcp-only-req' should have category 'Required', got %q", flagMap["hcp-only-req"].Category)
		}
	})

	t.Run("When flag is required in hypershift only it should set RequiredInHypershift only", func(t *testing.T) {
		hcpFlags := map[string]*FlagInfo{
			"hs-only-req": {Name: "hs-only-req", Category: "Other", RequiredInHcp: false, RequiredInHypershift: false},
		}
		hypershiftFlags := map[string]*FlagInfo{
			"hs-only-req": {Name: "hs-only-req", Category: "Other", RequiredInHcp: true, RequiredInHypershift: true},
		}

		merged := mergeFlags(hcpFlags, hypershiftFlags)
		flagMap := make(map[string]FlagInfo)
		for _, f := range merged {
			flagMap[f.Name] = f
		}

		// Should be required in hypershift only
		if flagMap["hs-only-req"].RequiredInHcp {
			t.Errorf("flag 'hs-only-req' should NOT be RequiredInHcp")
		}
		if !flagMap["hs-only-req"].RequiredInHypershift {
			t.Errorf("flag 'hs-only-req' should be RequiredInHypershift")
		}
		// Category should still be Required since it's required in at least one CLI
		if flagMap["hs-only-req"].Category != "Required" {
			t.Errorf("flag 'hs-only-req' should have category 'Required', got %q", flagMap["hs-only-req"].Category)
		}
	})

	t.Run("When flag only exists in hypershift and is required it should set RequiredInHypershift", func(t *testing.T) {
		hcpFlags := map[string]*FlagInfo{}
		hypershiftFlags := map[string]*FlagInfo{
			"dev-only": {Name: "dev-only", Category: "Developer-Only", RequiredInHcp: true, RequiredInHypershift: true},
		}

		merged := mergeFlags(hcpFlags, hypershiftFlags)
		flagMap := make(map[string]FlagInfo)
		for _, f := range merged {
			flagMap[f.Name] = f
		}

		// Should be in hypershift only and required in hypershift only
		if flagMap["dev-only"].InHcp {
			t.Errorf("flag 'dev-only' should NOT be InHcp")
		}
		if !flagMap["dev-only"].InHypershift {
			t.Errorf("flag 'dev-only' should be InHypershift")
		}
		if flagMap["dev-only"].RequiredInHcp {
			t.Errorf("flag 'dev-only' should NOT be RequiredInHcp")
		}
		if !flagMap["dev-only"].RequiredInHypershift {
			t.Errorf("flag 'dev-only' should be RequiredInHypershift")
		}
	})
}

func TestGroupByCategory(t *testing.T) {
	t.Run("When grouping flags by category it should follow CategoryOrder", func(t *testing.T) {
		flags := []FlagInfo{
			{Name: "region", Category: "AWS Infrastructure"},
			{Name: "name", Category: "Required"},
			{Name: "namespace", Category: "Cluster Identity"},
			{Name: "aws-creds", Category: "Developer-Only"},
		}

		categories := groupByCategory(flags)

		// Required should come first
		if len(categories) == 0 || categories[0].Name != "Required" {
			t.Errorf("expected first category to be 'Required'")
		}

		// Verify order matches CategoryOrder
		categoryIndex := make(map[string]int)
		for i, cat := range categories {
			categoryIndex[cat.Name] = i
		}

		if categoryIndex["Required"] > categoryIndex["Cluster Identity"] {
			t.Errorf("Required should come before Cluster Identity")
		}
		if categoryIndex["Cluster Identity"] > categoryIndex["AWS Infrastructure"] {
			t.Errorf("Cluster Identity should come before AWS Infrastructure")
		}
		if categoryIndex["AWS Infrastructure"] > categoryIndex["Developer-Only"] {
			t.Errorf("AWS Infrastructure should come before Developer-Only")
		}
	})

	t.Run("When flags have unknown category it should be grouped under Other", func(t *testing.T) {
		flags := []FlagInfo{
			{Name: "unknown-flag", Category: "Other"},
		}

		categories := groupByCategory(flags)

		found := false
		for _, cat := range categories {
			if cat.Name == "Other" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected 'Other' category to be present")
		}
	})
}

func TestFindSubcommand(t *testing.T) {
	t.Run("When subcommand exists it should return it", func(t *testing.T) {
		parent := &cobra.Command{Use: "parent"}
		child := &cobra.Command{Use: "aws"}
		parent.AddCommand(child)

		found := findSubcommand(parent, "aws")
		if found == nil {
			t.Errorf("expected to find 'aws' subcommand")
		}
		if found != child {
			t.Errorf("expected to find the correct child command")
		}
	})

	t.Run("When subcommand does not exist it should return nil", func(t *testing.T) {
		parent := &cobra.Command{Use: "parent"}
		child := &cobra.Command{Use: "aws"}
		parent.AddCommand(child)

		found := findSubcommand(parent, "azure")
		if found != nil {
			t.Errorf("expected nil for non-existent subcommand")
		}
	})
}
