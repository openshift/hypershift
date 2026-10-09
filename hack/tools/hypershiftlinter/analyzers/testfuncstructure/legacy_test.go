package testfuncstructure

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestLegacyExceptions(t *testing.T) {
	g := NewWithT(t)
	_, source, _, ok := runtime.Caller(0)
	g.Expect(ok).To(BeTrue())
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "../../../../.."))
	declarations := map[string][]string{}

	for _, exception := range sets.List(legacyExceptions) {
		t.Run(fmt.Sprintf("When %s is baselined, it should name an existing test", exception), func(t *testing.T) {
			g := NewWithT(t)
			filename, name, ok := strings.Cut(exception, "|")
			g.Expect(ok).To(BeTrue())
			g.Expect(filename).To(HaveSuffix("_test.go"))
			g.Expect(name).To(HavePrefix("Test"))

			names, loaded := declarations[filename]
			if !loaded {
				file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(repoRoot, filename), nil, parser.SkipObjectResolution)
				g.Expect(err).ToNot(HaveOccurred())
				for _, declaration := range file.Decls {
					if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil {
						names = append(names, function.Name.Name)
					}
				}
				declarations[filename] = names
			}

			g.Expect(names).To(ContainElement(name))
		})
	}
}
