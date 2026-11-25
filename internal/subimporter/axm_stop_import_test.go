package subimporter

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// There is no observable interleaving point inside the active stop path. Guard the lock
// boundary directly so introducing an unlock before the signal fails even when Go does
// not schedule another worker into that gap during the behavioral tests.
func TestStopImportKeepsOneLockAcrossTheOperation(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "importer.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Name.Name != "StopImport" {
			continue
		}
		if len(fn.Body.List) < 2 {
			t.Fatal("StopImport must take the lock and defer its release")
		}
		lock, locked := fn.Body.List[0].(*ast.ExprStmt)
		unlock, deferred := fn.Body.List[1].(*ast.DeferStmt)
		if !locked || !deferred {
			t.Fatal("StopImport must take the lock and defer its release before inspecting the import")
		}
		method := func(expr ast.Expr, name string) bool {
			call, ok := expr.(*ast.CallExpr)
			if !ok {
				return false
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return false
			}
			id, ok := sel.X.(*ast.Ident)
			return ok && id.Name == fn.Recv.List[0].Names[0].Name && sel.Sel.Name == name
		}
		if !method(lock.X, "Lock") || !method(unlock.Call, "Unlock") {
			t.Fatal("StopImport must hold the importer lock until it returns")
		}
		for _, stmt := range fn.Body.List[2:] {
			ast.Inspect(stmt, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && (method(call, "Unlock") || method(call, "Stop")) {
					t.Error("the numbered stop must not release the lock or delegate to the unnumbered stop")
				}
				return true
			})
		}
		return
	}
	t.Fatal("StopImport method not found")
}

// StopImport clears a finished import only when the number is that of the import held (LST-63).
func TestStopImportOnlyThatImport(t *testing.T) {
	im := &Importer{status: Status{Status: StatusFinished, AxmImportID: 7, Imported: 3}, importSeq: 7}
	if st, done := im.StopImport(6); done || st.Status != StatusFinished || st.Imported != 3 {
		t.Fatalf("%v %+v", done, st)
	}
	if st, done := im.StopImport(7); !done || st.Status != StatusNone {
		t.Fatalf("%v %+v", done, st)
	}
}

func TestStopImportKeepsSignalsWithTheirSession(t *testing.T) {
	im := New(Options{}, nil, nil)
	first, err := im.NewSession(SessionOpt{Filename: "first.csv"})
	if err != nil {
		t.Fatal(err)
	}
	st, changed := im.StopImport(first.id)
	if !changed || st.Status != StatusStopping || st.AxmImportID != first.id {
		t.Fatalf("stop of active import: %v %+v", changed, st)
	}
	if st, changed := im.StopImport(first.id); changed || st.Status != StatusStopping {
		t.Fatalf("repeat stop cleared an active import: %v %+v", changed, st)
	}
	if _, err := im.NewSession(SessionOpt{}); err == nil {
		t.Fatal("started another import while the first was still stopping")
	}
	// The first worker can finish without reading its stop signal if it has read the CSV.
	im.setStatus(StatusFinished)
	second, err := im.NewSession(SessionOpt{Filename: "second.csv"})
	if err != nil {
		t.Fatal(err)
	}
	if st, changed := im.StopImport(first.id); changed || st.Status != StatusImporting || st.AxmImportID != second.id {
		t.Fatalf("stale stop affected the second import: %v %+v", changed, st)
	}
	select {
	case <-second.stop:
		t.Fatal("the second reader received the first reader's stop signal")
	default:
	}
	if log := string(im.GetLogsForImport(first.id)); log != "" {
		t.Fatalf("old request received the second import's log: %q", log)
	}
	if log := string(im.GetLogsForImport(second.id)); !strings.Contains(log, "second.csv") {
		t.Fatalf("missing current log: %q", log)
	}
}

func TestStaleStopImportReturnsADetachedSnapshot(t *testing.T) {
	im := New(Options{}, nil, nil)
	s, err := im.NewSession(SessionOpt{})
	if err != nil {
		t.Fatal(err)
	}
	row := RejectedRow{Row: 2, Reason: RejectFormat}
	im.reject(s.id, row)
	im.addWarned(s.id, map[string]int{"role": 1})
	st, changed := im.StopImport(s.id - 1)
	if changed || st.AxmImportID != s.id {
		t.Fatalf("stale stop: %v %+v", changed, st)
	}
	im.reject(s.id, row)
	im.addWarned(s.id, map[string]int{"role": 1})
	if st.AxmRejectedTotal != 1 || st.AxmRejected[RejectFormat] != 1 || st.AxmWarnedTotal != 1 || st.AxmWarned["role"] != 1 {
		t.Fatalf("response changed after more rows were processed: %+v", st)
	}
}
