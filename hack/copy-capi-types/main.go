// copy-capi-types copies Go API types from vendored CAPI provider packages
// into a local package, stripping external dependencies that aren't needed.
//
// It uses Go's AST parser to precisely identify and remove:
//   - Functions/methods that reference banned imports
//   - Var/const/type declarations that reference banned imports
//   - Import declarations that become unused after stripping
//   - Files that consist entirely of banned content
//
// Dependencies considered safe (kept as-is):
//   - k8s.io/apimachinery/...
//   - k8s.io/api/...
//   - k8s.io/utils/...
//   - sigs.k8s.io/controller-runtime/pkg/scheme
//   - sigs.k8s.io/cluster-api/api/...
//   - sigs.k8s.io/cluster-api/errors
//   - Standard library
//
// Everything else is banned and any declaration referencing a banned import
// is stripped from the output.
//
// CAPI itself is kept during the copy but does not survive in the final
// module: a second pass (--localize) extracts the CAPI declarations the copied
// types need into the provider module and repoints the imports at them. See
// the "Localize mode" section below.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var allowedImportPrefixes = []string{
	"k8s.io/apimachinery/",
	"k8s.io/api/",
	"k8s.io/utils/",
	"sigs.k8s.io/controller-runtime/pkg/scheme",
	"sigs.k8s.io/cluster-api/api/",
	"sigs.k8s.io/cluster-api/errors",
}

var skipFiles = map[string]string{
	"zz_generated.defaults.go":   "generated defaults reference functions from stripped files",
	"zz_generated.conversion.go": "generated conversions reference stripped files",
}

var skipSuffixes = map[string]string{
	"_webhook.go": "webhook infrastructure not used by HyperShift",
}

type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	src := flag.String("src", "", "source directory (vendored API package)")
	dst := flag.String("dst", "", "destination directory for copied types")
	var extraAllowed stringSlice
	flag.Var(&extraAllowed, "allow", "additional allowed import prefix (can be repeated)")

	localize := flag.Bool("localize", false, "localize CAPI types into the provider module instead of copying a package")
	dir := flag.String("dir", "", "localize: provider module root (the directory holding its go.mod)")
	module := flag.String("module", "", "localize: module path declared by that go.mod")
	capiRoot := flag.String("capi-root", "", "localize: source root of the CAPI module this provider resolves to")
	capiAPIRoot := flag.String("capi-api-root", "", "localize: source root of the separate CAPI API module, if present")
	outName := flag.String("out", "capicore", "localize: subdirectory to hold the extracted CAPI types")
	flag.Parse()

	if *localize {
		if *dir == "" || *module == "" || *capiRoot == "" {
			log.Fatal("--localize requires --dir, --module and --capi-root")
		}
		runLocalize(*dir, *module, *capiRoot, *capiAPIRoot, *outName)
		return
	}

	for _, prefix := range extraAllowed {
		allowedImportPrefixes = append(allowedImportPrefixes, prefix)
	}

	if *src == "" || *dst == "" {
		log.Fatal("--src and --dst are required")
	}

	if err := os.MkdirAll(*dst, 0755); err != nil {
		log.Fatalf("failed to create destination: %v", err)
	}

	files, err := filepath.Glob(filepath.Join(*src, "*.go"))
	if err != nil {
		log.Fatalf("failed to glob: %v", err)
	}
	if len(files) == 0 {
		log.Fatalf("no .go files found in %s", *src)
	}

	// Pass 1: process each file individually (within-file cascading)
	var results []fileResult
	allStrippedSymbols := make(map[string]bool)

	for _, srcFile := range files {
		base := filepath.Base(srcFile)

		if reason, ok := skipFiles[base]; ok {
			results = append(results, fileResult{base, &result{action: actionSkip, reason: reason}})
			continue
		}

		skipBySuffix := false
		for suffix, reason := range skipSuffixes {
			if strings.HasSuffix(base, suffix) {
				results = append(results, fileResult{base, &result{action: actionSkip, reason: reason}})
				skipBySuffix = true
				break
			}
		}
		if skipBySuffix {
			continue
		}

		r, stripped, err := processFile(srcFile)
		if err != nil {
			log.Fatalf("error processing %s: %v", base, err)
		}
		results = append(results, fileResult{base, r})
		for sym := range stripped {
			allStrippedSymbols[sym] = true
		}
	}

	// Pass 2: cross-file cascading — re-process files that may call
	// symbols stripped from other files
	crossFileCascade(results, allStrippedSymbols)

	// Write output
	var stats struct {
		copied, stripped, skipped int
	}

	for _, r := range results {
		switch r.result.action {
		case actionSkip:
			fmt.Printf("SKIP  %s (%s)\n", r.base, r.result.reason)
			stats.skipped++
		case actionCopy:
			if err := os.WriteFile(filepath.Join(*dst, r.base), r.result.content, 0644); err != nil {
				log.Fatalf("failed to write %s: %v", r.base, err)
			}
			fmt.Printf("COPY  %s\n", r.base)
			stats.copied++
		case actionStrip:
			if err := os.WriteFile(filepath.Join(*dst, r.base), r.result.content, 0644); err != nil {
				log.Fatalf("failed to write %s: %v", r.base, err)
			}
			fmt.Printf("STRIP %s (removed: %s)\n", r.base, r.result.reason)
			stats.stripped++
		}
	}

	fmt.Printf("\nDone: %d copied, %d stripped, %d skipped\n", stats.copied, stats.stripped, stats.skipped)
}

type fileResult struct {
	base   string
	result *result
}

type action int

const (
	actionCopy action = iota
	actionStrip
	actionSkip
)

type result struct {
	action  action
	content []byte
	reason  string
}

func processFile(path string) (*result, map[string]bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("parse error: %w", err)
	}

	bannedImports := findBannedImports(file)
	if len(bannedImports) == 0 {
		return &result{action: actionCopy, content: src}, nil, nil
	}

	bannedAliases := make(map[string]bool)
	for _, imp := range bannedImports {
		alias := importAlias(imp)
		bannedAliases[alias] = true
	}

	funcRemoveSet := make(map[*ast.FuncDecl]bool)
	genRemoveSet := make(map[*ast.GenDecl]bool)

	// Pass 1: mark declarations that directly reference banned packages
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if nodeUsesBannedPackage(d, bannedAliases) {
				funcRemoveSet[d] = true
			}
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			if nodeUsesBannedPackage(d, bannedAliases) {
				genRemoveSet[d] = true
			}
		}
	}

	// Pass 2: cascade — mark declarations that reference stripped symbols.
	cascadeRemovals(file, funcRemoveSet, genRemoveSet)

	if len(funcRemoveSet) == 0 && len(genRemoveSet) == 0 {
		return &result{action: actionCopy, content: src}, nil, nil
	}

	// Collect removed names for logging
	var removedNames []string
	for fn := range funcRemoveSet {
		if fn.Recv != nil {
			removedNames = append(removedNames, fmt.Sprintf("%s.%s", receiverType(fn), fn.Name.Name))
		} else {
			removedNames = append(removedNames, fn.Name.Name)
		}
	}
	for gd := range genRemoveSet {
		for _, spec := range gd.Specs {
			switch s := spec.(type) {
			case *ast.ValueSpec:
				for _, name := range s.Names {
					removedNames = append(removedNames, name.Name)
				}
			case *ast.TypeSpec:
				removedNames = append(removedNames, s.Name.Name)
			}
		}
	}

	// Check if everything non-import will be removed
	if countRemainingDecls(file, funcRemoveSet, genRemoveSet) == 0 {
		importPaths := make([]string, len(bannedImports))
		for i, imp := range bannedImports {
			importPaths[i] = strings.Trim(imp.Path.Value, `"`)
		}
		allSymbols := collectStrippedSymbols(funcRemoveSet, genRemoveSet)
		return &result{
			action: actionSkip,
			reason: fmt.Sprintf("all decls use banned imports: %s", strings.Join(importPaths, ", ")),
		}, allSymbols, nil
	}

	funcsToRemove := make([]*ast.FuncDecl, 0, len(funcRemoveSet))
	for fn := range funcRemoveSet {
		funcsToRemove = append(funcsToRemove, fn)
	}
	genDeclsToRemove := make([]*ast.GenDecl, 0, len(genRemoveSet))
	for gd := range genRemoveSet {
		genDeclsToRemove = append(genDeclsToRemove, gd)
	}

	allSymbols := collectStrippedSymbols(funcRemoveSet, genRemoveSet)
	content, err := stripDecls(fset, file, funcsToRemove, genDeclsToRemove)
	if err != nil {
		return nil, nil, err
	}

	return &result{
		action:  actionStrip,
		content: content,
		reason:  strings.Join(removedNames, ", "),
	}, allSymbols, nil
}

// crossFileCascade re-processes file results to remove declarations that
// reference symbols stripped from other files during Pass 1.
// It loops until no new symbols are discovered, so that symbols stripped
// from one file can trigger further removals in other files.
func crossFileCascade(results []fileResult, allStrippedSymbols map[string]bool) {
	if len(allStrippedSymbols) == 0 {
		return
	}
	for {
		outerChanged := false
		for i := range results {
			r := results[i]
			if r.result.action == actionSkip {
				continue
			}
			content := r.result.content
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, r.base, content, parser.ParseComments)
			if err != nil {
				log.Fatalf("failed to re-parse %s in cross-file cascade: %v", r.base, err)
			}

			var crossFuncRemove []*ast.FuncDecl
			var crossGenRemove []*ast.GenDecl
			var crossNames []string
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if funcDeclReferencesAny(d, allStrippedSymbols) {
						crossFuncRemove = append(crossFuncRemove, d)
						if d.Recv != nil {
							crossNames = append(crossNames, fmt.Sprintf("%s.%s", receiverType(d), d.Name.Name))
						} else {
							crossNames = append(crossNames, d.Name.Name)
						}
					}
				case *ast.GenDecl:
					if d.Tok == token.IMPORT {
						continue
					}
					if nodeReferencesAny(d, allStrippedSymbols) {
						crossGenRemove = append(crossGenRemove, d)
						for _, spec := range d.Specs {
							switch s := spec.(type) {
							case *ast.ValueSpec:
								for _, name := range s.Names {
									crossNames = append(crossNames, name.Name)
								}
							case *ast.TypeSpec:
								crossNames = append(crossNames, s.Name.Name)
							}
						}
					}
				}
			}
			if len(crossFuncRemove) == 0 && len(crossGenRemove) == 0 {
				continue
			}

			funcRemoveSet := make(map[*ast.FuncDecl]bool)
			for _, fn := range crossFuncRemove {
				funcRemoveSet[fn] = true
			}
			genRemoveSet := make(map[*ast.GenDecl]bool)
			for _, gd := range crossGenRemove {
				genRemoveSet[gd] = true
			}
			crossNames = append(crossNames, cascadeRemovals(file, funcRemoveSet, genRemoveSet)...)

			// Update allStrippedSymbols so subsequent files (and rounds) see newly
			// discovered symbols from this file's cascade.
			for _, name := range crossNames {
				if !allStrippedSymbols[name] {
					allStrippedSymbols[name] = true
					outerChanged = true
				}
			}

			if countRemainingDecls(file, funcRemoveSet, genRemoveSet) == 0 {
				results[i].result = &result{action: actionSkip, reason: "all remaining decls reference stripped symbols"}
				continue
			}

			funcsToRemove := make([]*ast.FuncDecl, 0, len(funcRemoveSet))
			for fn := range funcRemoveSet {
				funcsToRemove = append(funcsToRemove, fn)
			}
			genDeclsToRemove := make([]*ast.GenDecl, 0, len(genRemoveSet))
			for gd := range genRemoveSet {
				genDeclsToRemove = append(genDeclsToRemove, gd)
			}
			stripped, err := stripDecls(fset, file, funcsToRemove, genDeclsToRemove)
			if err != nil {
				log.Fatalf("stripDecls failed for %s: %v", r.base, err)
			}
			existingReason := r.result.reason
			if existingReason != "" {
				existingReason += ", "
			}
			results[i].result = &result{
				action:  actionStrip,
				content: stripped,
				reason:  existingReason + strings.Join(crossNames, ", "),
			}
		}
		if !outerChanged {
			break
		}
	}
}

// countRemainingDecls counts non-import declarations that will survive stripping.
func countRemainingDecls(file *ast.File, funcRemoveSet map[*ast.FuncDecl]bool, genRemoveSet map[*ast.GenDecl]bool) int {
	remaining := 0
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !funcRemoveSet[d] {
				remaining++
			}
		case *ast.GenDecl:
			if d.Tok != token.IMPORT && !genRemoveSet[d] {
				remaining++
			}
		}
	}
	return remaining
}

// collectStrippedSymbols builds a set of function/method names that have been
// marked for removal. For methods, both "Type.Method" and bare "Method" forms
// are included to catch method calls via variable receivers.
func collectStrippedSymbols(funcSet map[*ast.FuncDecl]bool, genSet map[*ast.GenDecl]bool) map[string]bool {
	symbols := make(map[string]bool)
	for fn := range funcSet {
		symbols[fn.Name.Name] = true
		if fn.Recv != nil {
			symbols[fmt.Sprintf("%s.%s", receiverType(fn), fn.Name.Name)] = true
		}
	}
	for gd := range genSet {
		for _, spec := range gd.Specs {
			switch s := spec.(type) {
			case *ast.ValueSpec:
				for _, name := range s.Names {
					symbols[name.Name] = true
				}
			case *ast.TypeSpec:
				symbols[s.Name.Name] = true
			}
		}
	}
	return symbols
}

// funcDeclReferencesAny checks if a function's receiver, signature, or body
// references any of the named symbols. It deliberately skips the function's own
// name so that e.g. init() in file B is not falsely stripped when init() in
// file A was stripped for a different reason.
func funcDeclReferencesAny(fn *ast.FuncDecl, symbols map[string]bool) bool {
	if fn.Recv != nil && nodeReferencesAny(fn.Recv, symbols) {
		return true
	}
	if fn.Type != nil && nodeReferencesAny(fn.Type, symbols) {
		return true
	}
	if fn.Body != nil && nodeReferencesAny(fn.Body, symbols) {
		return true
	}
	return false
}

// nodeReferencesAny checks if an AST node references any of the named symbols
// via any kind of identifier usage: calls, composite literals, type assertions,
// type conversions, variable declarations, etc. It skips identifiers that are
// in defining positions (struct field names, parameter names, var/const names,
// type names) to avoid false positives when a local name collides with a
// stripped symbol.
func nodeReferencesAny(node ast.Node, symbols map[string]bool) bool {
	defining := make(map[*ast.Ident]bool)
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Field:
			for _, name := range x.Names {
				defining[name] = true
			}
		case *ast.ValueSpec:
			for _, name := range x.Names {
				defining[name] = true
			}
		case *ast.TypeSpec:
			defining[x.Name] = true
		}
		return true
	})

	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if found {
			return false
		}
		ident, ok := n.(*ast.Ident)
		if ok && symbols[ident.Name] && !defining[ident] {
			found = true
		}
		return true
	})
	return found
}

// cascadeRemovals marks declarations for removal that reference symbols from
// already-removed declarations. Repeats until stable. Returns names of all
// newly removed symbols discovered during the cascade.
func cascadeRemovals(file *ast.File, funcRemoveSet map[*ast.FuncDecl]bool, genRemoveSet map[*ast.GenDecl]bool) []string {
	var newNames []string
	for {
		strippedSymbols := collectStrippedSymbols(funcRemoveSet, genRemoveSet)
		if len(strippedSymbols) == 0 {
			break
		}
		changed := false
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if funcRemoveSet[d] {
					continue
				}
				if funcDeclReferencesAny(d, strippedSymbols) {
					funcRemoveSet[d] = true
					changed = true
					if d.Recv != nil {
						newNames = append(newNames, fmt.Sprintf("%s.%s", receiverType(d), d.Name.Name))
					} else {
						newNames = append(newNames, d.Name.Name)
					}
				}
			case *ast.GenDecl:
				if d.Tok == token.IMPORT || genRemoveSet[d] {
					continue
				}
				if nodeReferencesAny(d, strippedSymbols) {
					genRemoveSet[d] = true
					changed = true
					for _, spec := range d.Specs {
						switch s := spec.(type) {
						case *ast.ValueSpec:
							for _, name := range s.Names {
								newNames = append(newNames, name.Name)
							}
						case *ast.TypeSpec:
							newNames = append(newNames, s.Name.Name)
						}
					}
				}
			}
		}
		if !changed {
			break
		}
	}
	return newNames
}

func findBannedImports(file *ast.File) []*ast.ImportSpec {
	var banned []*ast.ImportSpec
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if !isAllowedImport(path) {
			banned = append(banned, imp)
		}
	}
	return banned
}

func isAllowedImport(path string) bool {
	if !strings.Contains(path, ".") {
		return true
	}
	for _, prefix := range allowedImportPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func importAlias(imp *ast.ImportSpec) string {
	if imp.Name != nil {
		return imp.Name.Name
	}
	path := strings.Trim(imp.Path.Value, `"`)
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	// For versioned modules (e.g., "foo/v5"), use the second-to-last component.
	// Go module version suffixes are strictly "vN" (digits only after v);
	// names like "v1beta1" contain letters and are standard package names.
	if len(parts) > 1 && isModuleVersionSuffix(last) {
		return parts[len(parts)-2]
	}
	return last
}

func isModuleVersionSuffix(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func nodeUsesBannedPackage(node ast.Node, banned map[string]bool) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if found {
			return false
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if banned[ident.Name] {
			found = true
		}
		return true
	})
	return found
}

func receiverType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	t := fn.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if ident, ok := t.(*ast.Ident); ok {
		return ident.Name
	}
	return "?"
}

// stripDecls removes specified declarations and cleans up unused imports.
func stripDecls(fset *token.FileSet, file *ast.File, funcsToRemove []*ast.FuncDecl, genDeclsToRemove []*ast.GenDecl) ([]byte, error) {
	funcRemoveSet := make(map[*ast.FuncDecl]bool)
	for _, fn := range funcsToRemove {
		funcRemoveSet[fn] = true
	}
	genRemoveSet := make(map[*ast.GenDecl]bool)
	for _, gd := range genDeclsToRemove {
		genRemoveSet[gd] = true
	}

	// Collect Doc comment groups from removed declarations so we can
	// clean them from file.Comments after filtering.
	removedComments := make(map[*ast.CommentGroup]bool)
	for fn := range funcRemoveSet {
		if fn.Doc != nil {
			removedComments[fn.Doc] = true
		}
	}
	for gd := range genRemoveSet {
		if gd.Doc != nil {
			removedComments[gd.Doc] = true
		}
		for _, spec := range gd.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				if s.Doc != nil {
					removedComments[s.Doc] = true
				}
			case *ast.ValueSpec:
				if s.Doc != nil {
					removedComments[s.Doc] = true
				}
			}
		}
	}

	var filtered []ast.Decl
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if funcRemoveSet[d] {
				continue
			}
		case *ast.GenDecl:
			if genRemoveSet[d] {
				continue
			}
		}
		filtered = append(filtered, decl)
	}
	file.Decls = filtered

	// Remove orphaned comments from stripped declarations.
	if len(removedComments) > 0 {
		var keptComments []*ast.CommentGroup
		for _, cg := range file.Comments {
			if !removedComments[cg] {
				keptComments = append(keptComments, cg)
			}
		}
		file.Comments = keptComments
	}

	removeUnusedImports(file)

	var buf bytes.Buffer
	cfg := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}
	if err := cfg.Fprint(&buf, fset, file); err != nil {
		return nil, fmt.Errorf("failed to print AST: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format.Source failed (AST printer may have emitted invalid syntax): %w", err)
	}
	return formatted, nil
}

// removeUnusedImports prunes unused imports and drops empty import declarations.
// Blank and dot imports are preserved because they do not use package selectors.
func removeUnusedImports(file *ast.File) {
	// Collect all package identifiers still used in remaining declarations
	usedPkgs := collectUsedPackages(file)

	// Remove imports that are no longer used
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		var kept []ast.Spec
		for _, spec := range gd.Specs {
			imp, ok := spec.(*ast.ImportSpec)
			if !ok {
				kept = append(kept, spec)
				continue
			}
			alias := importAlias(imp)
			if (imp.Name != nil && imp.Name.Name == "_") ||
				(imp.Name != nil && imp.Name.Name == ".") ||
				usedPkgs[alias] {
				kept = append(kept, spec)
			}
		}
		gd.Specs = kept
	}

	// Drop empty import declarations left after pruning.
	var cleanedDecls []ast.Decl
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if ok && gd.Tok == token.IMPORT && len(gd.Specs) == 0 {
			continue
		}
		cleanedDecls = append(cleanedDecls, decl)
	}
	file.Decls = cleanedDecls
}

// collectUsedPackages walks the AST (excluding import blocks) and returns
// all identifier names used as the X in selector expressions (pkg.Symbol).
func collectUsedPackages(file *ast.File) map[string]bool {
	used := make(map[string]bool)
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if ok && gd.Tok == token.IMPORT {
			continue
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			used[ident.Name] = true
			return true
		})
	}
	return used
}

// ---------------------------------------------------------------------------
// Localize mode
//
// A provider module whose copied types still import sigs.k8s.io/cluster-api is
// not actually decoupled from it: Go resolves one version of a module per
// build, so those types always compile against whatever CAPI version the root
// module selects, no matter what the provider's own go.mod says.
//
// Localize breaks that link. It extracts the CAPI declarations a provider
// genuinely needs into a package inside the provider module and repoints the
// imports at it, leaving the provider module with no CAPI dependency at all.
// The extraction is driven by the CAPI version that provider resolves to, so
// each provider stays pinned to its own contract and can move independently.
//
// Only declarations reachable from type, const and var declarations are
// extracted, because those are what shape the generated CRDs. A function that
// reaches further into CAPI is stripped instead of dragging a much larger type
// closure along with it.
// ---------------------------------------------------------------------------

const capiImportPrefix = "sigs.k8s.io/cluster-api/"

// capiPackage is one parsed CAPI source package that a provider draws from.
type capiPackage struct {
	importPath string
	subPath    string
	name       string
	fset       *token.FileSet
	srcByFile  map[string][]byte
	fileOrder  []string
	astByFile  map[string]*ast.File
	// decls indexes every package-level symbol to the declaration that
	// introduces it, and to the individual spec when the declaration is a
	// grouped block.
	decls map[string]*capiDecl
}

type capiDecl struct {
	file string
	decl *ast.GenDecl
	spec ast.Spec
}

func runLocalize(dir, modulePath, capiRoot, capiAPIRoot, outName string) {
	providerFiles := collectProviderFiles(dir, outName)
	if len(providerFiles) == 0 {
		log.Fatalf("no Go files found under %s", dir)
	}

	seeds := collectSeeds(providerFiles)
	if len(seeds) == 0 {
		fmt.Println("localize: no CAPI references found; nothing to do")
		return
	}

	pkgs, closure, extraImports := computeClosure(capiRoot, capiAPIRoot, seeds)

	for pkgPath, syms := range closure {
		if len(syms) == 0 {
			continue
		}
		writeLocalizedPackage(dir, outName, pkgs[pkgPath], syms, extraImports[pkgPath])
	}

	rewritten, strippedTotal := 0, 0
	for _, path := range providerFiles {
		changed, removed := localizeFile(path, modulePath, outName, closure)
		if changed {
			rewritten++
		}
		strippedTotal += removed
	}

	fmt.Printf("localize: %d CAPI package(s) extracted, %d provider file(s) repointed, %d decl(s) stripped\n",
		len(closure), rewritten, strippedTotal)
}

// collectSeeds returns the CAPI symbols the provider's type, const and var
// declarations reference, keyed by package path. Function bodies are
// deliberately excluded: a function reaching further into CAPI is stripped
// rather than pulling a much larger type closure into the extraction.
func collectSeeds(providerFiles []string) map[string]map[string]bool {
	seeds := map[string]map[string]bool{}
	for _, path := range providerFiles {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			log.Fatalf("parse %s: %v", path, err)
		}
		aliases := capiAliases(file)
		if len(aliases) == 0 {
			continue
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok == token.IMPORT {
				continue
			}
			for pkgPath, syms := range capiRefs(gd, aliases) {
				if seeds[pkgPath] == nil {
					seeds[pkgPath] = map[string]bool{}
				}
				for s := range syms {
					seeds[pkgPath][s] = true
				}
			}
		}
	}
	return seeds
}

// computeClosure walks out from the seed symbols to everything they reference,
// loading CAPI packages on demand and following references across them. It
// returns the loaded packages, the symbols to extract from each, and the
// non-CAPI imports those declarations need.
func computeClosure(capiRoot, capiAPIRoot string, seeds map[string]map[string]bool) (
	map[string]*capiPackage, map[string]map[string]bool, map[string]map[string]string,
) {
	pkgs := map[string]*capiPackage{}
	closure := map[string]map[string]bool{}
	extraImports := map[string]map[string]string{} // pkgPath -> alias -> import path

	type work struct{ pkgPath, symbol string }
	var queue []work
	for pkgPath, syms := range seeds {
		for s := range syms {
			queue = append(queue, work{pkgPath, s})
		}
	}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if closure[item.pkgPath][item.symbol] {
			continue
		}
		pkg, ok := pkgs[item.pkgPath]
		if !ok {
			pkg = loadCAPIPackage(capiRoot, capiAPIRoot, item.pkgPath)
			pkgs[item.pkgPath] = pkg
		}
		d, ok := pkg.decls[item.symbol]
		if !ok {
			log.Fatalf("localize: %s.%s not found in %s", item.pkgPath, item.symbol, pkg.subPath)
		}
		if closure[item.pkgPath] == nil {
			closure[item.pkgPath] = map[string]bool{}
		}
		closure[item.pkgPath][item.symbol] = true

		node := ast.Node(d.decl)
		if d.spec != nil {
			node = d.spec
		}
		srcFile := pkg.astByFile[d.file]
		fileAliases := capiAliases(srcFile)
		otherImports := nonCAPIImports(srcFile)

		ast.Inspect(node, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				id, ok := x.X.(*ast.Ident)
				if !ok {
					return true
				}
				if dep, ok := fileAliases[id.Name]; ok {
					queue = append(queue, work{dep, x.Sel.Name})
					return false
				}
				if path, ok := otherImports[id.Name]; ok {
					if extraImports[item.pkgPath] == nil {
						extraImports[item.pkgPath] = map[string]string{}
					}
					extraImports[item.pkgPath][id.Name] = path
				}
				return false
			case *ast.Ident:
				if _, ok := pkg.decls[x.Name]; ok {
					queue = append(queue, work{item.pkgPath, x.Name})
				}
			}
			return true
		})
	}
	return pkgs, closure, extraImports
}

// collectProviderFiles lists the module's Go sources, skipping the directory
// the extracted CAPI types are written to.
func collectProviderFiles(dir, outName string) []string {
	var out []string
	outDir := filepath.Join(dir, outName)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path == outDir {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		log.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(out)
	return out
}

// capiAliases maps each local import alias to the CAPI package it refers to.
func capiAliases(file *ast.File) map[string]string {
	aliases := map[string]string{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if strings.HasPrefix(path, capiImportPrefix) {
			aliases[importAlias(imp)] = path
		}
	}
	return aliases
}

func nonCAPIImports(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if !strings.HasPrefix(path, capiImportPrefix) {
			out[importAlias(imp)] = path
		}
	}
	return out
}

// capiRefs returns the CAPI symbols a node references, keyed by package path.
func capiRefs(node ast.Node, aliases map[string]string) map[string]map[string]bool {
	refs := map[string]map[string]bool{}
	ast.Inspect(node, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		pkgPath, ok := aliases[id.Name]
		if !ok {
			return true
		}
		if refs[pkgPath] == nil {
			refs[pkgPath] = map[string]bool{}
		}
		refs[pkgPath][sel.Sel.Name] = true
		return false
	})
	return refs
}

func capiSourceDir(capiRoot, capiAPIRoot, importPath string) string {
	if capiAPIRoot != "" && strings.HasPrefix(importPath, capiImportPrefix+"api/") {
		return filepath.Join(capiAPIRoot, strings.TrimPrefix(importPath, capiImportPrefix+"api/"))
	}
	return filepath.Join(capiRoot, strings.TrimPrefix(importPath, capiImportPrefix))
}

func loadCAPIPackage(capiRoot, capiAPIRoot, importPath string) *capiPackage {
	subPath := strings.TrimPrefix(importPath, capiImportPrefix)
	srcDir := capiSourceDir(capiRoot, capiAPIRoot, importPath)
	entries, err := filepath.Glob(filepath.Join(srcDir, "*.go"))
	if err != nil || len(entries) == 0 {
		log.Fatalf("localize: no sources for %s under %s", importPath, srcDir)
	}
	sort.Strings(entries)

	pkg := &capiPackage{
		importPath: importPath,
		subPath:    subPath,
		fset:       token.NewFileSet(),
		srcByFile:  map[string][]byte{},
		astByFile:  map[string]*ast.File{},
		decls:      map[string]*capiDecl{},
	}

	for _, path := range entries {
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("read %s: %v", path, err)
		}
		file, err := pkg.parse(path, src)
		if err != nil {
			log.Fatalf("parse %s: %v", path, err)
		}
		pkg.name = file.Name.Name
		pkg.srcByFile[base] = src
		pkg.astByFile[base] = file
		pkg.fileOrder = append(pkg.fileOrder, base)

		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok == token.IMPORT {
				continue
			}
			single := len(gd.Specs) == 1
			for _, spec := range gd.Specs {
				var names []string
				switch s := spec.(type) {
				case *ast.TypeSpec:
					names = []string{s.Name.Name}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						names = append(names, n.Name)
					}
				}
				for _, name := range names {
					ref := &capiDecl{file: base, decl: gd}
					if !single {
						ref.spec = spec
					}
					pkg.decls[name] = ref
				}
			}
		}
	}
	return pkg
}

func (p *capiPackage) parse(path string, src []byte) (*ast.File, error) {
	return parser.ParseFile(p.fset, path, src, parser.ParseComments)
}

// writeLocalizedPackage emits the selected declarations as a single generated
// file. Declaration text is sliced verbatim from the CAPI source so that doc
// comments and kubebuilder markers survive, keeping the generated CRD schema
// identical to what the upstream types produce.
func writeLocalizedPackage(dir, outName string, pkg *capiPackage, symbols map[string]bool, imports map[string]string) {
	type chunk struct {
		file  string
		order int
		text  string
	}

	seen := map[*ast.GenDecl]bool{}
	seenSpec := map[ast.Spec]bool{}
	var chunks []chunk

	for _, name := range sortedKeys(symbols) {
		d := pkg.decls[name]
		src := pkg.srcByFile[d.file]

		if d.spec == nil {
			if seen[d.decl] {
				continue
			}
			seen[d.decl] = true
			chunks = append(chunks, chunk{d.file, pkg.offset(d.decl.Pos()), pkg.slice(src, declStart(d.decl), d.decl.End())})
			continue
		}
		if seenSpec[d.spec] {
			continue
		}
		seenSpec[d.spec] = true
		text := pkg.slice(src, specStart(d.spec), d.spec.End())
		chunks = append(chunks, chunk{d.file, pkg.offset(d.spec.Pos()), fmt.Sprintf("%s (\n%s\n)", d.decl.Tok, indent(text))})
	}

	fileRank := map[string]int{}
	for i, f := range pkg.fileOrder {
		fileRank[f] = i
	}
	sort.SliceStable(chunks, func(i, j int) bool {
		if chunks[i].file != chunks[j].file {
			return fileRank[chunks[i].file] < fileRank[chunks[j].file]
		}
		return chunks[i].order < chunks[j].order
	})

	var buf bytes.Buffer
	buf.WriteString("// Code generated by copy-capi-types. DO NOT EDIT.\n//\n")
	fmt.Fprintf(&buf, "// Extracted from %s so that this module carries no dependency on\n", pkg.importPath)
	buf.WriteString("// sigs.k8s.io/cluster-api. Refresh with 'make capi-sync-force'.\n\n")
	buf.WriteString("// +kubebuilder:object:generate=true\n")
	fmt.Fprintf(&buf, "package %s\n", pkg.name)

	if len(imports) > 0 {
		buf.WriteString("\nimport (\n")
		for _, alias := range sortedKeys(toSet(imports)) {
			fmt.Fprintf(&buf, "\t%s %q\n", alias, imports[alias])
		}
		buf.WriteString(")\n")
	}
	for _, c := range chunks {
		buf.WriteString("\n")
		buf.WriteString(c.text)
		buf.WriteString("\n")
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		log.Fatalf("localize: formatting extracted %s failed: %v", pkg.importPath, err)
	}

	destDir := filepath.Join(dir, outName, pkg.subPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		log.Fatalf("mkdir %s: %v", destDir, err)
	}
	dest := filepath.Join(destDir, "zz_generated.capi_types.go")
	if err := os.WriteFile(dest, formatted, 0644); err != nil {
		log.Fatalf("write %s: %v", dest, err)
	}
	fmt.Printf("CAPI   %s -> %s (%d symbols)\n", pkg.importPath, filepath.Join(outName, pkg.subPath), len(symbols))
}

func (p *capiPackage) offset(pos token.Pos) int { return p.fset.Position(pos).Offset }

func (p *capiPackage) slice(src []byte, start, end token.Pos) string {
	return strings.TrimRight(string(src[p.offset(start):p.offset(end)]), " \t\n")
}

func declStart(d *ast.GenDecl) token.Pos {
	if d.Doc != nil {
		return d.Doc.Pos()
	}
	return d.Pos()
}

func specStart(spec ast.Spec) token.Pos {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		if s.Doc != nil {
			return s.Doc.Pos()
		}
	case *ast.ValueSpec:
		if s.Doc != nil {
			return s.Doc.Pos()
		}
	}
	return spec.Pos()
}

func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = "\t" + l
		}
	}
	return strings.Join(lines, "\n")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func toSet(m map[string]string) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// localizeFile repoints a provider file's CAPI imports at the extracted
// package, stripping any declaration that reaches a CAPI symbol which was not
// extracted. Returns whether the file changed and how many decls were removed.
func localizeFile(path, modulePath, outName string, closure map[string]map[string]bool) (bool, int) {
	src, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read %s: %v", path, err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		log.Fatalf("parse %s: %v", path, err)
	}

	aliases := capiAliases(file)
	if len(aliases) == 0 {
		return false, 0
	}

	funcRemove := map[*ast.FuncDecl]bool{}
	genRemove := map[*ast.GenDecl]bool{}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if usesUnextractedCAPI(d, aliases, closure) {
				funcRemove[d] = true
			}
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			if usesUnextractedCAPI(d, aliases, closure) {
				genRemove[d] = true
			}
		}
	}
	removed := len(funcRemove) + len(genRemove)
	if removed > 0 {
		cascadeRemovals(file, funcRemove, genRemove)
		removed = len(funcRemove) + len(genRemove)
		var funcs []*ast.FuncDecl
		for fn := range funcRemove {
			funcs = append(funcs, fn)
		}
		var gens []*ast.GenDecl
		for gd := range genRemove {
			gens = append(gens, gd)
		}
		stripped, err := stripDecls(fset, file, funcs, gens)
		if err != nil {
			log.Fatalf("localize: stripping %s: %v", path, err)
		}
		fset = token.NewFileSet()
		file, err = parser.ParseFile(fset, path, stripped, parser.ParseComments)
		if err != nil {
			log.Fatalf("localize: re-parsing stripped %s: %v", path, err)
		}
	}

	for _, imp := range file.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if !strings.HasPrefix(p, capiImportPrefix) {
			continue
		}
		local := modulePath + "/" + outName + "/" + strings.TrimPrefix(p, capiImportPrefix)
		imp.Path.Value = strconv.Quote(local)
	}

	removeUnusedImports(file)

	var buf bytes.Buffer
	cfg := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}
	if err := cfg.Fprint(&buf, fset, file); err != nil {
		log.Fatalf("localize: printing %s: %v", path, err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		log.Fatalf("localize: formatting %s: %v", path, err)
	}
	if bytes.Equal(formatted, src) {
		return false, removed
	}
	if err := os.WriteFile(path, formatted, 0644); err != nil {
		log.Fatalf("write %s: %v", path, err)
	}
	return true, removed
}

// usesUnextractedCAPI reports whether a node references a CAPI symbol that was
// not pulled into the localized package.
func usesUnextractedCAPI(node ast.Node, aliases map[string]string, closure map[string]map[string]bool) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if found {
			return false
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		pkgPath, ok := aliases[id.Name]
		if !ok {
			return true
		}
		if !closure[pkgPath][sel.Sel.Name] {
			found = true
		}
		return false
	})
	return found
}
