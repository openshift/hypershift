package main

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"text/template"
	"unicode"

	"github.com/openshift/hypershift/cmd/infra/aws"

	"k8s.io/apimachinery/pkg/util/sets"
)

// main generates a Go source file containing a client that delegates various AWS service API calls
// to the correct set of credentials. In order to make sure that we have a deterministic output for
// the generator, we need to use a sorted order for traversing services and delegates.
func main() {
	delegates, err := aws.APIsByDelegatedServices()
	if err != nil {
		panic(err)
	}

	delegates = adjustServices(delegates)
	delegates = adjustAPIs(delegates)

	// we need a deterministic order for services and delegates to generate stable output
	orderedDelegateNames := delegateNames(delegates)

	// most of the access we need to do in the template will be by service, not by delegate
	// service -> name -> apis
	delegatesByService := byService(delegates)

	// we need to deduplicate which APIs are provided by which delegate for each service
	delegatesByService = deduplicateAPIs(delegatesByService, orderedDelegateNames)

	// now that we've pruned things, we need to update all of our mappings and orders
	delegates = byDelegate(delegatesByService)
	orderedServiceNames := serviceNames(delegates)
	orderedDelegateNames = delegateNames(delegates)

	client, err := template.New("client").Funcs(
		template.FuncMap{
			"ToIfaceName": func(input string) string {
				return strings.ToUpper(input)
			},
			"ToName": func(input string) string { // snake-case to camelCase
				output := strings.Builder{}
				var capitalize bool
				for _, r := range input {
					if r == '-' {
						capitalize = true
					} else {
						if capitalize {
							output.WriteRune(unicode.ToUpper(r))
							capitalize = false
						} else {
							output.WriteRune(r)
						}
					}
				}
				return output.String()
			},
			"Output": func(group, api string) string {
				outputType := api + "Output"
				switch group {
				case "ec2":
					switch api {
					case "AttachVolume":
						outputType = "VolumeAttachment"
					case "DetachVolume":
						outputType = "VolumeAttachment"
					case "CreateVolume":
						outputType = "Volume"
					case "CreateSnapshot":
						outputType = "Snapshot"
					case "RunInstances":
						outputType = "Reservation"
					}
				}
				return group + "." + outputType
			},
			"IsV2Service": isV2Service,
		}).Parse(`package aws

import (
	"context"
	"fmt"

    awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	configv2 "github.com/aws/aws-sdk-go-v2/config"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/smithy-go/middleware"
{{- range $service := .Services }}
	{{- if IsV2Service $service }}
	{{$service}}v2 "github.com/aws/aws-sdk-go-v2/service/{{$service}}"
	{{- else }}
	"github.com/aws/aws-sdk-go/service/{{$service}}"
	"github.com/aws/aws-sdk-go/service/{{$service}}/{{$service}}iface"
	{{- end }}
{{- end}}
)

// NewDelegatingClient creates a new set of AWS service clients that delegate individual calls to the right credentials.
func NewDelegatingClient (
	ctx context.Context,
{{- range $name := $.Delegates }}
	{{$name | ToName}}CredentialsFile string,
{{- end}}
) (*DelegatingClient, error) {
	awsConfig := awsutil.NewConfig()
	awsConfigv2 := awsutil.NewConfigV2()
{{- range $name := $.Delegates }}
	{{- with $services := $name | index $.DelegatesByName }}
	{{- $hasV1 := false }}
	{{- $hasV2 := false }}
	{{- range $service, $apis := $services }}
		{{- if IsV2Service $service }}
			{{- $hasV2 = true }}
		{{- else }}
			{{- $hasV1 = true }}
		{{- end }}
	{{- end }}
	{{- if $hasV1 }}
	{{$name | ToName}}Session, err := session.NewSessionWithOptions(session.Options{SharedConfigFiles: []string{ {{- $name | ToName}}CredentialsFile}})
	if err != nil {
		return nil, fmt.Errorf("error creating new AWS session for {{$name | ToName}}: %w", err)
	}
	{{$name | ToName}}Session.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/hypershift",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io hypershift", "{{$name}}"),
	})
	{{- end }}
	{{- if $hasV2 }}
	{{$name | ToName}}Cfg, err := configv2.LoadDefaultConfig(ctx,
		configv2.WithSharedConfigFiles([]string{ {{- $name | ToName}}CredentialsFile}),
		configv2.WithAPIOptions([]func(*middleware.Stack) error{
			awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", "{{$name}}"),
		}))
	if err != nil {
		return nil, fmt.Errorf("error loading AWS config for {{$name | ToName}}: %w", err)
	}
	{{- end }}
	{{$name | ToName}} := &{{$name | ToName}}ClientDelegate{
{{- range $service, $apis := $services }}
		{{- if IsV2Service $service }}
		{{$service}}Client: {{$service}}v2.NewFromConfig({{$name | ToName}}Cfg, func(o *{{$service}}v2.Options) {
			o.Retryer = awsConfigv2()
		}),
		{{- else }}
		{{$service}}Client: {{$service}}.New({{$name | ToName}}Session, awsConfig),
		{{- end }}
{{- end}}
	}
	{{- end }}
{{- end}}
	return &DelegatingClient{
{{- range $service := .Services }}
		{{- if IsV2Service $service }}
		{{$service | ToIfaceName}}Client: &{{$service}}Client{
			{{$service | ToIfaceName}}API: nil,
		{{- else }}
		{{$service | ToIfaceName}}API: &{{$service}}Client{
			{{$service | ToIfaceName}}API: nil,
		{{- end }}
{{- with $delegates := $service | index $.DelegatesByService }}
{{- range $name, $apis := $delegates }}
			{{$name | ToName}}: {{$name | ToName}},
{{- end}}
{{- end}}
		},
{{- end}}
	}, nil
}

{{- range $name := .Delegates }}
{{- with $services := $name | index $.DelegatesByName }}
type {{$name | ToName}}ClientDelegate struct {
{{- range $service, $apis := $services }}
	{{- if IsV2Service $service }}
	{{$service}}Client awsapi.{{$service | ToIfaceName}}API
	{{- else }}
	{{$service}}Client {{$service}}iface.{{$service | ToIfaceName}}API
	{{- end }}
{{- end}}
}
{{- end}}
{{ end}}

// DelegatingClient embeds clients for AWS services we have privileges to use with guest cluster component roles.
type DelegatingClient struct {
{{- range $service := .Services }}
	{{- if IsV2Service $service }}
	{{$service | ToIfaceName}}Client awsapi.{{$service | ToIfaceName}}API
	{{- else }}
	{{$service}}iface.{{$service | ToIfaceName}}API
	{{- end }}
{{- end}}
}

{{ range $service := .Services }}
{{- with $delegates := $service | index $.DelegatesByService }}
// {{$service}}Client delegates to individual component clients for API calls we know those components will have privileges to make.
type {{$service}}Client struct {
	// embedding this fulfills the interface and falls back to a panic for APIs we don't have privileges for
	{{- if IsV2Service $service }}
	awsapi.{{$service | ToIfaceName}}API
	{{- else }}
	{{$service}}iface.{{$service | ToIfaceName}}API
	{{- end }}
{{ range $name, $apis := $delegates }}
	{{$name | ToName}} *{{$name | ToName}}ClientDelegate
{{- end}}
}

{{- range $name := $.Delegates }}
{{- with $apis := $name | index $delegates }}
{{ range $api := $apis }}
{{- if IsV2Service $service }}
func (c *{{$service}}Client) {{$api}}(ctx context.Context, input *{{$service}}v2.{{$api}}Input, optFns ...func(*{{$service}}v2.Options)) (*{{$service}}v2.{{$api}}Output, error) {
	return c.{{$name | ToName}}.{{$service}}Client.{{$api}}(ctx, input, optFns...)
}
{{- else }}
func (c *{{$service}}Client) {{$api}}WithContext(ctx aws.Context, input *{{$service}}.{{$api}}Input, opts ...request.Option) (*{{Output $service $api}}, error) {
	return c.{{$name | ToName}}.{{$service}}Client.{{$api}}WithContext(ctx, input, opts...)
}
{{- end }}
{{- end}}
{{- end}}
{{- end}}
{{ end}}
{{- end}}
	`)
	if err != nil {
		panic(fmt.Errorf("unable to parse client template: %w", err))
	}

	out := bytes.Buffer{}
	if err := client.Execute(&out, struct {
		Services           []string
		Delegates          []string
		DelegatesByName    aws.ServicesByDelegate
		DelegatesByService DelegatesByService
	}{
		Services:           orderedServiceNames,
		Delegates:          orderedDelegateNames,
		DelegatesByName:    delegates,
		DelegatesByService: delegatesByService,
	}); err != nil {
		panic(fmt.Errorf("unable to execute delegate client template: %w", err))
	}

	if _, err := fmt.Fprintln(os.Stdout, out.String()); err != nil {
		panic(fmt.Errorf("unable to write delegate client template: %w", err))
	}

	// Generate interface definitions for v2 services
	if err := generateV2Interfaces(delegatesByService); err != nil {
		panic(fmt.Errorf("unable to generate v2 interfaces: %w", err))
	}
}

type EndpointsByDelegate map[string][]string
type DelegatesByService map[string]EndpointsByDelegate

// byService remaps from {delegate name -> service -> APIs} to {service -> delegate name -> APIs}
func byService(delegates aws.ServicesByDelegate) DelegatesByService {
	delegatesByService := DelegatesByService{}
	for name, delegate := range delegates {
		for service, apis := range delegate {
			if _, ok := delegatesByService[service]; !ok {
				delegatesByService[service] = EndpointsByDelegate{}
			}
			delegatesByService[service][name] = apis
		}
	}
	return delegatesByService
}

// byDelegate remaps from {service -> delegate name -> APIs} to {delegate name -> service -> APIs}
func byDelegate(delegatesByService DelegatesByService) aws.ServicesByDelegate {
	allDelegates := aws.ServicesByDelegate{}
	for service, delegates := range delegatesByService {
		for name, apis := range delegates {
			if _, ok := allDelegates[name]; !ok {
				allDelegates[name] = aws.EndpointsByService{}
			}
			allDelegates[name][service] = apis
		}
	}
	return allDelegates
}

// serviceNames returns a sorted list of service names from a map of services by delegate
func serviceNames(delegates aws.ServicesByDelegate) []string {
	allServices := sets.New[string]()
	for _, services := range delegates {
		for service := range services {
			allServices.Insert(service)
		}
	}
	services := allServices.UnsortedList()
	sort.Strings(services)
	return services
}

// delegateNames returns a sorted list of delegate names from a map of services by delegate
func delegateNames(delegates aws.ServicesByDelegate) []string {
	allDelegates := sets.New[string]()
	for name := range delegates {
		allDelegates.Insert(name)
	}
	delegateNameSlice := allDelegates.UnsortedList()
	sort.Strings(delegateNameSlice)
	return delegateNameSlice
}

// v1Services lists services still using AWS SDK v1.
// As services are migrated to v2, remove them from this list.
// When this list is empty, all services have been migrated and this logic can be removed.
var v1Services = []string{
	"ec2",
	"elb",
	"elbv2",
	"sqs",
}

// isV2Service returns true if the service uses AWS SDK v2.
// Services not in the v1Services list are assumed to use v2 (default for new services).
func isV2Service(service string) bool {
	for _, v1 := range v1Services {
		if service == v1 {
			return false
		}
	}
	return true
}

// adjustServices maps permission attestation names to the Go SDK names, as necessary, and
// ignores services we do not care about.
func adjustServices(delegates aws.ServicesByDelegate) aws.ServicesByDelegate {
	// some services are named differently in the Go SDK
	serviceOverrides := map[string][]string{
		"elasticloadbalancing": {"elb", "elbv2"},
	}
	// some services we just don't care about
	serviceIgnores := sets.New[string]("kms", "autoscaling", "iam", "tag")

	adjusted := aws.ServicesByDelegate{}
	for name, delegate := range delegates {
		updated := aws.EndpointsByService{}
		for service, apis := range delegate {
			if serviceIgnores.Has(service) {
				continue
			}
			overrides, overridden := serviceOverrides[service]
			if overridden {
				for _, override := range overrides {
					updated[override] = apis
				}
			} else {
				updated[service] = apis
			}
		}
		if len(updated) > 0 {
			adjusted[name] = updated
		}
	}
	return adjusted
}

// adjustAPIs maps APIs into Go SDK names for the cases where the two names differ and handles the ELB duality
func adjustAPIs(delegates aws.ServicesByDelegate) aws.ServicesByDelegate {
	// some APIs exist for the ELBv1 or v2 group, but not both, and the privilege attestations do not
	// distinguish between them
	apiRemovals := map[string]sets.Set[string]{
		"elb": sets.New(
			"RegisterTargets",
			"CreateListener",
			"CreateTargetGroup",
			"DeleteListener",
			"DeleteTargetGroup",
			"DeregisterTargets",
			"DescribeListeners",
			"DescribeTargetGroups",
			"DescribeTargetGroupAttributes",
			"DescribeTargetHealth",
			"ModifyListener",
			"ModifyTargetGroup",
			"ModifyTargetGroupAttributes",
		),
		"elbv2": sets.New(
			"ApplySecurityGroupsToLoadBalancer",
			"AttachLoadBalancerToSubnets",
			"ConfigureHealthCheck",
			"CreateLoadBalancerListeners",
			"CreateLoadBalancerPolicy",
			"DeleteLoadBalancerListeners",
			"DeregisterInstancesFromLoadBalancer",
			"DescribeLoadBalancerPolicies",
			"DetachLoadBalancerFromSubnets",
			"RegisterInstancesWithLoadBalancer",
			"SetLoadBalancerPoliciesForBackendServer",
			"SetLoadBalancerPoliciesOfListener",
		),
	}

	// most but not all the Go SDK APIs match the privilege attestations
	// Note: Some IAM permissions cover multiple SDK API calls
	apiMapping := map[string]map[string][]string{
		"s3": {
			"ListBucket":                 {"ListBuckets", "ListObjectsV2"},  // s3:ListBucket covers multiple list APIs
			"DeleteObject":               {"DeleteObject", "DeleteObjects"}, // s3:DeleteObject covers both single and batch delete
			"ListBucketMultipartUploads": {"ListMultipartUploads"},
			"GetLifecycleConfiguration":  {"GetBucketLifecycleConfiguration"},
			"GetEncryptionConfiguration": {"GetBucketEncryption"},
			"PutLifecycleConfiguration":  {"PutBucketLifecycleConfiguration"},
			"ListMultipartUploadParts":   {"ListMultipartUploads"},
			"PutBucketPublicAccessBlock": {"PutPublicAccessBlock"},
			"GetBucketPublicAccessBlock": {"GetPublicAccessBlock"},
			"PutEncryptionConfiguration": {"PutBucketEncryption"},
		},
	}

	updated := aws.ServicesByDelegate{}
	for name, services := range delegates {
		updated[name] = aws.EndpointsByService{}
		for service, apis := range services {
			var mapped []string
			for _, api := range apis {
				if mappings, remapped := apiMapping[service][api]; remapped {
					mapped = append(mapped, mappings...)
				} else if removals, ok := apiRemovals[service]; !ok || !removals.Has(api) {
					mapped = append(mapped, api)
				}
			}
			slices.Sort(mapped)
			updated[name][service] = mapped
		}
	}

	return updated
}

// deduplicateAPIs removes duplicate APIs from the delegates for a service, preferring to use the first
// delegate by name order, as which delegate fulfills an API does not matter
func deduplicateAPIs(delegatesByService DelegatesByService, delegateNames []string) DelegatesByService {
	updated := DelegatesByService{}
	for service, delegates := range delegatesByService {
		updated[service] = EndpointsByDelegate{}
		apis := sets.New[string]()
		for _, delegateName := range delegateNames {
			if delegateApis, ok := delegates[delegateName]; ok {
				prunedApis := sets.New(delegateApis...).Difference(apis).UnsortedList()
				slices.Sort(prunedApis)
				if len(prunedApis) > 0 {
					updated[service][delegateName] = prunedApis
					apis.Insert(prunedApis...)
				}
			}
		}
	}
	return updated
}

// extendedAPIs defines additional SDK methods used by CLI tools with full credentials.
// These methods are NOT in the IAM delegation policy and will NOT be delegated.
// They are merged into the base API interface so callers use a single interface type.
// The delegating client embeds a nil interface for these methods (panic on call).
var extendedAPIs = map[string][]string{
	"route53": {
		"AssociateVPCWithHostedZone",
		"CreateHostedZone",
		"CreateVPCAssociationAuthorization",
		"DeleteHostedZone",
		"DisassociateVPCFromHostedZone",
		"GetHostedZone",
		"ListHostedZonesByVPC",
	},
}

// standaloneV2APIs defines v2 service interfaces that are NOT part of the delegating client
// (i.e. the service is in serviceIgnores) but still need a generated interface file in
// support/awsapi/ for use by CLI tools and tests.
var standaloneV2APIs = map[string][]string{
	"iam": {
		"AddRoleToInstanceProfile",
		"AttachRolePolicy",
		"CreateInstanceProfile",
		"CreateOpenIDConnectProvider",
		"CreateRole",
		"DeleteInstanceProfile",
		"DeleteOpenIDConnectProvider",
		"DeleteRole",
		"DeleteRolePolicy",
		"DetachRolePolicy",
		"GetInstanceProfile",
		"GetRole",
		"GetRolePolicy",
		"ListAttachedRolePolicies",
		"ListOpenIDConnectProviders",
		"ListRolePolicies",
		"PutRolePolicy",
		"RemoveRoleFromInstanceProfile",
	},
}

// generateV2Interfaces generates interface definitions for AWS SDK v2 services
func generateV2Interfaces(delegatesByService DelegatesByService) error {
	for service := range delegatesByService {
		if !isV2Service(service) {
			continue
		}

		// Collect all APIs for this service
		allAPIs := sets.New[string]()
		for _, apis := range delegatesByService[service] {
			allAPIs.Insert(apis...)
		}
		sortedAPIs := allAPIs.UnsortedList()
		slices.Sort(sortedAPIs)

		// Generate interface file
		if err := generateInterfaceFile(service, sortedAPIs, extendedAPIs[service]); err != nil {
			return fmt.Errorf("failed to generate interface for %s: %w", service, err)
		}
	}

	// Generate interface files for standalone v2 services (not in delegating client)
	for service, apis := range standaloneV2APIs {
		sortedAPIs := append([]string{}, apis...)
		slices.Sort(sortedAPIs)
		if err := generateInterfaceFile(service, nil, sortedAPIs); err != nil {
			return fmt.Errorf("failed to generate standalone interface for %s: %w", service, err)
		}
	}

	return nil
}

// generateInterfaceFile generates a single interface file for a v2 service.
// Extended APIs (from extendedAPIs map) are merged into the base interface,
// matching v1's pattern where route53iface.Route53API contained all methods.
func generateInterfaceFile(service string, apis []string, extended []string) error {
	// Merge extended APIs into the main list for a single interface
	allAPIs := append(append([]string{}, apis...), extended...)
	slices.Sort(allAPIs)

	interfaceTemplate := `// Code generated by delegatingclientgenerator. DO NOT EDIT.

package awsapi

//` + `go:generate ../../hack/tools/bin/mockgen -source={{.Service}}.go -package=awsapi -destination={{.Service}}_mock.go

import (
	"context"

	{{.Service}}v2 "github.com/aws/aws-sdk-go-v2/service/{{.Service}}"
)

{{- if .HasExtended }}

// {{.Service | ToUpper}}API defines the {{.Service | ToUpper}} operations used by HyperShift.
// Generated from: cmd/infra/aws/iam.go + delegatingclientgenerator extendedAPIs
//
// This interface includes methods from IAM delegation policies as well as additional
// methods used by CLI tools with full credentials. Only the delegated subset is
// implemented by the delegating client; non-delegated methods will panic if called
// through the delegating path (matching v1 behavior).
{{- else }}

// {{.Service | ToUpper}}API defines the {{.Service | ToUpper}} operations allowed by IAM policies.
// Generated from: cmd/infra/aws/iam.go
//
// This interface includes all methods corresponding to IAM permissions granted to HyperShift.
// Some IAM permissions map to multiple SDK methods (e.g., s3:ListBucket covers both
// ListBuckets and ListObjectsV2). See delegatingclientgenerator for mapping rules.
{{- end }}
type {{.Service | ToUpper}}API interface {
{{- range .APIs }}
	{{.}}(ctx context.Context, input *{{$.Service}}v2.{{.}}Input, optFns ...func(*{{$.Service}}v2.Options)) (*{{$.Service}}v2.{{.}}Output, error)
{{- end }}
}

// Ensure *{{.Service}}v2.Client implements {{.Service | ToUpper}}API
var _ {{.Service | ToUpper}}API = (*{{.Service}}v2.Client)(nil)
`

	tmpl, err := template.New("interface").Funcs(template.FuncMap{
		"ToUpper": strings.ToUpper,
	}).Parse(interfaceTemplate)
	if err != nil {
		return fmt.Errorf("unable to parse interface template: %w", err)
	}

	out := bytes.Buffer{}
	if err := tmpl.Execute(&out, struct {
		Service     string
		APIs        []string
		HasExtended bool
	}{
		Service:     service,
		APIs:        allAPIs,
		HasExtended: len(extended) > 0,
	}); err != nil {
		return fmt.Errorf("unable to execute interface template: %w", err)
	}

	// Write to support/awsapi/{service}.go
	filename := fmt.Sprintf("support/awsapi/%s.go", service)
	if err := os.WriteFile(filename, out.Bytes(), 0644); err != nil {
		return fmt.Errorf("unable to write interface file %s: %w", filename, err)
	}

	// Note: Don't print to stderr here as it interferes with stdout redirection
	return nil
}
