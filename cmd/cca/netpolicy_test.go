package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The README promises cca makes no internet connections of its own and listens only on
// 127.0.0.1. This fails when shipped code breaks that: a network import outside the server, an
// HTTP client, a dial, a listener on another address, or a network MCP transport.
func TestNoNetworkBeyondLoopback(t *testing.T) {
	root := filepath.Join("..", "..")
	skip := map[string]bool{".git": true, ".dev": true, "dist": true, "node_modules": true, "testdata": true, "web": true}
	// Client-side and listen-anywhere parts of net/http; the server side stays allowed.
	httpBanned := map[string]bool{"Get": true, "Post": true, "PostForm": true, "Head": true, "NewRequest": true,
		"NewRequestWithContext": true, "Client": true, "DefaultClient": true, "Transport": true, "DefaultTransport": true,
		"ListenAndServe": true, "ListenAndServeTLS": true}
	fset := token.NewFileSet()
	files := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		files++
		rel, _ := filepath.Rel(root, path)
		server := filepath.ToSlash(filepath.Dir(rel)) == "internal/server"
		for _, im := range f.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			network := p == "net" || strings.HasPrefix(p, "net/") || p == "crypto/tls" || strings.HasPrefix(p, "golang.org/x/net")
			if !network || p == "net/url" || (server && (p == "net" || p == "net/http")) {
				continue
			}
			t.Errorf("%s imports %s: only internal/server may use the network, and only net and net/http", rel, p)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && isSel(call.Fun, "net", "Listen") {
				if len(call.Args) != 2 || !loopback(call.Args[1]) {
					t.Errorf("%s: net.Listen must bind a literal 127.0.0.1 address", fset.Position(call.Pos()))
				}
			}
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			name := sel.Sel.Name
			switch {
			case pkg.Name == "http" && httpBanned[name]:
				t.Errorf("%s: http.%s makes or accepts outside connections", fset.Position(sel.Pos()), name)
			case pkg.Name == "net" && name != "Listen" && name != "Listener" && name != "SplitHostPort": // parsing only
				t.Errorf("%s: net.%s is not allowed, only a loopback net.Listen", fset.Position(sel.Pos()), name)
			case pkg.Name == "mcp" && name != "StdioTransport" &&
				(strings.HasSuffix(name, "Transport") || strings.Contains(name, "HTTP") || strings.Contains(name, "SSE")):
				t.Errorf("%s: mcp.%s is a network transport; the MCP server is stdio only", fset.Position(sel.Pos()), name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 10 {
		t.Fatalf("scanned only %d Go files: the walk is looking in the wrong place", files)
	}

	app, err := os.ReadFile(filepath.Join(root, "macos", "App.swift"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(app), "URLSession") {
		t.Error("macos/App.swift uses URLSession: the app must load nothing but the engine's own page")
	}
}

func isSel(e ast.Expr, pkg, name string) bool {
	s, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := s.X.(*ast.Ident)
	return ok && x.Name == pkg && s.Sel.Name == name
}

// loopback reports whether an address expression starts with the literal "127.0.0.1:".
func loopback(e ast.Expr) bool {
	for {
		b, ok := e.(*ast.BinaryExpr)
		if !ok {
			break
		}
		e = b.X
	}
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	s, _ := strconv.Unquote(lit.Value)
	return strings.HasPrefix(s, "127.0.0.1:")
}
