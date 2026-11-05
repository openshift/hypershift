package verify

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const hypershiftCRDDir = "cmd/install/assets/crds/hypershift-operator"
const karpenterAssetsDir = "karpenter-operator/controllers/karpenter/assets"

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
func copyGlob(pattern, dst string) error {
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no files match %s", pattern)
	}
	for _, path := range paths {
		if err := copyFile(path, filepath.Join(dst, filepath.Base(path))); err != nil {
			return err
		}
	}
	return nil
}
func removeGlobs(patterns ...string) error {
	for _, pattern := range patterns {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		for _, path := range paths {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
	}
	return nil
}
func (r *runner) hypershiftAPI() error {
	if err := r.task("workspace-sync"); err != nil {
		return err
	}
	for name, pkg := range map[string]string{"controller-gen": "sigs.k8s.io/controller-tools/cmd/controller-gen", "codegen": "github.com/openshift/api/tools/codegen/cmd"} {
		if err := r.tool(name, pkg); err != nil {
			return err
		}
	}
	api := filepath.Join(r.root, "api/hypershift/v1beta1")
	if err := removeGlobs(filepath.Join(api, "zz_generated.featuregated-crd-manifests"), filepath.Join(api, "zz_generated.featuregated-crd-manifests.yaml")); err != nil {
		return err
	}
	assets := filepath.Join(r.root, hypershiftCRDDir)
	if err := cleanDirectory(assets, "tests", "doc.go"); err != nil {
		return err
	}
	if err := r.binary("", "controller-gen", "object:headerFile=hack/boilerplate.go.txt", "paths=./api/..."); err != nil {
		return err
	}
	for _, args := range [][]string{{"empty-partial-schemas"}, {"schemapatch"}, {"crd-manifest-merge", "--manifest-merge:payload-manifest-path", "./hypershift/v1beta1/featuregates"}} {
		if err := r.binary("api", "codegen", args...); err != nil {
			return err
		}
	}
	if err := os.Rename(filepath.Join(api, "zz_generated.crd-manifests"), filepath.Join(assets, "zz_generated.crd-manifests")); err != nil {
		return err
	}
	if err := copyGlob(filepath.Join(api, "featuregates/*.yaml"), filepath.Join(assets, "payload-manifests/featuregates")); err != nil {
		return err
	}
	if err := removeGlobs(filepath.Join(assets, "zz_generated.crd-manifests/hostedclusters-SelfManagedHA-*.yaml"), filepath.Join(assets, "zz_generated.crd-manifests/hostedcontrolplanes-SelfManagedHA-*.yaml"), filepath.Join(assets, "zz_generated.crd-manifests/*-CustomNoUpgrade.crd.yaml")); err != nil {
		return err
	}
	for _, pkg := range []string{"scheduling", "certificates", "auditlogpersistence"} {
		if err := r.binary("", "controller-gen", "crd", "paths=./api/"+pkg+"/...", "output:crd:artifacts:config="+assets); err != nil {
			return err
		}
	}
	return nil
}
func (r *runner) karpenterAPI() error {
	if err := r.tool("controller-gen", "sigs.k8s.io/controller-tools/cmd/controller-gen"); err != nil {
		return err
	}
	if err := r.tool("yq", "github.com/mikefarah/yq/v4"); err != nil {
		return err
	}
	assets := filepath.Join(r.root, karpenterAssetsDir)
	if err := copyGlob(filepath.Join(r.root, "vendor/github.com/aws/karpenter-provider-aws/pkg/apis/crds/*"), assets); err != nil {
		return err
	}
	for _, expression := range []string{
		`.spec.versions[0].schema.openAPIV3Schema.properties.spec.required = []`,
		`.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.amiFamily.enum = ["Custom"]`,
		`.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.amiSelectorTerms.x-kubernetes-validations = [{"message": "expected only \"id\" to be set", "rule": "!self.exists(x, has(x.alias) || has(x.tags) || has(x.name) || has(x.owner))"}]`,
		`.spec.versions[0].schema.openAPIV3Schema.properties.spec.x-kubernetes-validations = []`,
		`.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.role."x-kubernetes-validations" = [{"message": "role cannot be empty", "rule": "self != ''"}]`,
	} {
		if err := r.binary("", "yq", "eval", "-i", expression, filepath.Join(assets, "karpenter.k8s.aws_ec2nodeclasses.yaml")); err != nil {
			return err
		}
	}
	if err := r.binary("", "controller-gen", "crd", "paths=./api/karpenter/...", "output:crd:artifacts:config="+assets); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(assets, "karpenter.hypershift.openshift.io_openshiftec2nodeclasses.yaml"), filepath.Join(assets, "zz_generated.crd-manifests/openshiftec2nodeclasses.crd.yaml")); err != nil {
		return err
	}
	return r.goRun("", nil, "run", "./hack/kubelet-ratcheting-gen/main.go")
}

func (r *runner) providerCRDs() error { return r.providerCRDsFor("") }

func (r *runner) providerCRDsFor(filter string) error {
	if err := r.tool("controller-gen", "sigs.k8s.io/controller-tools/cmd/controller-gen"); err != nil {
		return err
	}
	core := []string{"crd", "paths=./vendor/sigs.k8s.io/cluster-api/api/core/...", "paths=./vendor/sigs.k8s.io/cluster-api/api/ipam/v1beta1", "paths=./vendor/sigs.k8s.io/cluster-api/api/ipam/v1beta2", "paths=./vendor/sigs.k8s.io/cluster-api/api/addons/..."}
	if filter == "" || filter == "cluster-api" {
		if err := r.stageCRDs("cluster-api", false, core); err != nil {
			return err
		}
	}
	for _, p := range providers {
		if filter != "" && filter != "cluster-api-provider-"+p.name {
			continue
		}
		path := p.module + "/..."
		args := []string{"crd", "paths=" + path}
		if p.name == "openstack" {
			args = append(args, "paths=github.com/k-orc/openstack-resource-controller/...")
		}
		if err := r.stageCRDs("cluster-api-provider-"+p.name, true, args); err != nil {
			return err
		}
		dir := filepath.Join(r.root, "cmd/install/assets/crds/cluster-api-provider-"+p.name)
		if p.name == "aws" {
			if err := removeGlobs(filepath.Join(dir, "infrastructure.cluster.x-k8s.io_rosa*.yaml"), filepath.Join(dir, "infrastructure.cluster.x-k8s.io_awsmanaged*.yaml"), filepath.Join(dir, "infrastructure.cluster.x-k8s.io_awsfargateprofiles.yaml")); err != nil {
				return err
			}
		}
		if p.name == "azure" {
			if err := removeGlobs(filepath.Join(dir, "infrastructure.cluster.x-k8s.io_azuremanaged*.yaml")); err != nil {
				return err
			}
		}
	}
	return nil
}
func (r *runner) stageCRDs(destination string, workspace bool, args []string) error {
	temp, err := os.MkdirTemp("", "hypershift-crds-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	args = append(slices.Clone(args), "output:crd:artifacts:config="+temp)
	if workspace {
		err = r.workspaceCommand("controller-gen", args...)
	} else {
		err = r.binary("", "controller-gen", args...)
	}
	if err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(temp, "*.yaml"))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("controller-gen produced no CRDs; existing manifests were preserved")
	}
	dst := filepath.Join(r.root, "cmd/install/assets/crds", destination)
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	if err := removeGlobs(filepath.Join(dst, "*.yaml")); err != nil {
		return err
	}
	return copyGlob(filepath.Join(temp, "*.yaml"), dst)
}
func (r *runner) apiDocs() error {
	if err := r.task("workspace-sync"); err != nil {
		return err
	}
	if err := r.tool("gen-crd-api-reference-docs", "github.com/ahmetb/gen-crd-api-reference-docs"); err != nil {
		return err
	}
	temp, err := os.MkdirTemp("", "hypershift-api-docs-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	repo := filepath.Join(temp, "src/github.com/openshift/hypershift")
	if err := os.MkdirAll(filepath.Dir(repo), 0755); err != nil {
		return err
	}
	if err := os.Symlink(r.root, repo); err != nil {
		return err
	}
	return r.exec(repo, []string{"GOPATH=" + temp, "GO111MODULE=off", "GOFLAGS="}, filepath.Join(r.root, "hack/tools/bin/gen-crd-api-reference-docs"), "--config", filepath.Join(repo, "docs/api-doc-gen/config.json"), "--template-dir", filepath.Join(repo, "docs/api-doc-gen/templates"), "--api-dir", "./api/hypershift/v1beta1", "--out-file", filepath.Join(repo, "docs/content/reference/api.md"))
}
func (r *runner) clients() error {
	if err := r.task("workspace-sync"); err != nil {
		return err
	}
	out, err := r.output("", nil, "go", "run", "./cmd/infra/aws/delegatingclientgenerator/main.go")
	if err != nil {
		return err
	}
	out, err = format.Source(out)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.root, "cmd/infra/aws/delegating_client.go"), out, 0644); err != nil {
		return err
	}
	// Match kube_codegen.sh's marked version directories, in sorted order.
	versionRE := regexp.MustCompile(`^v[0-9]+((alpha|beta)[0-9]+)?$`)
	marker := regexp.MustCompile(`(?m)^\s*//\s*\+genclient`)
	dirs := map[string]bool{}
	err = filepath.WalkDir(filepath.Join(r.root, "api"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || !versionRE.MatchString(filepath.Base(filepath.Dir(path))) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if marker.Match(data) {
			dirs[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	sorted := make([]string, 0, len(dirs))
	for dir := range dirs {
		sorted = append(sorted, dir)
	}
	slices.Sort(sorted)
	var pkgs, groups []string
	for _, dir := range sorted {
		out, err := r.output(dir, nil, "go", "list", "-find", ".")
		if err != nil {
			return err
		}
		pkgs = append(pkgs, strings.TrimSpace(string(out)))
		groups = append(groups, filepath.Base(filepath.Dir(dir))+"/"+filepath.Base(dir))
	}
	if len(pkgs) == 0 {
		return nil
	}
	for _, name := range []string{"applyconfiguration-gen", "client-gen", "lister-gen", "informer-gen"} {
		if err := r.tool(name, "k8s.io/code-generator/cmd/"+name); err != nil {
			return err
		}
	}
	outPkg := "github.com/openshift/hypershift/client"
	header := []string{"-v", "0", "--go-header-file", filepath.Join(r.root, "hack/boilerplate.go.txt")}
	generate := func(name, sub string, args []string) error {
		dst := filepath.Join(r.root, "client", sub)
		if err := removeGenerated(dst, name); err != nil {
			return err
		}
		flags := append(slices.Clone(header), "--output-dir", dst, "--output-pkg", outPkg+"/"+sub)
		return r.exec("api", []string{"GOFLAGS=-mod=readonly"}, filepath.Join(r.root, "hack/tools/bin", name), append(flags, args...)...)
	}
	if err := generate("applyconfiguration-gen", "applyconfiguration", append([]string{"--external-applyconfigurations", "", "--openapi-schema", ""}, pkgs...)); err != nil {
		return err
	}
	args := []string{"--clientset-name", "clientset", "--apply-configuration-package", outPkg + "/applyconfiguration", "--input-base", filepath.Join(r.root, "api"), "--plural-exceptions", "", "--prefers-protobuf=false"}
	for _, group := range groups {
		args = append(args, "--input", group)
	}
	if err := generate("client-gen", "clientset", args); err != nil {
		return err
	}
	if err := generate("lister-gen", "listers", append([]string{"--plural-exceptions", ""}, pkgs...)); err != nil {
		return err
	}
	return generate("informer-gen", "informers", append([]string{"--versioned-clientset-package", outPkg + "/clientset/clientset", "--listers-package", outPkg + "/listers", "--plural-exceptions", ""}, pkgs...))
}
func removeGenerated(dir, name string) error {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte("// Code generated by "+name+". DO NOT EDIT.")) {
			return os.Remove(path)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
