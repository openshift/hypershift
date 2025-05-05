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
package utils

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// IsBasicType checks if the type of the given identifier is a basic type.
// Basic types are types like int, string, bool, etc.
func IsBasicType(pass *analysis.Pass, ident *ast.Ident) bool {
	_, ok := pass.TypesInfo.TypeOf(ident).(*types.Basic)
	return ok
}

// LookupTypeSpec is used to search for the type spec of a given identifier.
// It will first check to see if the ident has an Obj, and if so, it will return the type spec
// from the Obj. If the Obj is nil, it will search through the files in the package to find the
// type spec that matches the identifier's position.
func LookupTypeSpec(pass *analysis.Pass, ident *ast.Ident) (*ast.TypeSpec, bool) {
	if ident.Obj != nil && ident.Obj.Decl != nil {
		// The identifier has an Obj, we can use it to find the type spec.
		if tSpec, ok := ident.Obj.Decl.(*ast.TypeSpec); ok {
			return tSpec, true
		}
	}

	namedType, ok := pass.TypesInfo.TypeOf(ident).(*types.Named)
	if !ok {
		return nil, false
	}

	if !isInPassPackage(pass, namedType) {
		// The identifier is not in the pass package, we can't find the type spec.
		return nil, false
	}

	tokenFile, astFile := getFilesForType(pass, ident)

	if astFile == nil {
		// We couldn't match the token.File to the ast.File.
		return nil, false
	}

	for n := range ast.Preorder(astFile) {
		tSpec, ok := n.(*ast.TypeSpec)
		if !ok {
			continue
		}

		// Token files are 1-based, while ast files are 0-based.
		// We need to adjust the position to match the token file.
		filePos := tSpec.Pos() - astFile.FileStart + token.Pos(tokenFile.Base())

		if filePos == namedType.Obj().Pos() {
			return tSpec, true
		}
	}

	return nil, false
}

func getFilesForType(pass *analysis.Pass, ident *ast.Ident) (*token.File, *ast.File) {
	namedType, ok := pass.TypesInfo.TypeOf(ident).(*types.Named)
	if !ok {
		return nil, nil
	}

	tokenFile := pass.Fset.File(namedType.Obj().Pos())

	for _, astFile := range pass.Files {
		if astFile.Package == token.Pos(tokenFile.Base()) {
			return tokenFile, astFile
		}
	}

	return tokenFile, nil
}

func isInPassPackage(pass *analysis.Pass, namedType *types.Named) bool {
	return namedType.Obj().Pkg() != nil && namedType.Obj().Pkg() == pass.Pkg
}
