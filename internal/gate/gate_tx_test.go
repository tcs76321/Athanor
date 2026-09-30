package gate

// SQLite single-connection discipline as an executable gate (F2; ADR-0027).
//
// ADR-0003 pins the daemon to one SQLite connection
// (`SetMaxOpenConns(1)`), so anything that holds that connection also holds
// the whole database. The way that happens accidentally is a transaction — or
// a taken `*sql.Conn` — opened in an orchestration package that also does
// network, model, or process I/O: every other goroutine then blocks on the
// pool and the daemon stalls with no error (a 5 s `busy_timeout` over WAL
// turns a deadlock into a hang, not a failure).
//
// The rule is package-shaped, not file-shaped:
//
//   - `BeginTx` / `Begin(` may not appear in `internal/engine` or `cmd/`.
//     Transactions live in the storage packages (`internal/store` and the
//     repos over it: `job`, `project`, `artifact`, `evaluation`, `mce`),
//     which perform no I/O. A new transaction there is a non-event; one in
//     the engine is a build break.
//   - `Conn(` may be called only under `internal/store` — the one place a
//     sqlite-vec-style extension loader would live (ADR-0026 §2, deferred).
//
// It is deliberately a structural rule over identifiers, so it cannot prove
// that a transaction *handed to* the engine stays short. That is what the
// runtime concurrency guard in `internal/store` is for (ADR-0027 §3).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// txForbiddenRoots are the orchestration trees where opening a transaction
// would hold the single connection across I/O.
var txForbiddenRoots = []string{"../../internal/engine", "../../cmd"}

// connAllowedPrefix is the only tree allowed to take the pooled connection
// directly (extension loading; ADR-0027 §1).
const connAllowedPrefix = "../../internal/store/"

// TestGateSingleConnectionDiscipline fails the build if a transaction is
// opened outside the storage packages, or if the pooled connection is taken
// outside internal/store (ADR-0027 §2).
func TestGateSingleConnectionDiscipline(t *testing.T) {
	violations := 0

	for _, root := range txForbiddenRoots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if perr != nil {
				t.Errorf("parsing %s: %v", path, perr)
				return nil
			}
			for _, bad := range transactionCalls(file) {
				t.Errorf("%s calls %s — transactions belong in the storage packages; "+
					"holding the single SQLite connection (ADR-0003) across I/O stalls the daemon (ADR-0027 §2)",
					path, bad)
				violations++
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	err := filepath.WalkDir("../../internal", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.HasPrefix(path, connAllowedPrefix) {
			return nil // internal/store is the sanctioned owner
		}
		file, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if perr != nil {
			t.Errorf("parsing %s: %v", path, perr)
			return nil
		}
		if connectionCalls(file) {
			t.Errorf("%s takes the pooled connection via Conn( — only internal/store may "+
				"(extension loading; ADR-0027 §2)", path)
			violations++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if violations > 0 {
		t.Fatalf("%d single-connection discipline violation(s) (ADR-0027)", violations)
	}
}

// transactionCalls returns the transaction-opening calls in file, rendered for
// the failure message. Any `.BeginTx(` or `.Begin(` counts: in the forbidden
// roots there is no legitimate reason to open a transaction, so the rule does
// not try to identify the receiver. A type with its own Begin method would
// trip this gate deliberately, and the allowlist is the place to say so.
func transactionCalls(file *ast.File) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "BeginTx" || sel.Sel.Name == "Begin" {
			out = append(out, "."+sel.Sel.Name+"(")
		}
		return true
	})
	return out
}

// connectionCalls reports whether file calls a method named Conn — the shape
// `db.Conn(ctx)` takes the single pooled connection (ADR-0027 §1).
func connectionCalls(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "Conn" {
			found = true
		}
		return true
	})
	return found
}

// TestTransactionCallsDetection proves the detector fires on the shapes that
// open a transaction and ignores everything else — including the string
// literal in a comment or message, which a text search would have caught.
func TestTransactionCallsDetection(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"beginTx", `package p
import "database/sql"
func f(db *sql.DB) { _, _ = db.BeginTx(nil, nil) }`, 1},
		{"begin", `package p
import "database/sql"
func f(db *sql.DB) { _, _ = db.Begin() }`, 1},
		{"no_transaction", `package p
func f(db any) { _ = db }`, 0},
		{"string_literal_only", `package p
var s = ".BeginTx("`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "synthetic.go", c.src, 0)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := len(transactionCalls(file)); got != c.want {
				t.Errorf("transactionCalls = %d, want %d", got, c.want)
			}
		})
	}
}

// TestConnectionCallsDetection proves the detector fires on db.Conn(ctx) and
// on nothing else.
func TestConnectionCallsDetection(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"conn", `package p
import "database/sql"
func f(db *sql.DB) { _, _ = db.Conn(nil) }`, true},
		{"connect_named_other", `package p
func f(x any) { _ = x }`, false},
		{"string_literal_only", `package p
var s = ".Conn("`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "synthetic.go", c.src, 0)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := connectionCalls(file); got != c.want {
				t.Errorf("connectionCalls = %v, want %v", got, c.want)
			}
		})
	}
}
