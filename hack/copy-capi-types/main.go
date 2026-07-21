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
	flag.Parse()

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
	remainingDecls := 0
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !funcRemoveSet[d] {
				remainingDecls++
			}
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			if !genRemoveSet[d] {
				remainingDecls++
			}
		}
	}

	if remainingDecls == 0 {
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

			if remaining == 0 {
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
