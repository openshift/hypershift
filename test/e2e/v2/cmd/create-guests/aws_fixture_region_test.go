//go:build e2ev2

package main

import (
	"strings"
	"testing"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	"github.com/spf13/pflag"
)

func TestEffectiveAWSRegion(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "When the region uses an equals sign, it should match the production string flag", args: []string{"--region=us-east-1"}, want: "us-east-1"},
		{name: "When the region uses a separate value, it should match the production string flag", args: []string{"--region", "us-west-2"}, want: "us-west-2"},
		{name: "When extra args use a separate value, it should override the common equals argument", args: []string{"--region=us-east-1", "--zones=us-west-2a", "--region", "us-west-2"}, want: "us-west-2"},
		{name: "When mixed repeated flags are supplied, it should use the last region", args: []string{"--region", "us-east-1", "--region=us-west-2", "--region", "eu-west-1", "--region=ap-southeast-2"}, want: "ap-southeast-2"},
		{name: "When a region-like value belongs to another flag, it should not treat that value as a region flag", args: []string{"--region=us-east-1", "--annotations=example.test/region=us-west-2"}, want: "us-east-1"},
		{name: "When flags follow the terminator, it should ignore them like pflag", args: []string{"--region=us-east-1", "--", "--region=us-west-2"}, want: "us-east-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := effectiveAWSRegion(test.args)
			if err != nil || got != test.want {
				t.Fatalf("unexpected effective region: %q, %v", got, err)
			}
			flags := pflag.NewFlagSet("create cluster aws", pflag.ContinueOnError)
			parsed := flags.String("region", "", "")
			flags.String("zones", "", "")
			flags.String("annotations", "", "")
			if err := flags.Parse(test.args); err != nil || *parsed != got {
				t.Fatalf("region differs from production pflag semantics: %q, %v", *parsed, err)
			}
		})
	}
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "When region is absent, it should reject input", args: []string{"--zones=us-east-1a"}},
		{name: "When region lacks its separate value, it should reject input", args: []string{"--region=us-east-1", "--region"}},
		{name: "When region has an empty equals value, it should reject input", args: []string{"--region="}},
		{name: "When region has an empty separate value, it should reject input", args: []string{"--region", ""}},
		{name: "When the separate region value is another flag, it should reject ambiguous input", args: []string{"--region", "--zones=us-west-2a"}},
		{name: "When the separate region value is a terminator, it should reject ambiguous input", args: []string{"--region", "--"}},
		{name: "When region contains whitespace, it should reject ambiguous input", args: []string{"--region=us-west-2 us-east-1"}},
		{name: "When a malformed earlier region is followed by a valid region, it should reject malformed input", args: []string{"--region=", "--region=us-west-2"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := effectiveAWSRegion(test.args); err == nil {
				t.Fatal("accepted malformed region input")
			}
		})
	}
}

func TestAWSFixtureRegion(t *testing.T) {
	for _, test := range []struct {
		name       string
		createArgs []string
		destroy    []string
		want       string
		wantError  string
	}{
		{name: "When both effective regions match, it should accept mixed flag forms", createArgs: []string{"--region=us-east-1", "--region", "us-west-2"}, destroy: []string{"--region", "us-west-2"}, want: "us-west-2"},
		{name: "When a repeated override returns to the manifest region, it should accept the final value", createArgs: []string{"--region=us-east-1", "--region", "us-west-2", "--region=us-east-1"}, destroy: []string{"--region=us-east-1"}, want: "us-east-1"},
		{name: "When the create region differs from destruction, it should reject the override", createArgs: []string{"--region=us-east-1", "--region", "us-west-2"}, destroy: []string{"--region=us-east-1"}, wantError: "set HYPERSHIFT_AWS_REGION"},
		{name: "When the create region is malformed, it should reject input", createArgs: []string{"--region"}, destroy: []string{"--region=us-east-1"}, wantError: "create region"},
		{name: "When the destroy region is malformed, it should reject input", createArgs: []string{"--region=us-east-1"}, destroy: []string{"--region"}, wantError: "destroy region"},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := namedSpec{ClusterSpec: lifecycle.ClusterSpec{CreateArgs: test.createArgs, DestroyArgs: test.destroy}}
			got, err := awsFixtureRegion(spec)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("missing region rejection: %v", err)
				}
			} else if err != nil || got != test.want {
				t.Fatalf("unexpected effective fixture region: %q, %v", got, err)
			}
		})
	}
}

func TestCreateAWSFixture(t *testing.T) {
	t.Run("When alias resolution has a mismatched or malformed region, it should reject input before credentials or AWS work", func(t *testing.T) {
		t.Setenv("E2E_AWS_KMS_KEY_ALIAS", "alias/hypershift-e2e")
		for _, arguments := range [][]string{{"--region=us-east-1", "--region", "us-west-2"}, {"--region"}} {
			spec := namedSpec{ClusterSpec: lifecycle.ClusterSpec{Variant: "custom-config", CreateArgs: arguments, DestroyArgs: []string{"--region=us-east-1"}}}
			err := createAWSFixture(t.Context(), envConfig{}, spec)
			if err == nil || !strings.Contains(err.Error(), "region") {
				t.Fatalf("invalid region reached an external lookup: %v", err)
			}
		}
	})
}
