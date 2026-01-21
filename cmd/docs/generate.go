package docs

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/template"

	clustercmd "github.com/openshift/hypershift/cmd/cluster"
	productclustercmd "github.com/openshift/hypershift/product-cli/cmd/cluster"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// GenerateOptions contains options for the generate command
type GenerateOptions struct {
	OutputDir    string
	Platform     string
	Command      string
	TemplatePath string
}

// NewGenerateCommand creates the docs generate command
func NewGenerateCommand() *cobra.Command {
	opts := &GenerateOptions{
		OutputDir: "docs/content/reference/cli-flags",
		Platform:  "aws",
		Command:   "create-cluster",
	}

	cmd := &cobra.Command{
		Use:          "generate",
		Short:        "Generate CLI flag documentation",
		Long:         "Generate markdown documentation for CLI flags, comparing hcp and hypershift CLIs.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGenerate(opts)
		},
	}

	cmd.Flags().StringVarP(&opts.OutputDir, "output", "o", opts.OutputDir, "Output directory for generated docs")
	cmd.Flags().StringVar(&opts.Platform, "platform", opts.Platform, "Platform to generate docs for (aws)")
	cmd.Flags().StringVar(&opts.Command, "command", opts.Command, "Command to generate docs for (create-cluster)")
	cmd.Flags().StringVar(&opts.TemplatePath, "template", "", "Path to custom template file (uses embedded default if not set)")

	return cmd
}

func runGenerate(opts *GenerateOptions) error {
	if opts.Platform != "aws" {
		return fmt.Errorf("platform %q not yet supported, only 'aws' is available", opts.Platform)
	}

	if opts.Command != "create-cluster" {
		return fmt.Errorf("command %q not yet supported, only 'create-cluster' is available", opts.Command)
	}

	// Extract flags from both CLIs
	data, err := extractAWSCreateClusterFlags()
	if err != nil {
		return fmt.Errorf("failed to extract flags: %w", err)
	}

	// Load and render template
	content, err := renderTemplate(opts, data)
	if err != nil {
		return fmt.Errorf("failed to render template: %w", err)
	}

	// Write output file
	outputPath := filepath.Join(opts.OutputDir, opts.Platform+".md")
	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}

	fmt.Printf("Generated %s\n", outputPath)
	return nil
}

func extractAWSCreateClusterFlags() (*TemplateData, error) {
	// Use the actual command construction to get proper flag annotations
	// This ensures the doc generator uses the same source of truth as the CLIs

	// hcp CLI (product) - use actual NewCreateCommands()
	hcpClusterCmd := productclustercmd.NewCreateCommands()
	hcpAWSCmd := findSubcommand(hcpClusterCmd, "aws")
	if hcpAWSCmd == nil {
		return nil, fmt.Errorf("aws subcommand not found in hcp CLI")
	}

	// hypershift CLI (developer) - use actual NewCreateCommands()
	hypershiftClusterCmd := clustercmd.NewCreateCommands()
	hypershiftAWSCmd := findSubcommand(hypershiftClusterCmd, "aws")
	if hypershiftAWSCmd == nil {
		return nil, fmt.Errorf("aws subcommand not found in hypershift CLI")
	}

	// Extract flag info from both commands (persistent + local flags)
	hcpFlagMap := extractFlagMapFromCommand(hcpAWSCmd)
	hypershiftFlagMap := extractFlagMapFromCommand(hypershiftAWSCmd)

	// Merge flags
	allFlags := mergeFlags(hcpFlagMap, hypershiftFlagMap)

	// Group by category
	categories := groupByCategory(allFlags)

	// Calculate counts
	sharedCount := 0
	devOnlyCount := 0
	for _, flag := range allFlags {
		if flag.InHcp && flag.InHypershift {
			sharedCount++
		} else if flag.InHypershift && !flag.InHcp {
			devOnlyCount++
		}
	}

	return &TemplateData{
		Platform:        "aws",
		Command:         "create-cluster",
		Categories:      categories,
		SharedCount:     sharedCount,
		DevOnlyCount:    devOnlyCount,
		HcpTotal:        sharedCount,
		HypershiftTotal: sharedCount + devOnlyCount,
	}, nil
}

// findSubcommand finds a subcommand by name
func findSubcommand(parent *cobra.Command, name string) *cobra.Command {
	for _, cmd := range parent.Commands() {
		if cmd.Use == name || cmd.Name() == name {
			return cmd
		}
	}
	return nil
}

// BashCompOneRequiredFlag is the annotation key used by cobra for required flags
const BashCompOneRequiredFlag = "cobra_annotation_bash_completion_one_required_flag"

// extractFlagMapFromCommand extracts flags from a cobra command by walking the command tree.
// This ensures we access the original flag objects where annotations are stored,
// rather than relying on cobra's merged views which may contain copies.
func extractFlagMapFromCommand(cmd *cobra.Command) map[string]*FlagInfo {
	result := make(map[string]*FlagInfo)

	// 1. This command's local flags (non-persistent)
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		addFlagToMap(result, f)
	})

	// 2. This command's persistent flags
	cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if _, exists := result[f.Name]; !exists {
			addFlagToMap(result, f)
		}
	})

	// 3. Walk up parent chain for inherited persistent flags
	// This accesses the original flag objects where MarkPersistentFlagRequired sets annotations
	for p := cmd.Parent(); p != nil; p = p.Parent() {
		p.PersistentFlags().VisitAll(func(f *pflag.Flag) {
			if _, exists := result[f.Name]; !exists {
				addFlagToMap(result, f)
			}
		})
	}

	return result
}

func addFlagToMap(result map[string]*FlagInfo, f *pflag.Flag) {
	// Check if flag is marked as required via MarkFlagRequired
	isRequired := false
	if annotations, ok := f.Annotations[BashCompOneRequiredFlag]; ok {
		isRequired = len(annotations) > 0 && annotations[0] == "true"
	}

	// Store required status separately - category will be determined during merge
	result[f.Name] = &FlagInfo{
		Name:                 f.Name,
		Type:                 f.Value.Type(),
		Default:              cleanDefault(f.DefValue),
		Description:          f.Usage,
		Category:             GetCategory(f.Name), // Use normal category, Required handled during merge
		RequiredInHcp:        isRequired,          // Will be used as source for hcp flags
		RequiredInHypershift: isRequired,          // Will be used as source for hypershift flags
	}
}

// cleanDefault removes noisy default values like empty arrays/maps
func cleanDefault(defValue string) string {
	switch defValue {
	case "[]", "map[]", "0s":
		return ""
	}
	return defValue
}

func mergeFlags(hcpFlags, hypershiftFlags map[string]*FlagInfo) []FlagInfo {
	merged := make(map[string]*FlagInfo)

	// Add hcp flags - track RequiredInHcp from hcp's required status
	for name, flag := range hcpFlags {
		f := *flag
		f.InHcp = true
		// RequiredInHcp was set by addFlagToMap based on hcp CLI annotations
		// Clear RequiredInHypershift - will be set from hypershift flags
		f.RequiredInHypershift = false
		merged[name] = &f
	}

	// Add/merge hypershift flags - track RequiredInHypershift from hypershift's required status
	for name, flag := range hypershiftFlags {
		if existing, ok := merged[name]; ok {
			existing.InHypershift = true
			// RequiredInHypershift comes from hypershift CLI's annotation
			existing.RequiredInHypershift = flag.RequiredInHcp // addFlagToMap sets both fields the same
		} else {
			f := *flag
			f.InHypershift = true
			// This flag is only in hypershift, so RequiredInHcp should be false
			f.RequiredInHypershift = f.RequiredInHcp // Move to correct field
			f.RequiredInHcp = false
			merged[name] = &f
		}
	}

	// Convert to slice and set category to "Required" if required in either CLI
	result := make([]FlagInfo, 0, len(merged))
	for _, flag := range merged {
		// If required in either CLI, put in Required category
		if flag.RequiredInHcp || flag.RequiredInHypershift {
			flag.Category = "Required"
		}
		result = append(result, *flag)
	}

	// Sort by name for consistent output
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result
}

func groupByCategory(flags []FlagInfo) []CategoryInfo {
	// Group flags by category
	categoryMap := make(map[string][]FlagInfo)
	for _, flag := range flags {
		categoryMap[flag.Category] = append(categoryMap[flag.Category], flag)
	}

	// Create ordered categories
	var categories []CategoryInfo
	for _, catName := range CategoryOrder {
		if flags, ok := categoryMap[catName]; ok {
			// Sort flags within category by name
			sort.Slice(flags, func(i, j int) bool {
				return flags[i].Name < flags[j].Name
			})
			categories = append(categories, CategoryInfo{
				Name:  catName,
				Flags: flags,
			})
		}
	}

	// Add any uncategorized flags as "Other"
	if flags, ok := categoryMap["Other"]; ok && len(flags) > 0 {
		sort.Slice(flags, func(i, j int) bool {
			return flags[i].Name < flags[j].Name
		})
		categories = append(categories, CategoryInfo{
			Name:  "Other",
			Flags: flags,
		})
	}

	return categories
}

func loadTemplate(customPath, defaultName string) (*template.Template, error) {
	if customPath != "" {
		return template.ParseFiles(customPath)
	}
	return template.ParseFS(templateFS, "templates/"+defaultName)
}

func renderTemplate(opts *GenerateOptions, data *TemplateData) (string, error) {
	tmpl, err := loadTemplate(opts.TemplatePath, opts.Platform+".md.tmpl")
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}
