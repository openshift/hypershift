package testfuncstructure

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"

	"k8s.io/apimachinery/pkg/util/sets"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name:      "testfuncstructure",
	Doc:       "checks that unit tests map one-to-one to production functions and methods",
	Run:       run,
	FactTypes: []analysis.Fact{new(productionMetadata)},
}

type productionSymbol struct {
	id            string
	display       string
	canonicalTest string
	variants      []testNameVariant
}

type testNameVariant struct {
	name     string
	priority int
}

type testFunction struct {
	decl       *ast.FuncDecl
	filename   string
	name       string
	namePos    token.Pos
	nameEnd    token.Pos
	imported   bool
	suppressed bool
	resolution resolution
}

type resolution struct {
	kind       resolutionKind
	target     *productionSymbol
	candidates []*productionSymbol
	priority   int
}

type resolutionKind int

const (
	resolutionIgnored resolutionKind = iota
	resolutionExact
	resolutionScenario
	resolutionDisconnected
	resolutionAmbiguous
)

type symbolMatch struct {
	symbol    *productionSymbol
	kind      resolutionKind
	prefixLen int
	priority  int
}

var excludedBuildTags = sets.New("e2e", "e2ev2", "envtest", "integration", "reqserving")

func run(pass *analysis.Pass) (any, error) {
	symbols := collectProductionSymbols(pass)
	symbolsByID := make(map[string]*productionSymbol, len(symbols))
	for _, symbol := range symbols {
		symbolsByID[symbol.id] = symbol
	}

	tests := collectTests(pass)
	groups := map[string][]*testFunction{}
	for _, test := range tests {
		test.resolution = resolveTest(pass, test, symbols, symbolsByID)
		switch test.resolution.kind {
		case resolutionExact, resolutionScenario, resolutionDisconnected:
			groups[test.resolution.target.id] = append(groups[test.resolution.target.id], test)
		case resolutionAmbiguous:
			reportAmbiguous(pass, test)
			if isLegacyException(pass, test) {
				for _, candidate := range test.resolution.candidates {
					legacyCandidate := *test
					legacyCandidate.resolution = resolution{kind: resolutionDisconnected, target: candidate}
					groups[candidate.id] = append(groups[candidate.id], &legacyCandidate)
				}
			}
		}
	}
	for _, test := range collectImportedTests(pass, symbolsByID) {
		groups[test.resolution.target.id] = append(groups[test.resolution.target.id], test)
	}

	groupIDs := make([]string, 0, len(groups))
	for id := range groups {
		groupIDs = append(groupIDs, id)
	}
	slices.Sort(groupIDs)
	for _, id := range groupIDs {
		reportGroup(pass, groups[id])
	}
	if !strings.HasSuffix(pass.Pkg.Name(), "_test") && len(symbols) > 0 {
		exportProductionMetadata(pass, symbols, groups)
	}

	return nil, nil
}

func collectProductionSymbols(pass *analysis.Pass) []*productionSymbol {
	seen := map[string]*productionSymbol{}
	if target := externalPackageUnderTest(pass); target != nil {
		var metadata productionMetadata
		if pass.ImportPackageFact(target, &metadata) {
			collectExportedSymbols(target, seen)
			productionIDs := sets.New(metadata.SymbolIDs...)
			for id := range seen {
				if !productionIDs.Has(id) {
					delete(seen, id)
				}
			}
		}
	} else {
		for _, file := range pass.Files {
			filename := pass.Fset.File(file.Pos()).Name()
			if strings.HasSuffix(filename, "_test.go") || ast.IsGenerated(file) {
				continue
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				obj, ok := pass.TypesInfo.Defs[fn.Name].(*types.Func)
				if ok {
					addProductionSymbol(seen, obj)
				}
			}
		}
	}

	symbols := make([]*productionSymbol, 0, len(seen))
	for _, symbol := range seen {
		symbols = append(symbols, symbol)
	}
	slices.SortFunc(symbols, func(a, b *productionSymbol) int {
		return strings.Compare(a.id, b.id)
	})
	return symbols
}

func externalPackageUnderTest(pass *analysis.Pass) *types.Package {
	if !strings.HasSuffix(pass.Pkg.Name(), "_test") {
		return nil
	}

	name := strings.TrimSuffix(pass.Pkg.Name(), "_test")
	wantPath := strings.TrimSuffix(pass.Pkg.Path(), "_test")
	for _, imported := range pass.Pkg.Imports() {
		if imported.Name() == name && imported.Path() == wantPath {
			return imported
		}
	}
	return nil
}

func collectExportedSymbols(pkg *types.Package, seen map[string]*productionSymbol) {
	for _, name := range pkg.Scope().Names() {
		obj := pkg.Scope().Lookup(name)
		switch obj := obj.(type) {
		case *types.Func:
			if obj.Exported() {
				addProductionSymbol(seen, obj)
			}
		case *types.TypeName:
			named, ok := types.Unalias(obj.Type()).(*types.Named)
			if !ok {
				continue
			}
			collectExportedMethods(pkg, named, types.NewMethodSet(named), seen)
			collectExportedMethods(pkg, named, types.NewMethodSet(types.NewPointer(named)), seen)
		}
	}
}

func collectExportedMethods(pkg *types.Package, receiver *types.Named, methodSet *types.MethodSet, seen map[string]*productionSymbol) {
	for method := range methodSet.Methods() {
		fn, ok := method.Obj().(*types.Func)
		if !ok || !fn.Exported() || fn.Pkg() != pkg || receiverName(fn) != receiver.Obj().Name() {
			continue
		}
		addProductionSymbol(seen, fn)
	}
}

func addProductionSymbol(seen map[string]*productionSymbol, fn *types.Func) {
	if fn.Pkg() == nil {
		return
	}

	receiver := receiverName(fn)
	testReceiver := upperFirst(receiver)
	name := upperFirst(fn.Name())
	display := fn.Name()
	canonical := "Test" + name
	variants := []testNameVariant{{name: canonical, priority: 3}}
	if receiver != "" {
		display = receiver + "." + fn.Name()
		canonical = "Test" + testReceiver + "_" + name
		variants = []testNameVariant{
			{name: canonical, priority: 3},
			{name: "Test" + testReceiver + name, priority: 2},
			{name: "Test" + name, priority: 1},
		}
	}

	id := symbolID(fn)
	seen[id] = &productionSymbol{
		id:            id,
		display:       display,
		canonicalTest: canonical,
		variants:      variants,
	}
}

func receiverName(fn *types.Func) string {
	signature, ok := fn.Type().(*types.Signature)
	if !ok || signature.Recv() == nil {
		return ""
	}
	typ := types.Unalias(signature.Recv().Type())
	if pointer, ok := typ.(*types.Pointer); ok {
		typ = types.Unalias(pointer.Elem())
	}
	named, ok := typ.(*types.Named)
	if !ok {
		return ""
	}
	return named.Obj().Name()
}

func symbolID(fn *types.Func) string {
	receiver := receiverName(fn)
	if receiver == "" {
		return fn.Pkg().Path() + "." + fn.Name()
	}
	return fn.Pkg().Path() + "." + receiver + "." + fn.Name()
}

func upperFirst(value string) string {
	r, size := utf8.DecodeRuneInString(value)
	if r == utf8.RuneError && size == 0 {
		return value
	}
	return string(unicode.ToUpper(r)) + value[size:]
}

func collectTests(pass *analysis.Pass) []*testFunction {
	var tests []*testFunction
	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()
		if !isUnitTestFile(filename, file) {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !isOrdinaryTest(pass, fn) {
				continue
			}
			tests = append(tests, &testFunction{
				decl:       fn,
				filename:   filename,
				name:       fn.Name.Name,
				namePos:    fn.Name.Pos(),
				nameEnd:    fn.Name.End(),
				suppressed: hasNoLintDirective(fn),
			})
		}
	}
	slices.SortFunc(tests, func(a, b *testFunction) int {
		if result := strings.Compare(a.filename, b.filename); result != 0 {
			return result
		}
		return cmpPos(a.namePos, b.namePos)
	})
	return tests
}

func isUnitTestFile(filename string, file *ast.File) bool {
	if !pathutil.IsUnitTest(filename) || ast.IsGenerated(file) {
		return false
	}

	normalized := filepath.ToSlash(filename)
	for _, excludedPath := range []string{"/test/envtest/", "/test/reqserving-e2e/"} {
		if strings.Contains(normalized, excludedPath) {
			return false
		}
	}
	return !hasExcludedBuildTag(file)
}

func hasExcludedBuildTag(file *ast.File) bool {
	var plusBuildExpressions []constraint.Expr
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, comment := range group.List {
			switch {
			case constraint.IsGoBuild(comment.Text):
				expression, err := constraint.Parse(comment.Text)
				return err == nil && requiresExcludedBuildTag(expression)
			case constraint.IsPlusBuild(comment.Text):
				expression, err := constraint.Parse(comment.Text)
				if err == nil {
					plusBuildExpressions = append(plusBuildExpressions, expression)
				}
			}
		}
	}
	if len(plusBuildExpressions) == 0 {
		return false
	}
	expression := plusBuildExpressions[0]
	for _, next := range plusBuildExpressions[1:] {
		expression = &constraint.AndExpr{X: expression, Y: next}
	}
	return requiresExcludedBuildTag(expression)
}

func requiresExcludedBuildTag(expression constraint.Expr) bool {
	otherTags := sets.New[string]()
	collectOtherBuildTags(expression, otherTags)
	if len(otherTags) > 16 {
		return false
	}

	tags := sets.List(otherTags)
	for assignment := range 1 << len(tags) {
		values := make(map[string]bool, len(tags))
		for index, tag := range tags {
			values[tag] = assignment&(1<<index) != 0
		}
		if expression.Eval(func(tag string) bool {
			if excludedBuildTags.Has(tag) {
				return false
			}
			return values[tag]
		}) {
			return false
		}
	}
	return true
}

func collectOtherBuildTags(expression constraint.Expr, tags sets.Set[string]) {
	switch expression := expression.(type) {
	case *constraint.TagExpr:
		if !excludedBuildTags.Has(expression.Tag) {
			tags.Insert(expression.Tag)
		}
	case *constraint.NotExpr:
		collectOtherBuildTags(expression.X, tags)
	case *constraint.AndExpr:
		collectOtherBuildTags(expression.X, tags)
		collectOtherBuildTags(expression.Y, tags)
	case *constraint.OrExpr:
		collectOtherBuildTags(expression.X, tags)
		collectOtherBuildTags(expression.Y, tags)
	}
}

func isOrdinaryTest(pass *analysis.Pass, fn *ast.FuncDecl) bool {
	if fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || strings.HasPrefix(fn.Name.Name, "Test_") {
		return false
	}
	suffix := strings.TrimPrefix(fn.Name.Name, "Test")
	first, _ := utf8.DecodeRuneInString(suffix)
	if suffix != "" && unicode.IsLower(first) {
		return false
	}

	obj, ok := pass.TypesInfo.Defs[fn.Name].(*types.Func)
	if !ok {
		return false
	}
	signature, ok := obj.Type().(*types.Signature)
	if !ok || signature.Recv() != nil || signature.Params().Len() != 1 || signature.Results().Len() != 0 || signature.TypeParams().Len() != 0 {
		return false
	}
	return isTestingT(signature.Params().At(0).Type())
}

func isTestingT(typ types.Type) bool {
	pointer, ok := typ.(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := pointer.Elem().(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "testing" && named.Obj().Name() == "T"
}

func hasNoLintDirective(fn *ast.FuncDecl) bool {
	if fn.Doc == nil {
		return false
	}
	for _, comment := range fn.Doc.List {
		text := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(comment.Text, "/*"), "*/"))
		text = strings.TrimSpace(strings.TrimPrefix(text, "//"))
		if text == "nolint" {
			return true
		}
		if !strings.HasPrefix(text, "nolint:") {
			continue
		}
		directive := strings.Fields(text)[0]
		for linter := range strings.SplitSeq(strings.TrimPrefix(directive, "nolint:"), ",") {
			if linter == "hypershiftlinter" {
				return true
			}
		}
	}
	return false
}

func resolveTest(pass *analysis.Pass, test *testFunction, symbols []*productionSymbol, symbolsByID map[string]*productionSymbol) resolution {
	called := calledProductionSymbols(pass, test.decl, symbolsByID)
	var exactMatches, scenarioMatches []symbolMatch
	for _, symbol := range symbols {
		match, ok := matchSymbol(test.name, symbol)
		if !ok {
			continue
		}
		if match.kind == resolutionExact {
			exactMatches = append(exactMatches, match)
		} else {
			scenarioMatches = append(scenarioMatches, match)
		}
	}

	if len(exactMatches) > 0 {
		return resolveMatches(highestPriorityMatches(exactMatches), called)
	}
	if len(scenarioMatches) > 0 {
		longest := 0
		for _, match := range scenarioMatches {
			longest = max(longest, match.prefixLen)
		}
		scenarioMatches = slices.DeleteFunc(scenarioMatches, func(match symbolMatch) bool {
			return match.prefixLen != longest
		})
		return resolveMatches(highestPriorityMatches(scenarioMatches), called)
	}

	calledSymbols := make([]*productionSymbol, 0, len(called))
	for id := range called {
		calledSymbols = append(calledSymbols, symbolsByID[id])
	}
	slices.SortFunc(calledSymbols, func(a, b *productionSymbol) int {
		return strings.Compare(a.id, b.id)
	})
	if len(calledSymbols) == 1 {
		return resolution{kind: resolutionDisconnected, target: calledSymbols[0]}
	}
	if len(calledSymbols) > 1 {
		return resolution{kind: resolutionAmbiguous, candidates: calledSymbols}
	}
	return resolution{kind: resolutionIgnored}
}

func matchSymbol(testName string, symbol *productionSymbol) (symbolMatch, bool) {
	var best symbolMatch
	found := false
	for _, variant := range symbol.variants {
		kind, ok := matchTestName(testName, variant.name)
		if !ok {
			continue
		}
		candidate := symbolMatch{
			symbol:    symbol,
			kind:      kind,
			prefixLen: len(variant.name),
			priority:  variant.priority,
		}
		if !found || betterMatch(candidate, best) {
			best = candidate
			found = true
		}
	}
	return best, found
}

func matchTestName(testName, productionTestName string) (resolutionKind, bool) {
	if testName == productionTestName {
		return resolutionExact, true
	}
	if len(testName) <= len(productionTestName) || !strings.HasPrefix(testName, productionTestName) {
		return resolutionIgnored, false
	}
	remainder := testName[len(productionTestName):]
	first, _ := utf8.DecodeRuneInString(remainder)
	if first != '_' && !unicode.IsUpper(first) && !unicode.IsDigit(first) {
		return resolutionIgnored, false
	}
	return resolutionScenario, true
}

func highestPriorityMatches(matches []symbolMatch) []symbolMatch {
	highest := 0
	for _, match := range matches {
		highest = max(highest, match.priority)
	}
	return slices.DeleteFunc(matches, func(match symbolMatch) bool {
		return match.priority != highest
	})
}

func betterMatch(candidate, current symbolMatch) bool {
	if candidate.kind != current.kind {
		return candidate.kind == resolutionExact
	}
	if candidate.prefixLen != current.prefixLen {
		return candidate.prefixLen > current.prefixLen
	}
	return candidate.priority > current.priority
}

func resolveMatches(matches []symbolMatch, called sets.Set[string]) resolution {
	if len(matches) == 1 {
		match := matches[0]
		return resolution{kind: match.kind, target: match.symbol, priority: match.priority}
	}

	calledMatches := slices.DeleteFunc(slices.Clone(matches), func(match symbolMatch) bool {
		return !called.Has(match.symbol.id)
	})
	if len(calledMatches) == 1 {
		match := calledMatches[0]
		return resolution{kind: match.kind, target: match.symbol, priority: match.priority}
	}

	candidates := make([]*productionSymbol, 0, len(matches))
	for _, match := range matches {
		candidates = append(candidates, match.symbol)
	}
	slices.SortFunc(candidates, func(a, b *productionSymbol) int {
		return strings.Compare(a.id, b.id)
	})
	return resolution{kind: resolutionAmbiguous, candidates: candidates}
}

func calledProductionSymbols(pass *analysis.Pass, test *ast.FuncDecl, symbolsByID map[string]*productionSymbol) sets.Set[string] {
	called := sets.New[string]()
	ast.Inspect(test.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn := calledFunction(pass, call.Fun)
		if fn == nil || fn.Pkg() == nil {
			return true
		}
		id := symbolID(fn)
		if _, ok := symbolsByID[id]; ok {
			called.Insert(id)
		}
		return true
	})
	return called
}

func calledFunction(pass *analysis.Pass, expression ast.Expr) *types.Func {
	switch expression := expression.(type) {
	case *ast.ParenExpr:
		return calledFunction(pass, expression.X)
	case *ast.IndexExpr:
		return calledFunction(pass, expression.X)
	case *ast.IndexListExpr:
		return calledFunction(pass, expression.X)
	case *ast.Ident:
		fn, _ := pass.TypesInfo.Uses[expression].(*types.Func)
		return fn
	case *ast.SelectorExpr:
		if selection := pass.TypesInfo.Selections[expression]; selection != nil {
			fn, _ := selection.Obj().(*types.Func)
			return fn
		}
		fn, _ := pass.TypesInfo.Uses[expression.Sel].(*types.Func)
		return fn
	default:
		return nil
	}
}

func reportAmbiguous(pass *analysis.Pass, test *testFunction) {
	if shouldSuppress(pass, test) {
		return
	}
	names := make([]string, 0, len(test.resolution.candidates))
	for _, candidate := range test.resolution.candidates {
		names = append(names, candidate.display)
	}
	pass.Report(analysis.Diagnostic{
		Pos:     test.namePos,
		End:     test.nameEnd,
		Message: fmt.Sprintf("test function %q does not map unambiguously to one production function (candidates: %s); use the canonical Test<FunctionName> or Test<Receiver>_<Method> form, or add a documented //nolint:hypershiftlinter exception", test.name, strings.Join(names, ", ")),
	})
}

func reportGroup(pass *analysis.Pass, tests []*testFunction) {
	slices.SortFunc(tests, func(a, b *testFunction) int {
		if a.resolution.kind != b.resolution.kind {
			if a.resolution.kind == resolutionExact {
				return -1
			}
			if b.resolution.kind == resolutionExact {
				return 1
			}
		}
		if a.resolution.priority != b.resolution.priority {
			return b.resolution.priority - a.resolution.priority
		}
		if a.imported != b.imported {
			if a.imported {
				return -1
			}
			return 1
		}
		if result := strings.Compare(a.filename, b.filename); result != 0 {
			return result
		}
		return cmpPos(a.namePos, b.namePos)
	})

	symbol := tests[0].resolution.target
	preferred := symbol.canonicalTest
	var primary *testFunction
	if tests[0].resolution.kind == resolutionExact {
		primary = tests[0]
		preferred = primary.name
	}
	if primary != nil && !shouldSuppress(pass, primary) {
		for _, test := range tests[1:] {
			if test.imported && test.suppressed {
				continue
			}
			if !test.imported && !isLegacyException(pass, test) {
				continue
			}
			kind := "legacy"
			if test.imported {
				kind = "internal"
			}
			pass.Report(analysis.Diagnostic{
				Pos:     primary.namePos,
				End:     primary.nameEnd,
				Message: fmt.Sprintf("test function %q adds another top-level test for %s while %s test %q still exists; consolidate both under %q using table-driven cases or t.Run subtests", primary.name, symbol.display, kind, test.name, preferred),
				Related: relatedTest(test, fmt.Sprintf("%s top-level test %q", kind, test.name)),
			})
			break
		}
	}

	for _, test := range tests {
		if shouldSuppress(pass, test) {
			continue
		}

		var message string
		switch test.resolution.kind {
		case resolutionExact:
			if test == primary {
				continue
			}
			message = fmt.Sprintf("test function %q duplicates %q for %s; consolidate scenarios under %q using table-driven cases or t.Run subtests", test.name, primary.name, symbol.display, preferred)
		case resolutionScenario:
			if primary != nil {
				message = fmt.Sprintf("test function %q also tests %s; consolidate it into %q using table-driven cases or t.Run subtests", test.name, symbol.display, preferred)
			} else {
				message = fmt.Sprintf("test function %q must be named %q for %s; keep scenarios in table-driven cases or t.Run subtests", test.name, preferred, symbol.display)
			}
		case resolutionDisconnected:
			message = fmt.Sprintf("test function %q must be named %q to map to %s; use one top-level test with table-driven cases or t.Run subtests, or add a documented //nolint:hypershiftlinter exception", test.name, preferred, symbol.display)
		default:
			continue
		}

		diagnostic := analysis.Diagnostic{
			Pos:     test.namePos,
			End:     test.nameEnd,
			Message: message,
		}
		if primary != nil && primary != test {
			diagnostic.Related = relatedTest(primary, fmt.Sprintf("existing top-level test %q", primary.name))
		}
		pass.Report(diagnostic)
	}
}

func shouldSuppress(pass *analysis.Pass, test *testFunction) bool {
	if test.imported || test.suppressed {
		return true
	}
	return isLegacyException(pass, test)
}

func relatedTest(test *testFunction, message string) []analysis.RelatedInformation {
	if !test.namePos.IsValid() {
		return nil
	}
	return []analysis.RelatedInformation{{
		Pos:     test.namePos,
		End:     test.nameEnd,
		Message: message,
	}}
}

func isLegacyException(pass *analysis.Pass, test *testFunction) bool {
	return legacyExceptions.Has(legacyKey(pass, test))
}

func legacyKey(pass *analysis.Pass, test *testFunction) string {
	const modulePath = "github.com/openshift/hypershift"

	packagePath := strings.TrimSuffix(pass.Pkg.Path(), "_test")
	if packagePath == modulePath {
		return filepath.Base(test.filename) + "|" + test.name
	}
	if relativePackage, ok := strings.CutPrefix(packagePath, modulePath+"/"); ok {
		return relativePackage + "/" + filepath.Base(test.filename) + "|" + test.name
	}
	return packagePath + "/" + filepath.Base(test.filename) + "|" + test.name
}

func cmpPos(a, b token.Pos) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
