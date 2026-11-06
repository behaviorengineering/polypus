package polypus_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const facadeImportPath = "github.com/behaviorengineering/polypus/pkg/polypus"

// TestInternalDoesNotImportFacade keeps pkg/polypus a leaf: internal/ packages
// depend on the packages that do the work, never on the outside hosts' contract.
func TestInternalDoesNotImportFacade(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	fset := token.NewFileSet()
	var checked int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		checked++
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if p == facadeImportPath {
				t.Errorf("%s imports the public façade %s; import the internal package instead", path, facadeImportPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatalf("no Go files found under %s", root)
	}
}
