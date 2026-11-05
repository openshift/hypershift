package verify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type provider struct {
	name, module   string
	support, prune []string
}

var providers = []provider{
	{name: "agent", module: "github.com/openshift/cluster-api-provider-agent/api"},
	{name: "aws", module: "sigs.k8s.io/cluster-api-provider-aws/v2", prune: []string{"rosa*", "awsfargate*", "awsmanaged*", "webhooks.go", "s3bucket.go", "awscluster_defaults.go"}},
	{name: "azure", module: "sigs.k8s.io/cluster-api-provider-azure", support: []string{"util/azure"}},
	{name: "gcp", module: "sigs.k8s.io/cluster-api-provider-gcp"},
	{name: "ibmcloud", module: "sigs.k8s.io/cluster-api-provider-ibmcloud"},
	{name: "kubevirt", module: "sigs.k8s.io/cluster-api-provider-kubevirt"},
	{name: "openstack", module: "sigs.k8s.io/cluster-api-provider-openstack", support: []string{"pkg/utils/optional", "pkg/utils/errors"}},
}

func (r *runner) syncProviders() error {
	if err := r.tool("controller-gen", "sigs.k8s.io/controller-tools/cmd/controller-gen"); err != nil {
		return err
	}
	for _, p := range providers {
		if err := r.syncProvider(p); err != nil {
			return fmt.Errorf("%s: %w", p.name, err)
		}
	}
	return nil
}

func (r *runner) syncProvider(p provider) error {
	modDir := "hack/capi-vendor/" + p.name
	for _, args := range [][]string{{"mod", "tidy"}, {"mod", "download"}} {
		if err := r.goRun(modDir, []string{"GOFLAGS="}, args...); err != nil {
			return err
		}
	}
	out, err := r.output(modDir, []string{"GOFLAGS="}, "go", "mod", "download", "-json", p.module)
	if err != nil {
		return err
	}
	var download struct{ Dir string }
	if err := json.Unmarshal(out, &download); err != nil {
		return err
	}
	if download.Dir == "" {
		return errors.New("module download has no directory")
	}
	dst := filepath.Join(r.root, "pkg/capi", p.name)
	if err := cleanDirectory(dst, "go.mod", "go.sum", ".synced"); err != nil {
		return err
	}
	copyPackage := func(sub string, allow ...string) error {
		args := []string{"run", filepath.Join(r.root, "hack/copy-capi-types/main.go"), "--src", filepath.Join(download.Dir, sub), "--dst", filepath.Join(dst, sub)}
		for _, prefix := range allow {
			args = append(args, "--allow", prefix)
		}
		return r.goRun("", []string{"GO111MODULE=off", "GOFLAGS="}, args...)
	}
	for _, sub := range p.support {
		if err := copyPackage(sub); err != nil {
			return err
		}
	}
	imports, err := parser.ParseFile(token.NewFileSet(), filepath.Join(r.root, modDir, "vendor_imports.go"), nil, parser.ImportsOnly)
	if err != nil {
		return err
	}
	if len(imports.Imports) == 0 {
		return errors.New("no provider API imports")
	}
	for _, imp := range imports.Imports {
		pkg, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return err
		}
		sub, ok := strings.CutPrefix(pkg, p.module+"/")
		if !ok {
			return fmt.Errorf("%s is outside %s", pkg, p.module)
		}
		var allow []string
		switch p.name + ":" + sub {
		case "aws:api/v1beta1", "aws:exp/api/v1beta1", "aws:exp/api/v1beta2":
			allow = []string{p.module + "/api/"}
		case "kubevirt:api/v1alpha1":
			allow = []string{"kubevirt.io/api"}
		case "openstack:api/v1beta1":
			allow = []string{p.module + "/pkg/utils/"}
		case "openstack:api/v1alpha1":
			allow = []string{p.module + "/api/", p.module + "/pkg/utils/"}
		}
		if err := copyPackage(sub, allow...); err != nil {
			return err
		}
	}
	err = filepath.WalkDir(dst, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		remove := strings.HasSuffix(d.Name(), "_test.go") || strings.Contains(d.Name(), "conversion")
		for _, glob := range p.prune {
			match, _ := filepath.Match(glob, d.Name())
			remove = remove || match
		}
		if remove {
			return os.Remove(path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := r.goRun("", []string{"GO111MODULE=off", "GOFLAGS="}, "run", filepath.Join(r.root, "hack/copy-capi-types/main.go"), "--localize", "--dir", dst, "--module", p.module, "--capi-mod-dir", filepath.Join(r.root, modDir)); err != nil {
		return err
	}
	if err := r.workspaceCommand("controller-gen", "object:headerFile="+filepath.Join(r.root, "hack/boilerplate.go.txt"), "paths="+p.module+"/..."); err != nil {
		return err
	}
	return r.goRun("pkg/capi/"+p.name, []string{"GOFLAGS="}, "mod", "tidy")
}

func (r *runner) workspaceCommand(name string, args ...string) error {
	dir, err := os.MkdirTemp("", "hypershift-workspace-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.Symlink(r.root, filepath.Join(dir, "hypershift")); err != nil {
		return err
	}
	var work strings.Builder
	data, err := os.ReadFile(filepath.Join(r.root, "go.mod"))
	if err != nil {
		return err
	}
	version := regexp.MustCompile(`(?m)^go (\S+)$`).FindSubmatch(data)
	if len(version) != 2 {
		return errors.New("root go.mod has no Go version")
	}
	fmt.Fprintf(&work, "go %s\n\nuse (\n ./hypershift\n ./hypershift/api\n", version[1])
	mods, err := filepath.Glob(filepath.Join(r.root, "pkg/capi/*/go.mod"))
	if err != nil {
		return err
	}
	for _, mod := range mods {
		rel, err := filepath.Rel(r.root, filepath.Dir(mod))
		if err != nil {
			return err
		}
		fmt.Fprintf(&work, " ./hypershift/%s\n", filepath.ToSlash(rel))
	}
	work.WriteString(")\n")
	// Older CAPI releases do not have the separate API module.
	if out, err := r.output("", nil, "go", "list", "-m", "-f", "{{.Version}}", "sigs.k8s.io/cluster-api/api"); err == nil && len(bytes.TrimSpace(out)) > 0 {
		fmt.Fprintf(&work, "\nreplace sigs.k8s.io/cluster-api/api => sigs.k8s.io/cluster-api/api %s\n", bytes.TrimSpace(out))
	}
	file := filepath.Join(dir, "go.work")
	if err := os.WriteFile(file, []byte(work.String()), 0600); err != nil {
		return err
	}
	return r.exec(dir, []string{"GOWORK=" + file, "GOFLAGS="}, filepath.Join(r.root, "hack/tools/bin", name), args...)
}

func (r *runner) capiDecoupling() error {
	var errs []error
	for _, p := range providers {
		dir := "pkg/capi/" + p.name
		mod, err := os.ReadFile(filepath.Join(r.root, dir, "go.mod"))
		if err != nil {
			return err
		}
		if regexp.MustCompile(`(?m)^\s*sigs\.k8s\.io/cluster-api([\s/]|$)`).Match(mod) {
			errs = append(errs, fmt.Errorf("%s requires upstream CAPI", dir))
		}
		err = filepath.WalkDir(filepath.Join(r.root, dir), func(path string, d fs.DirEntry, err error) error {
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
			if regexp.MustCompile(`"sigs\.k8s\.io/cluster-api/api/[a-z0-9/]+"`).Match(data) {
				return fmt.Errorf("%s imports upstream CAPI instead of localized capicore", path)
			}
			return nil
		})
		errs = append(errs, err, r.goRun(dir, []string{"GOFLAGS=-mod=readonly"}, "build", "./..."))
	}
	paths, err := r.sourceFiles()
	if err != nil {
		return err
	}
	consumers := map[string]bool{}
	for _, path := range paths {
		if !strings.HasSuffix(path, ".go") || !r.rootModuleFile(path) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.root, path))
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte("capicore")) {
			consumers["./"+filepath.ToSlash(filepath.Dir(path))+"/..."] = true
		}
	}
	if len(consumers) > 0 {
		pkgs := make([]string, 0, len(consumers))
		for pkg := range consumers {
			pkgs = append(pkgs, pkg)
		}
		slices.Sort(pkgs)
		errs = append(errs, r.goRun("", nil, append([]string{"vet"}, pkgs...)...))
	}
	return errors.Join(errs...)
}

func cleanDirectory(dir string, keep ...string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !slices.Contains(keep, entry.Name()) {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *runner) sourceFiles() ([]string, error) {
	out, err := r.output("", nil, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	paths := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	paths = slices.DeleteFunc(paths, func(p string) bool {
		_, err := os.Stat(filepath.Join(r.root, p))
		return p == "" || err != nil || strings.Contains("/"+p, "/vendor/")
	})
	slices.Sort(paths)
	return slices.Compact(paths), nil
}

// A root-module vet pattern cannot include packages belonging to nested modules.
func (r *runner) rootModuleFile(path string) bool {
	for dir := filepath.Dir(filepath.Join(r.root, path)); dir != r.root; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return false
		}
		if filepath.Dir(dir) == dir {
			return false
		}
	}
	return true
}
