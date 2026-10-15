// Package archtest enforces the architecture rules of plan §1.1 (A1–A12) on the
// whole module. It is authoritative: golangci-lint's depguard mirrors A1–A3, but
// prefix rules are easy to misconfigure.
//
// Owner: SCAFFOLD (contract steward during phases).
package archtest

import (
	"go/ast"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/packages"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/logx"
)

const M = "github.com/Nibroc6/purple-chicken-cult"

var (
	loadOnce sync.Once
	loaded   []*packages.Package
	loadErr  error
)

func load(t *testing.T) []*packages.Package {
	t.Helper()
	loadOnce.Do(func() {
		touchSources()
		cfg := &packages.Config{
			Mode: packages.NeedName | packages.NeedImports | packages.NeedTypes | packages.NeedTypesInfo |
				packages.NeedSyntax | packages.NeedFiles,
			Dir: "../..",
		}
		loaded, loadErr = packages.Load(cfg, "./...")
	})
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	for _, p := range loaded {
		for _, e := range p.Errors {
			t.Fatalf("archtest: %s: %v", p.PkgPath, e)
		}
	}
	return loaded
}

// touchSources stats every .go file so the go test cache notices source changes
// (packages.Load reads them in a go list subprocess, which the cache cannot see).
func touchSources() {
	_ = filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "dist", "var":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") {
			_, _ = os.Stat(p)
		}
		return nil
	})
}

func rel(path string) string { return strings.TrimPrefix(strings.TrimPrefix(path, M), "/") }

func under(path, prefix string) bool {
	r := rel(path)
	return r == prefix || strings.HasPrefix(r, prefix+"/")
}

func isTestFile(p *packages.Package, f *ast.File) bool {
	return strings.HasSuffix(p.Fset.Position(f.Pos()).Filename, "_test.go")
}

// A1: only gate/..., store/..., cmd/pcc and archtest import internal/store/...
func TestA1StoreImporters(t *testing.T) {
	for _, p := range load(t) {
		for imp := range p.Imports {
			if !under(imp, "internal/store") {
				continue
			}
			if under(p.PkgPath, "internal/gate") || under(p.PkgPath, "internal/store") || rel(p.PkgPath) == "cmd/pcc" ||
				under(p.PkgPath, "internal/archtest") {
				continue
			}
			t.Errorf("A1: %s imports %s (only gate talks to the database, §13)", rel(p.PkgPath), rel(imp))
		}
		if rel(p.PkgPath) != "cmd/pcc" {
			continue
		}
		allowed := map[string]bool{"Open": true, "Migrate": true, "Close": true, "DB": true}
		for id, obj := range p.TypesInfo.Uses {
			if obj.Pkg() != nil && under(obj.Pkg().Path(), "internal/store") && !allowed[obj.Name()] {
				t.Errorf("A1: cmd/pcc uses store.%s at %s (only Open, Migrate, Close)", obj.Name(), p.Fset.Position(id.Pos()))
			}
		}
	}
}

// A2: handlers never import pgx, store, platform/refs or platform/sigs.
// A3: platform packages never import gate, store or api.
// A4: only gate/auth, gate/notify and gate/signals/automation import net/http.
func TestA2A3A4Imports(t *testing.T) {
	for _, p := range load(t) {
		for imp := range p.Imports {
			switch {
			case under(p.PkgPath, "internal/api"):
				if strings.HasPrefix(imp, "github.com/jackc/pgx") || under(imp, "internal/store") ||
					under(imp, "internal/platform/refs") || under(imp, "internal/platform/sigs") {
					t.Errorf("A2: %s imports %s", rel(p.PkgPath), imp)
				}
			case under(p.PkgPath, "internal/platform"):
				if under(imp, "internal/gate") || under(imp, "internal/store") || under(imp, "internal/api") {
					t.Errorf("A3: %s imports %s", rel(p.PkgPath), rel(imp))
				}
			case under(p.PkgPath, "internal/gate"):
				if imp == "net/http" && !under(p.PkgPath, "internal/gate/auth") && !under(p.PkgPath, "internal/gate/notify") &&
					!under(p.PkgPath, "internal/gate/signals/automation") {
					t.Errorf("A4: %s imports net/http", rel(p.PkgPath))
				}
				if under(imp, "internal/api") {
					t.Errorf("gate-no-api: %s imports %s", rel(p.PkgPath), rel(imp))
				}
			}
		}
	}
}

// forbiddenType reports whether t is, or structurally contains, a named type from
// a forbidden package (exported struct fields, pointer/slice/map/array/chan
// elements and function signatures; never methods, never unexported fields).
func forbiddenType(t types.Type, forbid func(pkg string) bool, seen map[types.Type]bool) string {
	if t == nil || seen[t] {
		return ""
	}
	seen[t] = true
	switch x := t.(type) {
	case *types.Named:
		if obj := x.Obj(); obj.Pkg() != nil && forbid(obj.Pkg().Path()) {
			return obj.Pkg().Path() + "." + obj.Name()
		}
		if obj := x.Obj(); obj.Pkg() != nil && under(obj.Pkg().Path(), "internal/store") {
			return "" // cmd/pcc may hold *store.DB (A1 restricts which store names it uses)
		}
		for i := 0; i < x.TypeArgs().Len(); i++ {
			if s := forbiddenType(x.TypeArgs().At(i), forbid, seen); s != "" {
				return s
			}
		}
		if st, ok := x.Underlying().(*types.Struct); ok {
			return forbiddenType(st, forbid, seen)
		}
		return ""
	case *types.Alias:
		return forbiddenType(types.Unalias(x), forbid, seen)
	case *types.Pointer:
		return forbiddenType(x.Elem(), forbid, seen)
	case *types.Slice:
		return forbiddenType(x.Elem(), forbid, seen)
	case *types.Array:
		return forbiddenType(x.Elem(), forbid, seen)
	case *types.Chan:
		return forbiddenType(x.Elem(), forbid, seen)
	case *types.Map:
		if s := forbiddenType(x.Key(), forbid, seen); s != "" {
			return s
		}
		return forbiddenType(x.Elem(), forbid, seen)
	case *types.Signature:
		for _, tup := range []*types.Tuple{x.Params(), x.Results()} {
			for i := 0; i < tup.Len(); i++ {
				if s := forbiddenType(tup.At(i).Type(), forbid, seen); s != "" {
					return s
				}
			}
		}
	case *types.Struct:
		for i := 0; i < x.NumFields(); i++ {
			if f := x.Field(i); f.Exported() {
				if s := forbiddenType(f.Type(), forbid, seen); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// A5: no raw values reach handlers: no expression in internal/api/... or cmd/pcc
// has a type that is or contains a core, store, pgx or uuid type.
func TestA5NoRawValuesInHandlers(t *testing.T) {
	for _, p := range load(t) {
		isAPI := under(p.PkgPath, "internal/api")
		isCmd := rel(p.PkgPath) == "cmd/pcc"
		if !isAPI && !isCmd {
			continue
		}
		forbid := func(pkg string) bool {
			if under(pkg, "internal/gate/internal/core") || strings.HasPrefix(pkg, "github.com/jackc/pgx") ||
				pkg == "github.com/google/uuid" {
				return true
			}
			return isAPI && under(pkg, "internal/store")
		}
		reported := map[token.Pos]bool{}
		for expr, tv := range p.TypesInfo.Types {
			if s := forbiddenType(tv.Type, forbid, map[types.Type]bool{}); s != "" && !reported[expr.Pos()] {
				reported[expr.Pos()] = true
				t.Errorf("A5: %s: expression of type %s contains %s", p.Fset.Position(expr.Pos()), tv.Type, s)
			}
		}
	}
}

// A6: handlers never encode JSON straight onto a ResponseWriter (use apix.JSON).
func TestA6JSONThroughApix(t *testing.T) {
	for _, p := range load(t) {
		if !under(p.PkgPath, "internal/api") || under(p.PkgPath, "internal/api/apix") {
			continue
		}
		for id, obj := range p.TypesInfo.Uses {
			if fn, ok := obj.(*types.Func); ok && fn.Pkg() != nil && fn.Pkg().Path() == "encoding/json" && fn.Name() == "NewEncoder" {
				t.Errorf("A6: %s uses json.NewEncoder; use apix.JSON", p.Fset.Position(id.Pos()))
			}
		}
	}
}

var codeRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// A7: Problem codes and reason enums are snake_case codes.
func TestA7Codes(t *testing.T) {
	for _, p := range load(t) {
		if !under(p.PkgPath, "internal/gate") && !under(p.PkgPath, "internal/api") {
			continue
		}
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					if fnName(p, x) == M+"/internal/gate.NewProblem" && len(x.Args) >= 2 {
						if lit, ok := x.Args[1].(*ast.BasicLit); ok {
							if s, _ := strconv.Unquote(lit.Value); !codeRE.MatchString(s) {
								t.Errorf("A7: %s: problem code %q", p.Fset.Position(lit.Pos()), s)
							}
						} else {
							t.Errorf("A7: %s: problem code must be a string literal", p.Fset.Position(x.Args[1].Pos()))
						}
					}
				case *ast.ValueSpec:
					tv, ok := p.TypesInfo.Types[x.Type]
					if x.Type == nil || !ok {
						return true
					}
					if named, ok := tv.Type.(*types.Named); ok && (named.Obj().Name() == "ModReason" || named.Obj().Name() == "ReportReason") {
						for _, v := range x.Values {
							if lit, ok := v.(*ast.BasicLit); ok {
								if s, _ := strconv.Unquote(lit.Value); !codeRE.MatchString(s) {
									t.Errorf("A7: %s: reason %q", p.Fset.Position(lit.Pos()), s)
								}
							}
						}
					}
				}
				return true
			})
		}
	}
}

func domainOf(path string) string {
	r := strings.TrimPrefix(rel(path), "internal/gate/")
	if strings.HasPrefix(r, "signals/") {
		parts := strings.SplitN(r, "/", 3)
		if len(parts) >= 2 {
			return parts[0] + "/" + parts[1]
		}
	}
	return strings.SplitN(r, "/", 2)[0]
}

// A8: domain isolation: gate/<a> never imports gate/<b>, except rank (pure),
// devseed → graph, and wire / conformance (import everything).
func TestA8DomainIsolation(t *testing.T) {
	for _, p := range load(t) {
		if !under(p.PkgPath, "internal/gate") || rel(p.PkgPath) == "internal/gate" {
			continue
		}
		a := domainOf(p.PkgPath)
		if a == "internal" || a == "wire" || a == "conformance" {
			continue
		}
		for imp := range p.Imports {
			if !under(imp, "internal/gate") || rel(imp) == "internal/gate" {
				continue
			}
			b := domainOf(imp)
			if b == a || b == "internal" || b == "rank" || (a == "devseed" && b == "graph") {
				continue
			}
			t.Errorf("A8: gate/%s imports gate/%s (cross-module calls go through core interfaces)", a, b)
		}
	}
}

var sqlStart = regexp.MustCompile(`^(?i)\s*(SELECT|INSERT|UPDATE|DELETE|WITH|CREATE|ALTER|TRUNCATE|RESTART|FROM|WHERE|AND|ORDER|VALUES|RETURNING|SET|JOIN|LEFT|GROUP|LIMIT|ON CONFLICT)\b`)

func looksLikeProse(s string) bool {
	var prev, prev2 rune
	i := 0
	for _, r := range s {
		if i >= 2 && unicode.IsLetter(prev2) && prev == ' ' && unicode.IsLetter(r) {
			return true
		}
		prev2, prev = prev, r
		i++
	}
	return false
}

func fnName(p *packages.Package, call *ast.CallExpr) string {
	var id *ast.Ident
	switch f := call.Fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	case *ast.IndexExpr:
		if sel, ok := f.X.(*ast.SelectorExpr); ok {
			id = sel.Sel
		}
	}
	if id == nil {
		return ""
	}
	obj := p.TypesInfo.Uses[id]
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil {
		return ""
	}
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		rt := sig.Recv().Type()
		if ptr, ok := rt.(*types.Pointer); ok {
			rt = ptr.Elem()
		}
		if named, ok := rt.(*types.Named); ok {
			return fn.Pkg().Path() + "." + named.Obj().Name() + "." + fn.Name()
		}
		return fn.Pkg().Path() + ".?." + fn.Name()
	}
	return fn.Pkg().Path() + "." + fn.Name()
}

func proseAllowedCall(name string) bool {
	switch {
	case strings.HasPrefix(name, "log/slog."):
		return true
	case name == "fmt.Errorf", name == "errors.New":
		return true
	case strings.HasPrefix(name, "testing."): // t.Errorf / t.Fatalf in test helpers
		return true
	}
	return false
}

// A9: no server prose (§15.4): in non-test files under gate/ and api/, a string
// literal that looks like prose may only be a log message, an error, SQL, a
// struct tag or an HTML document shell.
func TestA9NoServerProse(t *testing.T) {
	for _, p := range load(t) {
		if !under(p.PkgPath, "internal/gate") && !under(p.PkgPath, "internal/api") {
			continue
		}
		for _, f := range p.Syntax {
			if isTestFile(p, f) {
				continue
			}
			var walk func(n ast.Node, allowed bool)
			walk = func(n ast.Node, allowed bool) {
				ast.Inspect(n, func(c ast.Node) bool {
					switch x := c.(type) {
					case *ast.CallExpr:
						ok := allowed || proseAllowedCall(fnName(p, x))
						walk(x.Fun, allowed)
						for _, a := range x.Args {
							walk(a, ok)
						}
						return false
					case *ast.Field:
						if x.Type != nil {
							walk(x.Type, allowed)
						}
						return false // tags are allowed
					case *ast.BasicLit:
						if x.Kind != token.STRING || allowed {
							return false
						}
						s, err := strconv.Unquote(x.Value)
						if err != nil || !utf8.ValidString(s) {
							return false
						}
						if looksLikeProse(s) && !sqlStart.MatchString(s) && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "<!doctype") {
							t.Errorf("A9: %s: prose-like literal %q (send a code; text lives in the catalogs)", p.Fset.Position(x.Pos()), s)
						}
						return false
					}
					return true
				})
			}
			for _, d := range f.Decls {
				walk(d, false)
			}
		}
	}
}

// A10: only visibility and coreref mint Checked; only visibility, postindex and
// coreref read raw links (PostIndex.Children / Parents).
func TestA10VisibilityCapability(t *testing.T) {
	core := M + "/internal/gate/internal/core"
	for _, p := range load(t) {
		pkg := rel(p.PkgPath)
		for id, obj := range p.TypesInfo.Uses {
			fn, ok := obj.(*types.Func)
			if !ok || fn.Pkg() == nil || fn.Pkg().Path() != core {
				continue
			}
			sig, _ := fn.Type().(*types.Signature)
			switch {
			case fn.Name() == "MintChecked" && sig != nil && sig.Recv() == nil:
				if pkg != "internal/gate/visibility" && pkg != "internal/gate/internal/coreref" && pkg != "internal/gate/internal/core" {
					t.Errorf("A10: %s calls core.MintChecked", p.Fset.Position(id.Pos()))
				}
			case (fn.Name() == "Children" || fn.Name() == "Parents") && sig != nil && sig.Recv() != nil:
				if pkg != "internal/gate/visibility" && pkg != "internal/gate/postindex" && pkg != "internal/gate/internal/coreref" {
					t.Errorf("A10: %s reads raw links (use Visibility.VisibleChildren/VisibleParents)", p.Fset.Position(id.Pos()))
				}
			}
		}
	}
}

// A11: log attribute keys come from the logx allowlist; raw IDs, refs, IPs and
// requests are never logged.
func TestA11LoggingPolicy(t *testing.T) {
	bad := map[string]bool{
		M + "/internal/gate/internal/core.UserID": true, "github.com/google/uuid.UUID": true,
		"net/netip.Addr": true, M + "/internal/gate.Ref": true, "net/http.Request": true,
	}
	for _, p := range load(t) {
		if !under(p.PkgPath, "internal/gate") && !under(p.PkgPath, "internal/api") {
			continue
		}
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := fnName(p, call)
				var first int
				switch name {
				case "log/slog.Info", "log/slog.Warn", "log/slog.Error", "log/slog.Debug",
					"log/slog.Logger.Info", "log/slog.Logger.Warn", "log/slog.Logger.Error", "log/slog.Logger.Debug":
					first = 1
				case "log/slog.Log", "log/slog.Logger.Log":
					first = 3
				default:
					return true
				}
				for i := first; i < len(call.Args); i++ {
					arg := call.Args[i]
					if tv, ok := p.TypesInfo.Types[arg]; ok {
						typ := tv.Type
						if ptr, ok := typ.(*types.Pointer); ok {
							typ = ptr.Elem()
						}
						if named, ok := typ.(*types.Named); ok && named.Obj().Pkg() != nil &&
							bad[named.Obj().Pkg().Path()+"."+named.Obj().Name()] {
							t.Errorf("A11: %s logs a raw %s (use logx.UID)", p.Fset.Position(arg.Pos()), named.Obj().Name())
						}
					}
					if (i-first)%2 == 0 {
						if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							if k, _ := strconv.Unquote(lit.Value); !logx.Allowed(k) {
								t.Errorf("A11: %s: log key %q is not in the logx allowlist", p.Fset.Position(lit.Pos()), k)
							}
						}
					}
				}
				return true
			})
		}
	}
}

// A12: handlers register routes only through apix.Route (which requires a charge
// class); only router, staticapi, apix and mediaapi.RegisterPublic touch chi directly.
func TestA12ChargedRoutes(t *testing.T) {
	direct := map[string]bool{"Get": true, "Post": true, "Put": true, "Patch": true, "Delete": true, "Head": true,
		"Method": true, "MethodFunc": true, "Handle": true, "HandleFunc": true, "Route": true, "Mount": true, "Group": true}
	exempt := map[string]bool{"internal/api/router": true, "internal/api/staticapi": true, "internal/api/apix": true}
	var offenders []string
	for _, p := range load(t) {
		if !under(p.PkgPath, "internal/api") || exempt[rel(p.PkgPath)] {
			continue
		}
		for id, obj := range p.TypesInfo.Uses {
			fn, ok := obj.(*types.Func)
			if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "github.com/go-chi/chi/v5" || !direct[fn.Name()] {
				continue
			}
			pos := p.Fset.Position(id.Pos())
			if rel(p.PkgPath) == "internal/api/mediaapi" && strings.Contains(pos.Filename, "public") {
				continue
			}
			offenders = append(offenders, pos.String()+" chi."+fn.Name())
		}
	}
	sort.Strings(offenders)
	for _, o := range offenders {
		t.Errorf("A12: %s: register routes with apix.Route (charge class required)", o)
	}
}
