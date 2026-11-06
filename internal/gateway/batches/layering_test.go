package batches

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	modulePath   = "github.com/behaviorengineering/polypus"
	batchesPkg   = modulePath + "/internal/gateway/batches"
	gatewayTree  = modulePath + "/internal/gateway"
	batchLeafDir = "internal/batch"
)

// TestBatchesStaysInsideTheGateway keeps the batch lifecycle a gateway feature:
// nothing outside internal/gateway imports internal/gateway/batches, and the batch
// vocabulary and store in internal/batch stay a leaf that imports nothing under
// internal/gateway.
func TestBatchesStaysInsideTheGateway(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	files, err := moduleImports(root)
	if err != nil {
		t.Fatal(err)
	}
	for rel, imports := range files {
		inGateway := strings.HasPrefix(rel, "internal/gateway/")
		inLeaf := filepath.ToSlash(filepath.Dir(rel)) == batchLeafDir
		for _, ipath := range imports {
			if ipath == batchesPkg && !inGateway {
				t.Errorf("%s imports %s; the batch lifecycle is a gateway feature", rel, batchesPkg)
			}
			if inLeaf && (ipath == gatewayTree || strings.HasPrefix(ipath, gatewayTree+"/")) {
				t.Errorf("%s imports %s; internal/batch must stay a leaf", rel, ipath)
			}
		}
	}
}

// moduleImports parses every Go file under root (skipping hidden, vendor, testdata
// and node_modules trees) and returns its import paths by slash-separated path
// relative to root.
func moduleImports(root string) (map[string][]string, error) {
	fset := token.NewFileSet()
	out := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		imports := make([]string, 0, len(f.Imports))
		for _, imp := range f.Imports {
			ipath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			imports = append(imports, ipath)
		}
		out[filepath.ToSlash(rel)] = imports
		return nil
	})
	return out, err
}

// skipDir reports a directory the walk leaves out.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || name == "node_modules"
}
