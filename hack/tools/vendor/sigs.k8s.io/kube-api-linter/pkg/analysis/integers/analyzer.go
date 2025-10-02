/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package integers

import (
	"go/ast"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	kalerrors "sigs.k8s.io/kube-api-linter/pkg/analysis/errors"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/utils"
)

const name = "integers"

// Analyzer is the analyzer for the integers package.
// It checks that no struct fields or type aliases are `int`, or unsigned integers.
var Analyzer = &analysis.Analyzer{
	Name:     name,
	Doc:      "All integers should be explicit about their size, int32 and int64 should be used over plain int. Unsigned ints are not allowed.",
	Run:      run,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
}

func run(pass *analysis.Pass) (any, error) {
	inspect, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return nil, kalerrors.ErrCouldNotGetInspector
	}

	// Filter to fields so that we can look at fields within structs.
	// Filter typespecs so that we can look at type aliases.
	nodeFilter := []ast.Node{
		(*ast.StructType)(nil),
		(*ast.TypeSpec)(nil),
	}

	typeChecker := utils.NewTypeChecker(checkIntegers)

	// Preorder visits all the nodes of the AST in depth-first order. It calls
	// f(n) for each node n before it visits n's children.
	//
	// We use the filter defined above, ensuring we only look at struct fields and type declarations.
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		typeChecker.CheckNode(pass, n)
	})

	return nil, nil //nolint:nilnil
}

// checkIntegers looks for known type of integers that do not match the allowed `int32` or `int64` requirements.
func checkIntegers(pass *analysis.Pass, ident *ast.Ident, node ast.Node, prefix string) {
	switch ident.Name {
	case "int32", "int64":
		// Valid cases
	case "int", "int8", "int16":
		pass.Reportf(node.Pos(), "%s should not use an int, int8 or int16. Use int32 or int64 depending on bounding requirements", prefix)
	case "uint", "uint8", "uint16", "uint32", "uint64":
		pass.Reportf(node.Pos(), "%s should not use unsigned integers, use only int32 or int64 and apply validation to ensure the value is positive", prefix)
	}
}
