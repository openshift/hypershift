package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/modfile"
)

type runner struct {
	ctx  context.Context
	root string
	env  []string
	done map[string]bool
	base string
}

// Run executes one task. All verification subprocesses use the Go version in
// the root go.mod, host OS/architecture, vendored tools, and CGO_ENABLED=0.
func Run(ctx context.Context, args []string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "Makefile")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return errors.New("run verify from the HyperShift repository")
		}
		root = parent
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return err
	}
	mod, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return err
	}
	r := &runner{ctx: ctx, root: root, done: map[string]bool{}}
	r.env = environment(os.Environ(), "GO111MODULE=on", "GOWORK=off", "GOFLAGS=-mod=vendor", "CGO_ENABLED=0", "GOTOOLCHAIN=go"+mod.Go.Version, "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	if os.Getenv("HOME") == "/" {
		r.env = environment(r.env, "HOME="+os.TempDir())
	}
	// Hooks export repository-local Git variables. Clear Git's own list before
	// entering subdirectories or temporary repositories so discovery follows cwd.
	gitVariables, err := r.output("", nil, "git", "rev-parse", "--local-env-vars")
	if err != nil {
		return err
	}
	localVariables := strings.Fields(string(gitVariables))
	r.env = slices.DeleteFunc(r.env, func(value string) bool {
		key, _, _ := strings.Cut(value, "=")
		return slices.Contains(localVariables, key)
	})
	task := "verify"
	if len(args) > 0 {
		task = args[0]
	}
	if len(args) > 1 {
		return errors.New("usage: go run ./cmd/verify [task]")
	}
	return r.task(task)
}

func environment(base []string, values ...string) []string {
	out := slices.Clone(base)
	for _, value := range values {
		key, _, _ := strings.Cut(value, "=")
		out = slices.DeleteFunc(out, func(v string) bool { return strings.HasPrefix(v, key+"=") })
		out = append(out, value)
	}
	return out
}

func (r *runner) command(dir string, env []string, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(r.ctx, name, args...)
	cmd.Dir = dir
	if !filepath.IsAbs(dir) {
		cmd.Dir = filepath.Join(r.root, dir)
	}
	cmd.Env = environment(environment(r.env, env...), "PWD="+cmd.Dir)
	return cmd
}

func (r *runner) exec(dir string, env []string, name string, args ...string) error {
	cmd := r.command(dir, env, name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s (in %s): %w", name, strings.Join(args, " "), dir, err)
	}
	return nil
}

func (r *runner) output(dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := r.command(dir, env, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s (in %s): %w\n%s", name, strings.Join(args, " "), dir, err, &stderr)
	}
	return out, nil
}

func (r *runner) goRun(dir string, env []string, args ...string) error {
	return r.exec(dir, env, "go", args...)
}

func (r *runner) tool(name, pkg string) error {
	if r.done["tool:"+name] {
		return nil
	}
	if err := r.goRun("hack/tools", nil, "build", "-tags=tools", "-o", filepath.Join(r.root, "hack/tools/bin", name), pkg); err != nil {
		return err
	}
	r.done["tool:"+name] = true
	return nil
}

func (r *runner) binary(dir, name string, args ...string) error {
	return r.exec(dir, nil, filepath.Join(r.root, "hack/tools/bin", name), args...)
}

func (r *runner) comparisonBase() (string, error) {
	if r.base != "" {
		return r.base, nil
	}
	if base := os.Getenv("PULL_BASE_SHA"); base != "" {
		r.base = base
		return base, nil
	}
	remotes, err := r.output("", nil, "git", "remote", "-v")
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(remotes), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && strings.Contains(fields[1], "openshift/hypershift") && fields[2] == "(fetch)" {
			if out, err := r.output("", nil, "git", "rev-parse", fields[0]+"/main"); err == nil {
				r.base = strings.TrimSpace(string(out))
				return r.base, nil
			}
		}
	}
	out, err := r.output("", nil, "git", "rev-parse", "main")
	if err != nil {
		return "", errors.New("cannot find the base branch; set PULL_BASE_SHA")
	}
	r.base = strings.TrimSpace(string(out))
	return r.base, nil
}

func (r *runner) task(name string) error {
	if r.done[name] {
		return nil
	}
	start := time.Now()
	fmt.Fprintf(os.Stderr, "==> %s\n", name)
	err := r.perform(name)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	r.done[name] = true
	fmt.Fprintf(os.Stderr, "<== %s (%s)\n", name, time.Since(start).Round(time.Millisecond))
	return nil
}

func (r *runner) tasks(names ...string) error {
	for _, name := range names {
		if err := r.task(name); err != nil {
			return err
		}
	}
	return nil
}

func (r *runner) perform(name string) error {
	if strings.HasPrefix(name, "cluster-api") {
		return r.providerCRDsFor(name)
	}
	if strings.HasPrefix(name, "capi-sync-") && name != "capi-sync-force" {
		if err := r.tool("controller-gen", "sigs.k8s.io/controller-tools/cmd/controller-gen"); err != nil {
			return err
		}
		for _, p := range providers {
			if name == "capi-sync-"+p.name {
				return r.syncProvider(p)
			}
		}
		return fmt.Errorf("unknown provider in %q", name)
	}
	switch name {
	case "verify", "verify-ci", "verify-quick":
		if err := r.tasks("verify-repository", "lint"); err != nil {
			return err
		}
		delete(r.done, "verify-git-clean")
		return r.task("verify-git-clean")
	case "verify-repository":
		// Finish writers before repository checks read generated sources. CI runs
		// lint in a separate checkout; full verify runs both phases here.
		if err := r.tasks("update", "generate", "fmt", "verify-capi-decoupling"); err != nil {
			return err
		}
		checks := []string{"verify-tool-tests", "verify-codespell", "verify-codecov", "verify-api-deps", "verify-crd-schema", "cpo-container-sync", "run-gitlint", "verify-docs-nav", "verify-tekton-pipeline-pairs"}
		if err := r.checks(checks); err != nil {
			return err
		}
		return r.task("verify-git-clean")
	case "update":
		return r.tasks("api-deps", "workspace-sync", "capi-sync", "deps", "api", "clients", "docs-aggregate", "ownership-rules")
	case "api-deps":
		return r.moduleDeps("api")
	case "deps":
		if err := r.task("workspace-sync"); err != nil {
			return err
		}
		if err := r.moduleDeps(""); err != nil {
			return err
		}
		return r.moduleDeps("hack/tools")
	case "workspace-sync":
		if err := r.task("api-deps"); err != nil {
			return err
		}
		if err := r.goRun("hack/workspace", []string{"GOWORK=" + filepath.Join(r.root, "hack/workspace/go.work")}, "work", "sync"); err != nil {
			return err
		}
		if err := r.goRun("api", nil, "mod", "download", "all"); err != nil {
			return err
		}
		return r.goRun("api", nil, "mod", "vendor")
	case "capi-sync", "capi-sync-force":
		return r.syncProviders()
	case "verify-capi-sync":
		if err := r.tasks("generate", "capi-sync"); err != nil {
			return err
		}
		out, err := r.output("", nil, "git", "status", "--porcelain", "--", "pkg/capi/")
		if err != nil {
			return err
		}
		if len(out) > 0 {
			return fmt.Errorf("pkg/capi is out of sync; commit make capi-sync-force output:\n%s", out)
		}
		return nil
	case "verify-capi-decoupling":
		if err := r.task("verify-capi-sync"); err != nil {
			return err
		}
		return r.capiDecoupling()
	case "generate":
		if err := r.tool("mockgen", "go.uber.org/mock/mockgen"); err != nil {
			return err
		}
		return r.goRun("", nil, "generate", "./...")
	case "api":
		if err := r.tasks("hypershift-api", "karpenter-api"); err != nil {
			return err
		}
		if err := r.providerCRDs(); err != nil {
			return err
		}
		return r.task("api-docs")
	case "hypershift-api":
		return r.hypershiftAPI()
	case "karpenter-api":
		return r.karpenterAPI()
	case "api-docs":
		return r.apiDocs()
	case "clients":
		return r.clients()
	case "docs-aggregate":
		return r.goRun("", nil, "run", "./hack/tools/docs-aggregator/main.go")
	case "ownership-rules":
		return r.ownership()
	case "staticcheck":
		if err := r.tool("staticcheck", "honnef.co/go/tools/cmd/staticcheck"); err != nil {
			return err
		}
		return r.binary("", "staticcheck", "./control-plane-operator/...", "./control-plane-pki-operator/...", "./hypershift-operator/controllers/...", "./ignition-server/...", "./cmd/...", "./support/certs/...", "./support/releaseinfo/...", "./support/upsert/...", "./konnectivity-socks5-proxy/...", "./contrib/...", "./availability-prober/...", "./test/integration/...")
	case "fmt":
		return r.goRun("", nil, "fmt", "./...")
	case "vet":
		return r.goRun("", nil, "vet", "-tags", "integration,e2e,reqserving,e2ev2,backuprestore,envtest", "./...")
	case "golangci-lint":
		return r.tool("golangci-lint", "./cmd/golangci-lint")
	case "lint":
		if err := r.tasks("generate", "capi-sync"); err != nil {
			return err
		}
		return r.checks([]string{"staticcheck", "vet", "lint-modules"})
	case "lint-modules":
		if err := r.tool("golangci-lint", "./cmd/golangci-lint"); err != nil {
			return err
		}
		base, err := r.comparisonBase()
		if err != nil {
			return err
		}
		apiErr := r.binary("api", "golangci-lint", "run", "--config", ".golangci.yml", "--modules-download-mode=readonly", "-v", "--new-from-rev="+base)
		return errors.Join(apiErr, r.binary("", "golangci-lint", "run", "--config", ".golangci.yml", "--modules-download-mode=readonly", "-v"))
	case "verify-tool-tests":
		return r.goRun("hack/tools", nil, "test", "./verify", "./crd-schema-check")
	case "verify-api-deps":
		if err := r.goRun("hack/tools", nil, "test", "./verify-api-deps", "-count=1"); err != nil {
			return err
		}
		if err := r.tool("verify-api-deps", "./verify-api-deps"); err != nil {
			return err
		}
		return r.binary("", "verify-api-deps")
	case "verify-crd-schema":
		if err := r.tool("crd-schema-check", "./crd-schema-check"); err != nil {
			return err
		}
		base, err := r.comparisonBase()
		if err != nil {
			return err
		}
		return r.binary("", "crd-schema-check", "--comparison-base="+base, "--config=hack/tools/crd-schema-check/crdify-config.yaml", "--crd-dir=cmd/install/assets/crds/hypershift-operator", "--crd-dir=karpenter-operator/controllers/karpenter/assets/zz_generated.crd-manifests")
	case "verify-git-clean":
		out, err := r.output("", nil, "git", "status", "--porcelain", "--untracked-files=normal")
		if err != nil {
			return err
		}
		if len(out) > 0 {
			return fmt.Errorf("generated or uncommitted changes; run make update generate and commit the result:\n%s", out)
		}
		return nil
	case "verify-docs-nav":
		return r.docsNav()
	case "verify-tekton-pipeline-pairs":
		return r.tektonPairs()
	case "verify-codespell":
		return r.spelling()
	case "run-gitlint":
		return r.commits()
	case "run-gitlint-commit-msg":
		return r.pendingCommit()
	case "verify-codecov":
		return r.codecov()
	case "cpo-container-sync":
		return r.containerfiles()
	default:
		return fmt.Errorf("unknown verification task %q", name)
	}
}

func (r *runner) moduleDeps(dir string) error {
	for _, args := range [][]string{{"mod", "tidy"}, {"mod", "vendor"}, {"mod", "verify"}} {
		if err := r.goRun(dir, nil, args...); err != nil {
			return err
		}
	}
	_, err := r.output(dir, nil, "go", "list", "-m", "-mod=readonly", "-json", "all")
	return err
}

func (r *runner) checks(names []string) error {
	jobs := 2
	if value := os.Getenv("VERIFY_JOBS"); value != "" {
		var err error
		jobs, err = strconv.Atoi(value)
		if err != nil || jobs < 1 {
			return errors.New("VERIFY_JOBS must be a positive integer")
		}
	}
	// Each check gets its own task cache; checks never mutate dependency state.
	var wg sync.WaitGroup
	sem := make(chan struct{}, jobs)
	errs := make([]error, len(names))
	for i, name := range names {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			child := *r
			child.done = maps.Clone(r.done)
			errs[i] = child.task(name)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}
