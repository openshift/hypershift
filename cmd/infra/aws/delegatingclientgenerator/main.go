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
				switch input {
				case "route53":
					return "Route53"
				default:
					return strings.ToUpper(input)
				}
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
		}).Parse(`package aws

import (
	"fmt"

    awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
{{- range $service := .Services }}
	"github.com/aws/aws-sdk-go/service/{{$service}}"
	"github.com/aws/aws-sdk-go/service/{{$service}}/{{$service}}iface"
{{- end}}
)

// NewDelegatingClient creates a new set of AWS service clients that delegate individual calls to the right credentials.
func NewDelegatingClient (
{{- range $name := $.Delegates }}
	{{$name | ToName}}CredentialsFile string,
{{- end}}
) (*DelegatingClient, error) {
	awsConfig := awsutil.NewConfig()
{{- range $name := $.Delegates }}
	{{$name | ToName}}Session, err := session.NewSessionWithOptions(session.Options{SharedConfigFiles: []string{ {{- $name | ToName}}CredentialsFile}})
	if err != nil {
		return nil, fmt.Errorf("error creating new AWS session for {{$name | ToName}}: %w", err)
	}
	{{$name | ToName}}Session.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/hypershift",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io hypershift", "{{$name}}"),
	})
	{{$name | ToName}} := &{{$name | ToName}}ClientDelegate{
{{- with $services := $name | index $.DelegatesByName }}
{{- range $service, $apis := $services }}
		{{$service}}Client: {{$service}}.New({{$name | ToName}}Session, awsConfig),
{{- end}}
{{- end}}
	}
{{- end}}
	return &DelegatingClient{
{{- range $service := .Services }}
		{{$service | ToIfaceName}}API: &{{$service}}Client{
{{- with $delegates := $service | index $.DelegatesByService }}
			{{$service | ToIfaceName}}API: nil,
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
	{{$service}}Client {{$service}}iface.{{$service | ToIfaceName}}API
{{- end}}
}
{{- end}}
{{ end}}

// DelegatingClient embeds clients for AWS services we have privileges to use with guest cluster component roles.
type DelegatingClient struct {
{{- range $service := .Services }}
	{{$service}}iface.{{$service | ToIfaceName}}API
{{- end}}
}

{{ range $service := .Services }}
{{- with $delegates := $service | index $.DelegatesByService }}
// {{$service}}Client delegates to individual component clients for API calls we know those components will have privileges to make.
type {{$service}}Client struct {
	// embedding this fulfills the interface and falls back to a panic for APIs we don't have privileges for
	{{$service}}iface.{{$service | ToIfaceName}}API
{{ range $name, $apis := $delegates }}
	{{$name | ToName}} *{{$name | ToName}}ClientDelegate
{{- end}}
}

{{- range $name := $.Delegates }}
{{- with $apis := $name | index $delegates }}
{{ range $api := $apis }}
func (c *{{$service}}Client) {{$api}}WithContext(ctx aws.Context, input *{{$service}}.{{$api}}Input, opts ...request.Option) (*{{Output $service $api}}, error) {
	return c.{{$name | ToName}}.{{$service}}Client.{{$api}}WithContext(ctx, input, opts...)
}
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
			"DescribeTargetHealth",
			"ModifyListener",
			"ModifyTargetGroup",
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
	apiMapping := map[string]map[string]string{
		"s3": {
			"ListBucket":                 "ListBuckets",
			"ListBucketMultipartUploads": "ListMultipartUploads",
			"GetLifecycleConfiguration":  "GetBucketLifecycleConfiguration",
			"GetEncryptionConfiguration": "GetBucketEncryption",
			"PutLifecycleConfiguration":  "PutBucketLifecycleConfiguration",
			"ListMultipartUploadParts":   "ListMultipartUploads",
			"PutBucketPublicAccessBlock": "PutPublicAccessBlock",
			"GetBucketPublicAccessBlock": "GetPublicAccessBlock",
			"PutEncryptionConfiguration": "PutBucketEncryption",
		},
	}

	updated := aws.ServicesByDelegate{}
	for name, services := range delegates {
		updated[name] = aws.EndpointsByService{}
		for service, apis := range services {
			var mapped []string
			for _, api := range apis {
				if mapping, remapped := apiMapping[service][api]; remapped {
					mapped = append(mapped, mapping)
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
