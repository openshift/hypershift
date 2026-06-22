package config

import (
	"fmt"
	"strings"
)

// Config represents the YAML configuration used by the webhook.
//
// Example:
// default: "2"
// overrides:
//   - workloadKind: Deployment
//     workloadName: kube-apiserver
//     containerName: kube-apiserver
//     value: "4"
//
// exclusions:
//   - workloadKind: Deployment
//     workloadName: oauth-openshift
//     containerName: oauth-openshift
type Config struct {
	Default    string     `yaml:"default"`
	Overrides  Overrides  `yaml:"overrides"`
	Exclusions Exclusions `yaml:"exclusions"`
}

type Override struct {
	WorkloadKind  string `yaml:"workloadKind"`
	WorkloadName  string `yaml:"workloadName"`
	ContainerName string `yaml:"containerName"`
	Value         string `yaml:"value"`
}

type Exclusion struct {
	WorkloadKind  string `yaml:"workloadKind"`
	WorkloadName  string `yaml:"workloadName"`
	ContainerName string `yaml:"containerName"`
}

// String implements fmt.Stringer for Override for concise logging
func (o Override) String() string {
	return fmt.Sprintf("%s/%s:%s=%s", o.WorkloadKind, o.WorkloadName, o.ContainerName, o.Value)
}

// String implements fmt.Stringer for Exclusion for concise logging
func (e Exclusion) String() string {
	return fmt.Sprintf("%s/%s:%s", e.WorkloadKind, e.WorkloadName, e.ContainerName)
}

// Overrides is a named slice of Override to provide a Stringer implementation
type Overrides []Override

// String implements fmt.Stringer for a slice of Override
func (o Overrides) String() string {
	if len(o) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i := range o {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(o[i].String())
	}
	b.WriteByte(']')
	return b.String()
}

// Exclusions is a named slice of Exclusion to provide a Stringer implementation
type Exclusions []Exclusion

// String implements fmt.Stringer for a slice of Exclusion
func (e Exclusions) String() string {
	if len(e) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i := range e {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(e[i].String())
	}
	b.WriteByte(']')
	return b.String()
}
