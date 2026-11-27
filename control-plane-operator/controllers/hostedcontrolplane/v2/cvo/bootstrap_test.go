package cvo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/podspec"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/jsonpath"

	jsonpatch "github.com/evanphx/json-patch/v5"
)

func TestBootstrapScript(t *testing.T) {
	if os.Getenv("CVO_BOOTSTRAP_OC_HELPER") == "1" {
		if err := runBootstrapOC(os.Args[slices.Index(os.Args, "--")+1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	t.Parallel()

	for _, test := range []struct {
		name          string
		requested     []string
		known         []string
		expected      []string
		errorContains string
	}{
		{
			name:      "When an older payload rejects compatibility requirements, it should bootstrap with supported capabilities",
			requested: []string{"Build", "CompatibilityRequirements", "Console"},
			known:     []string{"Build", "Console"},
			expected:  []string{"Build", "Console"},
		},
		{
			name:      "When the payload supports compatibility requirements, it should keep the capability enabled",
			requested: []string{"Build", "CompatibilityRequirements", "Console"},
			known:     []string{"Build", "CompatibilityRequirements", "Console"},
			expected:  []string{"Build", "CompatibilityRequirements", "Console"},
		},
		{
			name:      "When multiple requested capabilities are unsupported, it should remove each without shifting other entries",
			requested: []string{"ClusterAPI", "Build", "CompatibilityRequirements"},
			known:     []string{"Build"},
			expected:  []string{"Build"},
		},
		{
			name:      "When no capabilities are requested, it should preserve the empty capability set",
			requested: []string{},
			known:     []string{"Build"},
		},
		{
			name:          "When the payload schema has no capability enum, it should fail before applying the ClusterVersion",
			requested:     []string{"Build"},
			errorContains: "ClusterVersion CRD does not declare supported capabilities",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "bootstrap workspace")
			payload := filepath.Join(dir, "payload", "manifests")
			if err := os.MkdirAll(payload, 0755); err != nil {
				t.Fatal(err)
			}
			crd := filepath.Join(payload, "0000_00_cluster-version-operator_01_clusterversions-Default.crd.yaml")
			if err := os.WriteFile(crd, []byte(`{"kind":"CustomResourceDefinition"}`), 0600); err != nil {
				t.Fatal(err)
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			oc := "#!/bin/bash\nCVO_BOOTSTRAP_OC_HELPER=1 exec \"$CVO_BOOTSTRAP_TEST_BINARY\" -test.run='^TestBootstrapScript$' -- \"$@\"\n"
			if err := os.WriteFile(filepath.Join(dir, "oc"), []byte(oc), 0700); err != nil {
				t.Fatal(err)
			}
			cv := configv1.ClusterVersion{
				TypeMeta:   metav1.TypeMeta{APIVersion: "config.openshift.io/v1", Kind: "ClusterVersion"},
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec: configv1.ClusterVersionSpec{
					ClusterID: "a52b8add-32e3-4dbb-acb2-09a90278534b",
					Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
						BaselineCapabilitySet: configv1.ClusterVersionCapabilitySetNone,
					},
				},
			}
			for _, capability := range test.requested {
				cv.Spec.Capabilities.AdditionalEnabledCapabilities = append(cv.Spec.Capabilities.AdditionalEnabledCapabilities, configv1.ClusterVersionCapability(capability))
			}
			cvJSON, err := json.Marshal(cv)
			if err != nil {
				t.Fatal(err)
			}
			knownJSON, err := json.Marshal(test.known)
			if err != nil {
				t.Fatal(err)
			}
			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			if err != nil {
				t.Fatal(err)
			}
			script := podspec.FindContainer("bootstrap", deployment.Spec.Template.Spec.InitContainers).Args[1]
			script = strings.ReplaceAll(script, "/tmp/clusterversion", "./clusterversion")
			script = strings.ReplaceAll(script, "/var/payload", "./payload")
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-x", "-c", script)
			cmd.Dir = dir
			cmd.WaitDelay = 5 * time.Second
			cmd.Env = append(os.Environ(),
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"CLUSTER_VERSION_JSON="+string(cvJSON),
				"CVO_BOOTSTRAP_KNOWN_CAPABILITIES="+string(knownJSON),
				"CVO_BOOTSTRAP_TEST_BINARY="+binary,
				"CVO_BOOTSTRAP_SUBMITTED="+filepath.Join(dir, "submitted.json"),
			)
			output, err := cmd.CombinedOutput()
			if test.errorContains != "" {
				if err == nil {
					t.Fatalf("expected bootstrap to fail without a supported capability list; output: %s", output)
				}
				if !strings.Contains(string(output), test.errorContains) {
					t.Errorf("expected error containing %q, got %s", test.errorContains, output)
				}
				if _, err := os.Stat(filepath.Join(dir, "submitted.json")); !os.IsNotExist(err) {
					t.Fatal("bootstrap applied a ClusterVersion without knowing the supported capabilities")
				}
				return
			}
			if err != nil {
				t.Fatalf("bootstrap failed: %v\n%s", err, output)
			}
			submitted, err := os.ReadFile(filepath.Join(dir, "submitted.json"))
			if err != nil {
				t.Fatal(err)
			}
			var actual configv1.ClusterVersion
			if err := json.Unmarshal(submitted, &actual); err != nil {
				t.Fatal(err)
			}
			var enabled []string
			for _, capability := range actual.Spec.Capabilities.AdditionalEnabledCapabilities {
				enabled = append(enabled, string(capability))
			}
			if !slices.Equal(test.expected, enabled) {
				t.Errorf("expected capabilities %v, got %v", test.expected, enabled)
			}
			if actual.Spec.ClusterID != "a52b8add-32e3-4dbb-acb2-09a90278534b" || actual.Spec.Capabilities.BaselineCapabilitySet != configv1.ClusterVersionCapabilitySetNone {
				t.Error("bootstrap changed the cluster ID or baseline capability set")
			}
		})
	}
}

// runBootstrapOC supplies the API responses and local JSON transformations used
// by the real bootstrap script. Applying an unsupported capability reproduces
// the older payload's schema rejection without a live API server.
func runBootstrapOC(args []string) error {
	var known []string
	if err := json.Unmarshal([]byte(os.Getenv("CVO_BOOTSTRAP_KNOWN_CAPABILITIES")), &known); err != nil {
		return err
	}
	option := func(name string) string {
		index := slices.Index(args, name)
		if index >= 0 && index+1 < len(args) {
			return args[index+1]
		}
		return ""
	}
	var document []byte
	var err error
	switch args[0] {
	case "get":
		if args[1] == "ns" {
			return nil
		}
		enum := ""
		if len(known) > 0 {
			enum = `"enum":` + os.Getenv("CVO_BOOTSTRAP_KNOWN_CAPABILITIES")
		}
		document = []byte(fmt.Sprintf(`{"spec":{"versions":[{"name":"v1","schema":{"openAPIV3Schema":{
          "properties":{"spec":{"properties":{"capabilities":{"properties":{
            "additionalEnabledCapabilities":{"items":{%s}}
          }}}}}
        }}}]}}`, enum))
	case "create", "patch", "apply":
		if args[0] == "create" && (!slices.Contains(args, "--dry-run=client") || !slices.Contains(args, "--validate=false")) {
			return fmt.Errorf("capability inspection requires --dry-run=client --validate=false")
		}
		file := option("-f")
		info, err := os.Stat(file)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		document, err = os.ReadFile(file)
		if err != nil {
			return err
		}
		if args[0] == "patch" {
			patch, err := jsonpatch.DecodePatch([]byte(option("-p")))
			if err != nil {
				return err
			}
			document, err = patch.Apply(document)
			if err != nil {
				return err
			}
		}
		if args[0] == "apply" {
			var cv configv1.ClusterVersion
			if err := json.Unmarshal(document, &cv); err != nil {
				return err
			}
			if cv.Kind != "ClusterVersion" {
				return nil
			}
			for _, capability := range cv.Spec.Capabilities.AdditionalEnabledCapabilities {
				if !slices.Contains(known, string(capability)) {
					return fmt.Errorf("unsupported value: %q", capability)
				}
			}
			return os.WriteFile(os.Getenv("CVO_BOOTSTRAP_SUBMITTED"), document, 0600)
		}
	default:
		return fmt.Errorf("unexpected oc invocation: %v", args)
	}
	output := option("-o")
	if output == "json" {
		_, err = os.Stdout.Write(document)
		return err
	}
	var object any
	if err := json.Unmarshal(document, &object); err != nil {
		return err
	}
	printer := jsonpath.New("oc").AllowMissingKeys(true)
	if err := printer.Parse(strings.TrimPrefix(output, "jsonpath=")); err != nil {
		return err
	}
	return printer.Execute(os.Stdout, object)
}
