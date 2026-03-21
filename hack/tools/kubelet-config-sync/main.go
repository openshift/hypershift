package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"text/template"
)

// excludedFields lists upstream KubeletConfiguration fields that should NOT be
// exposed in the HyperShift API. Each entry includes a reason comment.
var excludedFields = map[string]string{
	// Embedded TypeMeta
	"TypeMeta": "embedded type metadata",

	// Server / networking
	"EnableServer":        "server config, not user-facing",
	"StaticPodPath":       "static pod management",
	"PodLogsDir":          "pod log directory",
	"SyncFrequency":       "internal sync frequency",
	"FileCheckFrequency":  "file check frequency",
	"HTTPCheckFrequency":  "http check frequency",
	"StaticPodURL":        "static pod URL",
	"StaticPodURLHeader":  "static pod URL header",
	"Address":             "kubelet bind address",
	"Port":                "kubelet port",
	"ReadOnlyPort":        "read-only port",

	// TLS
	"TLSCertFile":        "TLS cert management",
	"TLSPrivateKeyFile":  "TLS private key management",
	"TLSCipherSuites":    "TLS cipher suites",
	"TLSMinVersion":      "TLS min version",
	"RotateCertificates":  "certificate rotation",
	"ServerTLSBootstrap": "server TLS bootstrap",

	// Auth
	"Authentication": "kubelet authentication config",
	"Authorization":  "kubelet authorization config",

	// Debug
	"EnableDebuggingHandlers":    "debug endpoint",
	"EnableContentionProfiling":  "debug profiling",
	"HealthzPort":                "healthz port",
	"HealthzBindAddress":         "healthz bind address",
	"EnableProfilingHandler":     "profiling handler",
	"EnableDebugFlagsHandler":    "debug flags handler",
	"EnableSystemLogHandler":     "system log handler",
	"EnableSystemLogQuery":       "system log query",

	// Provider-managed
	"ClusterDomain":      "cluster domain, system-managed",
	"ClusterDNS":         "cluster DNS, system-managed",
	"ProviderID":         "provider ID, system-managed",
	"RegisterNode":       "node registration",
	"RegisterWithTaints": "node registration taints",

	// Cgroup internals
	"KubeletCgroups":       "cgroup management",
	"SystemCgroups":        "cgroup management",
	"CgroupRoot":           "cgroup root",
	"CgroupsPerQOS":        "cgroup per QOS",
	"CgroupDriver":         "cgroup driver",
	"SystemReservedCgroup": "reserved cgroup",
	"KubeReservedCgroup":   "reserved cgroup",

	// CPU manager
	"CPUManagerPolicy":            "CPU manager policy",
	"CPUManagerPolicyOptions":     "CPU manager policy options",
	"CPUManagerReconcilePeriod":   "CPU manager reconcile period",

	// Memory / Topology manager
	"MemoryManagerPolicy":          "memory manager policy",
	"TopologyManagerPolicy":        "topology manager policy",
	"TopologyManagerScope":         "topology manager scope",
	"TopologyManagerPolicyOptions": "topology manager policy options",

	// Runtime
	"ContainerRuntimeEndpoint": "container runtime endpoint",
	"ImageServiceEndpoint":     "image service endpoint",
	"RuntimeRequestTimeout":    "runtime request timeout",

	// Feature gates
	"FeatureGates": "feature gates",
	"FailCgroupV1": "cgroup v1 failure flag",

	// Internal plumbing
	"ContentType":                               "API content type",
	"ConfigMapAndSecretChangeDetectionStrategy": "configmap/secret detection strategy",
	"Logging":                                   "logging config",
	"Tracing":                                   "tracing config",
	"CrashLoopBackOff":                          "crash loop backoff config",

	// Networking
	"HairpinMode":              "hairpin mode",
	"PodCIDR":                  "pod CIDR",
	"MakeIPTablesUtilChains":   "iptables management",
	"IPTablesMasqueradeBit":    "iptables masquerade bit",
	"IPTablesDropBit":          "iptables drop bit",

	// Node lifecycle
	"NodeLeaseDurationSeconds": "node lease duration",

	// Volume
	"VolumeStatsAggPeriod": "volume stats aggregation",
	"VolumePluginDir":      "volume plugin directory",

	// Swap / Memory
	"MemorySwap":              "memory swap config",
	"MemoryThrottlingFactor":  "memory throttling factor",
	"KernelMemcgNotification": "kernel memcg notification",

	// Shutdown
	"ShutdownGracePeriod":                "shutdown grace period",
	"ShutdownGracePeriodCriticalPods":    "shutdown critical pods",
	"ShutdownGracePeriodByPodPriority":   "shutdown by pod priority",

	// Reserved
	"ReservedMemory":     "reserved memory",
	"ReservedSystemCPUs": "reserved system CPUs",

	// Other
	"RunOnce":                     "run once mode",
	"ShowHiddenMetricsForVersion": "hidden metrics",
	"EnforceNodeAllocatable":      "enforce node allocatable",
}

// typeOverrides maps field names to their desired type string in the generated struct.
// This is used when the upstream type needs to be changed for our API.
var typeOverrides = map[string]string{
	"EvictionSoftGracePeriod": "map[string]metav1.Duration",
}

// fieldInfo represents a parsed field from the upstream KubeletConfiguration.
type fieldInfo struct {
	Name    string
	Type    string
	JSONTag string
	Doc     string
}

func main() {
	upstreamPath := flag.String("upstream", "vendor/k8s.io/kubelet/config/v1beta1/types.go", "path to upstream kubelet types.go")
	karpenterPath := flag.String("karpenter", "vendor/github.com/aws/karpenter-provider-aws/pkg/apis/v1/ec2nodeclass.go", "path to Karpenter ec2nodeclass.go")
	structOut := flag.String("struct-out", "api/karpenter/v1beta1/zz_generated.kubeletconfig.go", "output path for generated struct")
	conversionOut := flag.String("conversion-out", "api/karpenter/v1beta1/zz_generated.kubeletconfig_karpenter.go", "output path for generated conversion function")
	boilerplatePath := flag.String("boilerplate", "hack/boilerplate.go.txt", "path to boilerplate file")
	flag.Parse()

	// 1. Parse upstream kubelet KubeletConfiguration
	upstreamFields, err := parseStruct(*upstreamPath, "KubeletConfiguration")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing upstream kubelet types: %v\n", err)
		os.Exit(1)
	}

	// 2. Parse Karpenter KubeletConfiguration
	karpenterFields, err := parseStruct(*karpenterPath, "KubeletConfiguration")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing Karpenter types: %v\n", err)
		os.Exit(1)
	}
	karpenterFieldNames := make(map[string]bool)
	for _, f := range karpenterFields {
		karpenterFieldNames[f.Name] = true
	}

	// 3. Filter out excluded fields
	var allowedFields []fieldInfo
	for _, f := range upstreamFields {
		if _, excluded := excludedFields[f.Name]; excluded {
			continue
		}
		allowedFields = append(allowedFields, f)
	}

	// 4. Transform types: wrap non-pointer scalars for optional API semantics
	for i := range allowedFields {
		f := &allowedFields[i]
		if override, ok := typeOverrides[f.Name]; ok {
			f.Type = override
			continue
		}
		f.Type = wrapType(f.Type)
	}

	// 5. Read boilerplate
	boilerplate, err := os.ReadFile(*boilerplatePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading boilerplate: %v\n", err)
		os.Exit(1)
	}

	// 6. Determine if metav1 import is needed
	needsMetav1 := false
	for _, f := range allowedFields {
		if strings.Contains(f.Type, "metav1.") {
			needsMetav1 = true
			break
		}
	}

	// 7. Generate struct file
	if err := generateStructFile(*structOut, string(boilerplate), allowedFields, needsMetav1); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating struct file: %v\n", err)
		os.Exit(1)
	}

	// 8. Determine field intersection for conversion
	var intersectionFields []fieldInfo
	for _, f := range allowedFields {
		if karpenterFieldNames[f.Name] {
			intersectionFields = append(intersectionFields, f)
		}
	}

	// 9. Generate conversion function file
	if err := generateConversionFile(*conversionOut, string(boilerplate), intersectionFields); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating conversion file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated %s (%d fields)\n", *structOut, len(allowedFields))
	fmt.Printf("Generated %s (%d intersection fields)\n", *conversionOut, len(intersectionFields))
}

// parseStruct extracts field information from a named struct in the given Go source file.
func parseStruct(path, structName string) ([]fieldInfo, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	var fields []fieldInfo
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != structName {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range structType.Fields.List {
				if len(field.Names) == 0 {
					// Embedded field - use the type name
					typeName := typeString(field.Type)
					// Strip any package prefix for the name
					name := typeName
					if idx := strings.LastIndex(name, "."); idx >= 0 {
						name = name[idx+1:]
					}
					fields = append(fields, fieldInfo{
						Name:    name,
						Type:    typeName,
						JSONTag: tagValue(field.Tag),
						Doc:     fieldDoc(field),
					})
					continue
				}
				for _, name := range field.Names {
					fields = append(fields, fieldInfo{
						Name:    name.Name,
						Type:    typeString(field.Type),
						JSONTag: tagValue(field.Tag),
						Doc:     fieldDoc(field),
					})
				}
			}
		}
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("struct %s not found in %s", structName, path)
	}
	return fields, nil
}

// typeString returns the Go type string for an AST expression.
func typeString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.ArrayType:
		return "[]" + typeString(t.Elt)
	case *ast.MapType:
		return "map[" + typeString(t.Key) + "]" + typeString(t.Value)
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	default:
		return fmt.Sprintf("%T", expr)
	}
}

// tagValue extracts the raw tag string value from a field tag.
func tagValue(tag *ast.BasicLit) string {
	if tag == nil {
		return ""
	}
	// Remove the backticks
	return strings.Trim(tag.Value, "`")
}

// fieldDoc extracts the documentation comment for a struct field.
// It strips out marker comments like +optional, +featureGate, +default,
// since the generated struct adds its own +optional marker.
func fieldDoc(field *ast.Field) string {
	if field.Doc == nil {
		return ""
	}
	var lines []string
	for _, comment := range field.Doc.List {
		text := comment.Text
		trimmed := strings.TrimSpace(strings.TrimPrefix(text, "//"))
		// Skip marker comments
		if strings.HasPrefix(trimmed, "+optional") ||
			strings.HasPrefix(trimmed, "+featureGate") ||
			strings.HasPrefix(trimmed, "+default") ||
			strings.HasPrefix(trimmed, "+ optional") {
			continue
		}
		lines = append(lines, text)
	}
	return strings.Join(lines, "\n")
}

// wrapType transforms non-pointer scalar types to pointer types for optional API semantics.
// Pointers, slices, and maps are left as-is.
func wrapType(t string) string {
	if strings.HasPrefix(t, "*") || strings.HasPrefix(t, "[]") || strings.HasPrefix(t, "map[") {
		return t
	}
	// Wrap scalars and named types
	return "*" + t
}

// jsonFieldName extracts the JSON field name from a struct tag string like `json:"name,omitempty"`.
func jsonFieldName(tag string) string {
	// Find json:"..."
	const prefix = `json:"`
	idx := strings.Index(tag, prefix)
	if idx < 0 {
		return ""
	}
	rest := tag[idx+len(prefix):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	jsonVal := rest[:end]
	// Split on comma for name
	parts := strings.Split(jsonVal, ",")
	return parts[0]
}

func generateStructFile(outPath, boilerplate string, fields []fieldInfo, needsMetav1 bool) error {
	type templateData struct {
		Boilerplate string
		NeedsMetav1 bool
		Fields      []fieldInfo
	}

	tmpl := template.Must(template.New("struct").Funcs(template.FuncMap{
		"jsonTag": func(f fieldInfo) string {
			name := jsonFieldName(f.JSONTag)
			if name == "" {
				return ""
			}
			return fmt.Sprintf("`json:\"%s,omitempty\"`", name)
		},
	}).Parse(structTemplate))

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, templateData{
		Boilerplate: boilerplate,
		NeedsMetav1: needsMetav1,
		Fields:      fields,
	}); err != nil {
		return fmt.Errorf("executing struct template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("formatting generated struct: %w\n\nRaw output:\n%s", err, buf.String())
	}

	return os.WriteFile(outPath, formatted, 0644)
}

func generateConversionFile(outPath, boilerplate string, fields []fieldInfo) error {
	type templateData struct {
		Boilerplate string
		Fields      []fieldInfo
	}

	tmpl := template.Must(template.New("conversion").Parse(conversionTemplate))

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, templateData{
		Boilerplate: boilerplate,
		Fields:      fields,
	}); err != nil {
		return fmt.Errorf("executing conversion template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("formatting generated conversion: %w\n\nRaw output:\n%s", err, buf.String())
	}

	return os.WriteFile(outPath, formatted, 0644)
}

var structTemplate = `// Code generated by kubelet-config-sync. DO NOT EDIT.
{{.Boilerplate}}

package v1beta1
{{if .NeedsMetav1}}
import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)
{{end}}
// KubeletConfiguration configures kubelet settings for nodes provisioned by this NodeClass.
// These settings are injected into the node's ignition configuration via MachineConfig.
// This struct is auto-generated from the upstream KubeletConfiguration.
// +kubebuilder:validation:MinProperties=1
type KubeletConfiguration struct {
{{- range .Fields}}
	{{.Doc}}
	// +optional
	{{.Name}} {{.Type}} {{jsonTag .}}
{{- end}}
}
`

var conversionTemplate = `// Code generated by kubelet-config-sync. DO NOT EDIT.
{{.Boilerplate}}

package v1beta1

import (
	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"
)

// KarpenterKubeletConfiguration converts the HyperShift KubeletConfiguration to
// the upstream Karpenter KubeletConfiguration, mapping only fields that exist
// in both structs. Fields in our struct that are not present in Karpenter's
// struct are silently dropped.
func (spec OpenshiftEC2NodeClassSpec) KarpenterKubeletConfiguration() *awskarpenterv1.KubeletConfiguration {
	if spec.Kubelet == nil {
		return nil
	}
	return &awskarpenterv1.KubeletConfiguration{
{{- range .Fields}}
		{{.Name}}: spec.Kubelet.{{.Name}},
{{- end}}
	}
}
`

