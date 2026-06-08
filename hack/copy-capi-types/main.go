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
	var rewriteRules stringSlice
	flag.Var(&rewriteRules, "rewrite", "rewrite import prefix old=new (can be repeated)")
	flag.Parse()

	for _, prefix := range extraAllowed {
		allowedImportPrefixes = append(allowedImportPrefixes, prefix)
	}

	rewrites := parseRewriteRules(rewriteRules)

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

	// Pass 1: process each file individually (within-file cascading)
	type fileResult struct {
		base   string
		result *result
	}
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
	if len(allStrippedSymbols) > 0 {
		for i := range results {
			r := results[i]
			if r.result.action == actionSkip {
				continue
			}
			// Re-parse the content (original or already-stripped) and check
			// for calls to cross-file stripped symbols
			content := r.result.content
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, r.base, content, parser.ParseComments)
			if err != nil {
				continue
			}

			var crossFileRemove []*ast.FuncDecl
			var crossNames []string
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				if callsAny(fn, allStrippedSymbols) {
					crossFileRemove = append(crossFileRemove, fn)
					if fn.Recv != nil {
						crossNames = append(crossNames, fmt.Sprintf("%s.%s", receiverType(fn), fn.Name.Name))
					} else {
						crossNames = append(crossNames, fn.Name.Name)
					}
				}
			}
			if len(crossFileRemove) == 0 {
				continue
			}

			// Do another round of cascading within this file
			funcRemoveSet := make(map[*ast.FuncDecl]bool)
			for _, fn := range crossFileRemove {
				funcRemoveSet[fn] = true
			}
			for {
				localStripped := make(map[string]bool)
				for fn := range funcRemoveSet {
					localStripped[fn.Name.Name] = true
				}
				changed := false
				for _, decl := range file.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || funcRemoveSet[fn] {
						continue
					}
					if callsAny(fn, localStripped) {
						funcRemoveSet[fn] = true
						changed = true
						if fn.Recv != nil {
							crossNames = append(crossNames, fmt.Sprintf("%s.%s", receiverType(fn), fn.Name.Name))
						} else {
							crossNames = append(crossNames, fn.Name.Name)
						}
					}
				}
				if !changed {
					break
				}
			}

			// Check if everything will be removed
			remaining := 0
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if !funcRemoveSet[d] {
						remaining++
					}
				case *ast.GenDecl:
					if d.Tok != token.IMPORT {
						remaining++
					}
				}
			}

			if remaining == 0 {
				results[i].result = &result{action: actionSkip, reason: "all remaining decls call stripped symbols"}
				continue
			}

			stripped := stripDecls(fset, file, crossFileRemove, nil, nil)
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
	}

	// Apply import path rewrites
	if len(rewrites) > 0 {
		for i := range results {
			r := results[i]
			if r.result.action == actionSkip {
				continue
			}
			results[i].result.content = applyRewriteRules(r.result.content, rewrites)
		}
	}

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
	// Handles both FuncDecl and GenDecl (type/var/const). Repeat until stable.
	for {
		strippedSymbols := collectStrippedSymbols(file, funcRemoveSet, genRemoveSet)
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
				if callsAny(d, strippedSymbols) {
					funcRemoveSet[d] = true
					changed = true
				}
			case *ast.GenDecl:
				if d.Tok == token.IMPORT || genRemoveSet[d] {
					continue
				}
				if genDeclReferencesAny(d, strippedSymbols) {
					genRemoveSet[d] = true
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}

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
		allSymbols := collectStrippedSymbols(file, funcRemoveSet, genRemoveSet)
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

	allSymbols := collectStrippedSymbols(file, funcRemoveSet, genRemoveSet)
	content := stripDecls(fset, file, funcsToRemove, genDeclsToRemove, bannedAliases)

	return &result{
		action:  actionStrip,
		content: content,
		reason:  strings.Join(removedNames, ", "),
	}, allSymbols, nil
}

// collectStrippedSymbols builds a set of function/method names that have been
// marked for removal. For methods, both "Type.Method" and bare "Method" forms
// are included to catch method calls via variable receivers.
func collectStrippedSymbols(_ *ast.File, funcSet map[*ast.FuncDecl]bool, genSet map[*ast.GenDecl]bool) map[string]bool {
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

// callsAny checks if a function body calls any of the named symbols.
func callsAny(fn *ast.FuncDecl, symbols map[string]bool) bool {
	if fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch expr := n.(type) {
		case *ast.CallExpr:
			switch callee := expr.Fun.(type) {
			case *ast.Ident:
				if symbols[callee.Name] {
					found = true
				}
			case *ast.SelectorExpr:
				if symbols[callee.Sel.Name] {
					found = true
				}
			}
		}
		return true
	})
	return found
}

// genDeclReferencesAny checks if a GenDecl (type/var/const) references any of
// the named symbols in its initializer expressions or type definitions.
func genDeclReferencesAny(gd *ast.GenDecl, symbols map[string]bool) bool {
	found := false
	ast.Inspect(gd, func(n ast.Node) bool {
		if found {
			return false
		}
		ident, ok := n.(*ast.Ident)
		if ok && symbols[ident.Name] {
			found = true
		}
		return true
	})
	return found
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
	// For versioned modules (e.g., "foo/v5"), use the second-to-last component
	if len(parts) > 1 && len(last) > 1 && last[0] == 'v' && last[1] >= '0' && last[1] <= '9' {
		return parts[len(parts)-2]
	}
	return last
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
func stripDecls(fset *token.FileSet, file *ast.File, funcsToRemove []*ast.FuncDecl, genDeclsToRemove []*ast.GenDecl, _ map[string]bool) []byte {
	funcRemoveSet := make(map[*ast.FuncDecl]bool)
	for _, fn := range funcsToRemove {
		funcRemoveSet[fn] = true
	}
	genRemoveSet := make(map[*ast.GenDecl]bool)
	for _, gd := range genDeclsToRemove {
		genRemoveSet[gd] = true
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
			path := strings.Trim(imp.Path.Value, `"`)
			// Keep if: it's a blank import, a dot import, or actively used
			if (imp.Name != nil && imp.Name.Name == "_") ||
				(imp.Name != nil && imp.Name.Name == ".") ||
				usedPkgs[alias] {
				kept = append(kept, spec)
				continue
			}
			// Also keep standard library imports that are used
			if !strings.Contains(path, ".") && usedPkgs[alias] {
				kept = append(kept, spec)
				continue
			}
			// Drop unused imports
			if !usedPkgs[alias] {
				continue
			}
			kept = append(kept, spec)
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
		log.Fatalf("failed to print AST: %v", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return buf.Bytes()
	}
	return formatted
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

type rewriteRule struct {
	oldPrefix string
	newPrefix string
}

func parseRewriteRules(rules []string) []rewriteRule {
	var result []rewriteRule
	for _, r := range rules {
		parts := strings.SplitN(r, "=", 2)
		if len(parts) != 2 {
			log.Fatalf("invalid --rewrite value %q: expected old=new", r)
		}
		result = append(result, rewriteRule{oldPrefix: parts[0], newPrefix: parts[1]})
	}
	return result
}

func applyRewriteRules(content []byte, rules []rewriteRule) []byte {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", content, parser.ParseComments)
	if err != nil {
		return content
	}

	changed := false
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		for _, rule := range rules {
			if strings.HasPrefix(path, rule.oldPrefix) {
				newPath := rule.newPrefix + path[len(rule.oldPrefix):]
				imp.Path.Value = `"` + newPath + `"`
				changed = true
				break
			}
		}
	}

	if !changed {
		return content
	}

	var buf bytes.Buffer
	cfg := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}
	if err := cfg.Fprint(&buf, fset, file); err != nil {
		return content
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return buf.Bytes()
	}
	return formatted
}
