package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	urlpkg "net/url"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/stuffbin"
	"github.com/lib/pq"
)

// These tests run the real install() the way the CLI does (in a child process, because it
// exits with log.Fatal / os.Exit) against a database of their own. Every test creates a
// separate DATABASE on the server named by AXM_TEST_DB_URL and drops it afterwards, so the
// objects that must survive (the "public" ones the old installer wiped) are the test's
// own and never the shared test database's.

// brokenFS fails to read one file, to interrupt an install after it has started writing.
type brokenFS struct {
	stuffbin.FileSystem
	path string
}

func (b brokenFS) Get(path string) (*stuffbin.File, error) {
	if path == b.path {
		return nil, fmt.Errorf("injected failure reading %s", path)
	}
	return b.FileSystem.Get(path)
}

// TestInstallChildProcess is the child of instDB.install; it does nothing in a normal run.
func TestInstallChildProcess(t *testing.T) {
	dsn := os.Getenv("AXM_INSTALL_CHILD_DSN")
	if dsn == "" {
		t.Skip("helper process of the install tests")
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	// The app reads rows in sqlx's unsafe mode (initDB).
	db = db.Unsafe()
	local, err := stuffbin.NewLocalFS("/", repoRoot(t)+":/")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var f stuffbin.FileSystem = local
	if p := os.Getenv("AXM_INSTALL_CHILD_BREAK"); p != "" {
		f = brokenFS{local, p}
	}
	// install() and the helpers it calls read the package-level file system.
	fs = f
	if err := ko.Set("app.address", "localhost:9000"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	install(lastMigration(), db, f, false, os.Getenv("AXM_INSTALL_CHILD_IDEMPOTENT") == "1")
	os.Exit(0)
}

func lastMigration() string { return migList[len(migList)-1].version }

type installRun struct {
	idempotent bool
	breakFile  string // a file that fails to read
	searchPath string
	env        []string // LISTMONK_ADMIN_* values
}

type installResult struct {
	exit   int
	output string
}

// instDB is one test's own database.
type instDB struct {
	t    *testing.T
	name string
	dsn  string
	db   *sqlx.DB
}

func newInstDB(t *testing.T) *instDB {
	t.Helper()
	base := os.Getenv("AXM_TEST_DB_URL")
	if base == "" {
		t.Skip("AXM_TEST_DB_URL not set")
	}
	admin, err := sqlx.Connect("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("axm_inst_%d_%d", time.Now().UnixNano(), schemaSeq.Add(1))
	admin.MustExec("CREATE DATABASE " + pq.QuoteIdentifier(name))
	u, err := urlpkg.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	e := &instDB{t: t, name: name, dsn: u.String()}
	e.db, err = sqlx.Connect("postgres", e.dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e.db.Close()
		a, err := sqlx.Connect("postgres", base)
		if err != nil {
			return
		}
		defer a.Close()
		a.Exec("DROP DATABASE IF EXISTS " + pq.QuoteIdentifier(name) + " WITH (FORCE)")
	})
	return e
}

func (e *instDB) cmd(o installRun) *exec.Cmd {
	dsn := e.dsn
	if o.searchPath != "" {
		dsn = withSearchPath(e.t, dsn, o.searchPath)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestInstallChildProcess$", "-test.count=1")
	env := []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "LISTMONK_ADMIN_") && !strings.HasPrefix(v, "AXM_INSTALL_CHILD_") {
			env = append(env, v)
		}
	}
	env = append(env, "AXM_INSTALL_CHILD_DSN="+dsn, "AXM_INSTALL_CHILD_BREAK="+o.breakFile)
	if o.idempotent {
		env = append(env, "AXM_INSTALL_CHILD_IDEMPOTENT=1")
	}
	cmd.Env = append(env, o.env...)
	return cmd
}

func (e *instDB) install(o installRun) installResult {
	e.t.Helper()
	cmd := e.cmd(o)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	res := installResult{output: out.String()}
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			e.t.Fatal(err)
		}
		res.exit = ee.ExitCode()
	}
	return res
}

func (e *instDB) mustInstall(o installRun) {
	e.t.Helper()
	if r := e.install(o); r.exit != 0 {
		e.t.Fatalf("install exited %d:\n%s", r.exit, r.output)
	}
}

func (e *instDB) count(table string) int {
	e.t.Helper()
	var n int
	if err := e.db.Get(&n, "SELECT count(*) FROM "+table); err != nil {
		e.t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func (e *instDB) tableExists(qualified string) bool {
	e.t.Helper()
	var ok bool
	if err := e.db.Get(&ok, "SELECT to_regclass($1) IS NOT NULL", qualified); err != nil {
		e.t.Fatal(err)
	}
	return ok
}

// recorded returns the recorded migration versions (nil when there is no settings table).
func (e *instDB) recorded() []string {
	e.t.Helper()
	if !e.tableExists("public.settings") {
		return nil
	}
	var raw []byte
	if err := e.db.Get(&raw, "SELECT COALESCE((SELECT value FROM public.settings WHERE key='migrations'), '[]'::JSONB)"); err != nil {
		e.t.Fatal(err)
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		e.t.Fatal(err)
	}
	return out
}

// wantSample checks what a finished install leaves behind.
func (e *instDB) wantSample(users int) {
	e.t.Helper()
	for table, want := range map[string]int{"public.lists": 2, "public.subscribers": 2, "public.templates": 4, "public.campaigns": 1, "public.users": users} {
		if got := e.count(table); got != want {
			e.t.Errorf("%s has %d rows after install, want %d", table, got, want)
		}
	}
	if got := e.recorded(); len(got) != 1 || got[0] != lastMigration() {
		e.t.Errorf("recorded migrations = %v, want [%s]", got, lastMigration())
	}
}

// A first install on an empty database sets up the sample data once and records the
// version; running it again changes nothing.
func TestInstallIdempotentFreshThenAgain(t *testing.T) {
	e := newInstDB(t)
	e.mustInstall(installRun{idempotent: true})
	e.wantSample(0)

	e.db.MustExec("INSERT INTO public.subscribers (uuid, email, name) VALUES (gen_random_uuid(), 'keep@example.com', 'Keep')")
	e.mustInstall(installRun{idempotent: true})
	if got := e.count("public.subscribers"); got != 3 {
		t.Fatalf("a second idempotent install changed the subscribers: %d", got)
	}
}

// The default Docker start command runs --install --idempotent on every start. On a
// database that already holds listmonk data it must not recreate anything, whatever is
// missing from the bookkeeping.
func TestInstallIdempotentNeverWipesExistingData(t *testing.T) {
	keep := func(e *instDB) {
		e.db.MustExec("INSERT INTO public.subscribers (uuid, email, name) VALUES (gen_random_uuid(), 'keep@example.com', 'Keep')")
		e.db.MustExec("INSERT INTO public.templates (name, type, subject, body) VALUES ('Keep template', 'campaign', '', 'x')")
	}
	intact := func(t *testing.T, e *instDB, r installResult) {
		t.Helper()
		if got := e.count("public.subscribers"); got != 3 {
			t.Fatalf("subscribers: %d, want 3 (install changed existing data)\n%s", got, r.output)
		}
		if got := e.count("public.templates"); got != 5 {
			t.Fatalf("templates: %d, want 5\n%s", got, r.output)
		}
	}
	refused := func(t *testing.T, e *instDB, o installRun) {
		t.Helper()
		r := e.install(o)
		if r.exit == 0 {
			t.Errorf("install over unrecorded data reported success:\n%s", r.output)
		}
		if !strings.Contains(r.output, "refus") {
			t.Errorf("no clear refusal message:\n%s", r.output)
		}
		intact(t, e, r)
	}

	t.Run("settings has no migration record", func(t *testing.T) {
		e := newInstDB(t)
		e.mustInstall(installRun{idempotent: true})
		keep(e)
		e.db.MustExec("DELETE FROM public.settings WHERE key = 'migrations'")
		refused(t, e, installRun{idempotent: true})
	})

	t.Run("migration record is an empty list", func(t *testing.T) {
		e := newInstDB(t)
		e.mustInstall(installRun{idempotent: true})
		keep(e)
		e.db.MustExec("UPDATE public.settings SET value = '[]' WHERE key = 'migrations'")
		refused(t, e, installRun{idempotent: true})
	})

	t.Run("no settings table at all", func(t *testing.T) {
		e := newInstDB(t)
		e.mustInstall(installRun{idempotent: true})
		keep(e)
		e.db.MustExec("DROP TABLE public.settings CASCADE")
		refused(t, e, installRun{idempotent: true})
	})

	t.Run("an empty schema comes first on the search path", func(t *testing.T) {
		e := newInstDB(t)
		e.mustInstall(installRun{idempotent: true})
		keep(e)
		e.db.MustExec("CREATE SCHEMA lm_empty")
		// The finished install is found behind the empty schema and left alone.
		r := e.install(installRun{idempotent: true, searchPath: "lm_empty,public"})
		if r.exit != 0 {
			t.Fatalf("exit %d:\n%s", r.exit, r.output)
		}
		intact(t, e, r)
		if e.tableExists("lm_empty.settings") || e.tableExists("lm_empty.subscribers") {
			t.Fatal("install created tables in the empty schema although public holds the install")
		}
		// The same with public's settings table gone: the data in public must still survive.
		e.db.MustExec("DROP TABLE public.settings CASCADE")
		refused(t, e, installRun{idempotent: true, searchPath: "lm_empty,public"})
		if e.tableExists("lm_empty.subscribers") {
			t.Fatal("install created tables in the empty schema over public's data")
		}
	})

	t.Run("another application's tables share the names", func(t *testing.T) {
		e := newInstDB(t)
		e.db.MustExec(`CREATE TABLE public.users (id INT PRIMARY KEY, login TEXT);
			INSERT INTO public.users VALUES (1, 'someone'), (2, 'other');
			CREATE TABLE public.media (id INT PRIMARY KEY);
			INSERT INTO public.media VALUES (7);
			CREATE TYPE public.content_type AS ENUM ('a', 'b');
			CREATE TABLE public.other_app (id INT, kind public.content_type);
			INSERT INTO public.other_app VALUES (1, 'a')`)
		r := e.install(installRun{idempotent: true})
		if r.exit == 0 || !strings.Contains(r.output, "refus") {
			t.Errorf("installed over another application's tables (exit %d):\n%s", r.exit, r.output)
		}
		if e.count("public.users") != 2 || e.count("public.media") != 1 || e.count("public.other_app") != 1 {
			t.Fatalf("another application's data was changed:\n%s", r.output)
		}
		if e.tableExists("public.settings") {
			t.Fatal("listmonk's schema was created next to the other application's tables")
		}
	})
}

// An install that stops half way must not leave a database that looks finished: the
// retry has to complete the setup exactly once.
func TestInstallInterruptedThenRetried(t *testing.T) {
	for name, env := range map[string][]string{
		"no admin user":   nil,
		"with admin user": {"LISTMONK_ADMIN_USER=admin", "LISTMONK_ADMIN_PASSWORD=long-enough-password", "LISTMONK_ADMIN_API_USER=apiuser"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newInstDB(t)
			// The third sample template fails to read, after the lists, subscribers and two
			// templates have been written.
			r := e.install(installRun{idempotent: true, breakFile: "/static/email-templates/sample-tx.tpl", env: env})
			if r.exit == 0 {
				t.Fatalf("the injected failure did not stop the install:\n%s", r.output)
			}
			if got := e.recorded(); len(got) != 0 {
				t.Errorf("an interrupted install was recorded as version %v", got)
			}
			if e.tableExists("public.templates") && e.count("public.templates") != 0 {
				t.Errorf("an interrupted install left %d templates", e.count("public.templates"))
			}
			if e.tableExists("public.lists") && e.count("public.lists") != 0 {
				t.Errorf("an interrupted install left %d lists", e.count("public.lists"))
			}

			users := 0
			if env != nil {
				users = 2
			}
			e.mustInstall(installRun{idempotent: true, env: env})
			e.wantSample(users)
			// The sample campaign belongs to the default list, not to a hard-coded id.
			var onDefault int
			if err := e.db.Get(&onDefault, "SELECT count(*) FROM public.campaign_lists cl JOIN public.lists l ON l.id = cl.list_id WHERE l.name = 'Default list'"); err != nil {
				t.Fatal(err)
			}
			if onDefault != 1 {
				t.Errorf("the sample campaign is on the default list %d times, want 1", onDefault)
			}

			// And a third run is a no-op.
			e.mustInstall(installRun{idempotent: true, env: env})
			e.wantSample(users)
		})
	}
}

// Invalid credentials are refused before anything is written, also on a finished database.
func TestInstallInvalidCredentialsWriteNothing(t *testing.T) {
	bad := []string{"LISTMONK_ADMIN_USER=x", "LISTMONK_ADMIN_PASSWORD=long-enough-password"}
	e := newInstDB(t)
	r := e.install(installRun{idempotent: true, env: bad})
	if r.exit == 0 || !strings.Contains(r.output, "LISTMONK_ADMIN_USER should be min 3 chars") {
		t.Fatalf("invalid credentials were not refused (exit %d):\n%s", r.exit, r.output)
	}
	if e.tableExists("public.settings") || e.tableExists("public.subscribers") {
		t.Fatal("tables were created before the credentials were rejected")
	}
	e.mustInstall(installRun{idempotent: true})
	e.wantSample(0)
	r = e.install(installRun{idempotent: true, env: bad})
	if r.exit == 0 {
		t.Fatalf("invalid credentials were accepted on a finished database:\n%s", r.output)
	}
	e.wantSample(0)
}

// Containers started together must not install twice.
func TestInstallConcurrentStarts(t *testing.T) {
	e := newInstDB(t)
	var wg sync.WaitGroup
	results := make([]installResult, 3)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = e.install(installRun{idempotent: true})
		}(i)
	}
	wg.Wait()
	for i, r := range results {
		if r.exit != 0 {
			t.Errorf("start %d exited %d:\n%s", i, r.exit, r.output)
		}
	}
	e.wantSample(0)
}

// Without --idempotent the install is the documented reset: it wipes and sets up again.
func TestInstallExplicitResetStillWipes(t *testing.T) {
	e := newInstDB(t)
	e.mustInstall(installRun{idempotent: true})
	e.db.MustExec("INSERT INTO public.subscribers (uuid, email, name) VALUES (gen_random_uuid(), 'gone@example.com', 'Gone')")
	e.mustInstall(installRun{})
	e.wantSample(0)
}

// An upgrade must not run every migration from the oldest one over a database that has
// data but no record of its version.
func TestUpgradeRefusesUnrecordedDatabase(t *testing.T) {
	e := newInstDB(t)
	e.mustInstall(installRun{idempotent: true})
	e.db.MustExec("DELETE FROM public.settings WHERE key = 'migrations'")
	if _, toRun, err := getPendingMigrations(e.db); err == nil {
		t.Fatalf("%d migrations are pending on a database without a record, want an error", len(toRun))
	}
	e.db.MustExec("INSERT INTO public.settings (key, value) VALUES ('migrations', '[]')")
	if _, _, err := getPendingMigrations(e.db); err == nil {
		t.Fatal("an empty record was accepted")
	}
	// A database from before settings existed (v0.7.0) has no record either, and upgrades.
	e.db.MustExec("DROP TABLE public.settings CASCADE")
	if _, toRun, err := getPendingMigrations(e.db); err != nil || len(toRun) != len(migList) {
		t.Fatalf("a database without a settings table: %d of %d pending, %v", len(toRun), len(migList), err)
	}
}

// The guard of an idempotent install is built from the names schema.sql creates and drops.
// Everything a fresh install leaves behind must be among them, or a schema.sql change could
// add an object that the guard does not see.
func TestInstallGuardNamesEverythingTheSchemaBuilds(t *testing.T) {
	e := newInstDB(t)
	e.mustInstall(installRun{idempotent: true})
	raw, err := os.ReadFile(repoRoot(t) + "/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	objs := parseSchemaObjects(string(raw))

	var rels, types []string
	if err := e.db.Select(&rels, `
		SELECT c.relname::TEXT FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p', 'm')`); err != nil {
		t.Fatal(err)
	}
	// Indexes: named ones are in the list; an unnamed one (CREATE INDEX ON t) is covered by its table.
	var idx []struct {
		Name  string `db:"name"`
		Table string `db:"tbl"`
	}
	if err := e.db.Select(&idx, `
		SELECT c.relname::TEXT AS name, t.relname::TEXT AS tbl FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid JOIN pg_class t ON t.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND NOT EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conindid = c.oid)`); err != nil {
		t.Fatal(err)
	}
	for _, i := range idx {
		if !objs.relations[i.Name] && !objs.relations[i.Table] {
			t.Errorf("the install guard knows neither the index %s nor its table %s", i.Name, i.Table)
		}
	}
	if len(rels) < 30 {
		t.Fatalf("only %d relations found in a fresh install: %v", len(rels), rels)
	}
	for _, n := range rels {
		if !objs.relations[n] {
			t.Errorf("the install guard does not know the relation %s that schema.sql creates", n)
		}
	}
	if err := e.db.Select(&types, `SELECT t.typname::TEXT FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
		WHERE n.nspname = 'public' AND t.typtype = 'e'`); err != nil {
		t.Fatal(err)
	}
	if len(types) < 10 {
		t.Fatalf("only %d enum types found: %v", len(types), types)
	}
	for _, n := range types {
		if !objs.types[n] {
			t.Errorf("the install guard does not know the type %s that schema.sql creates", n)
		}
	}
}
