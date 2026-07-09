package quality

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestExportedSymbolsHaveDocs(t *testing.T) {
	root := repoRoot(t)
	for _, path := range trackedGoFiles(t, root) {
		src, err := os.ReadFile(filepath.Join(root, path)) //nolint:gosec // paths come from tracked git files under the repo root.
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if isGenerated(src) {
			continue
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.GenDecl:
				checkGenDecl(t, fset, path, decl)
			case *ast.FuncDecl:
				if decl.Recv == nil && decl.Name.IsExported() && !docStartsWith(decl.Doc, decl.Name.Name) {
					reportMissingDoc(t, fset, path, decl.Pos(), decl.Name.Name)
				}
			}
		}
	}
}

func checkGenDecl(t *testing.T, fset *token.FileSet, path string, decl *ast.GenDecl) {
	constBlockDocumented := false
	if decl.Tok == token.CONST {
		if first := firstExportedName(decl); first != "" {
			constBlockDocumented = docStartsWith(decl.Doc, first)
		}
	}

	for _, spec := range decl.Specs {
		switch spec := spec.(type) {
		case *ast.TypeSpec:
			if spec.Name.IsExported() && !docStartsWith(firstDoc(spec.Doc, decl.Doc), spec.Name.Name) {
				reportMissingDoc(t, fset, path, spec.Pos(), spec.Name.Name)
			}
		case *ast.ValueSpec:
			for _, name := range spec.Names {
				if !name.IsExported() {
					continue
				}
				if decl.Tok == token.CONST && constBlockDocumented {
					continue
				}
				if !docStartsWith(firstDoc(spec.Doc, decl.Doc), name.Name) {
					reportMissingDoc(t, fset, path, name.Pos(), name.Name)
				}
			}
		}
	}
}

func trackedGoFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", "ls-files", "*.go")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files *.go: %v", err)
	}

	var paths []string
	for _, path := range strings.Fields(string(out)) {
		if shouldSkip(path) {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func shouldSkip(path string) bool {
	if strings.HasSuffix(path, "_test.go") {
		return true
	}
	for _, prefix := range []string{"prototype/", ".worktrees/", ".claude/", "vendor/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func repoRoot(t *testing.T) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func firstExportedName(decl *ast.GenDecl) string {
	for _, spec := range decl.Specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, name := range valueSpec.Names {
			if name.IsExported() {
				return name.Name
			}
		}
	}
	return ""
}

func firstDoc(docs ...*ast.CommentGroup) *ast.CommentGroup {
	for _, doc := range docs {
		if doc != nil {
			return doc
		}
	}
	return nil
}

func docStartsWith(doc *ast.CommentGroup, name string) bool {
	if doc == nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(doc.Text()), name)
}

func reportMissingDoc(t *testing.T, fset *token.FileSet, path string, pos token.Pos, name string) {
	t.Helper()
	position := fset.Position(pos)
	t.Errorf("%s:%d:%d: exported symbol %s must have a doc comment starting with %q", path, position.Line, position.Column, name, name)
}

func isGenerated(src []byte) bool {
	for _, line := range bytes.Split(src, []byte("\n"))[:min(20, bytes.Count(src, []byte("\n"))+1)] {
		text := string(line)
		if strings.Contains(text, "Code generated") && strings.Contains(text, "DO NOT EDIT") {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
