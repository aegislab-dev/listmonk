package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/gofrs/uuid/v5"
	"github.com/jmoiron/sqlx"
	"github.com/knadh/goyesql/v2"
	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/utils"
	"github.com/knadh/listmonk/models"
	"github.com/knadh/stuffbin"
	"github.com/lib/pq"
	null "gopkg.in/volatiletech/null.v6"
)

// installMarkerKey is a settings row that exists only between the moment an install has
// written the schema and the moment it has finished the sample data and recorded its
// version. It is how a retry tells "my own unfinished install" from "somebody's data".
const installMarkerKey = "axm.install_pending"

// installLockID serialises installs on one database (advisory lock ids are per database).
const installLockID = 7419750075

// installCreds are the optional LISTMONK_ADMIN_* values of an install.
type installCreds struct {
	user, password, apiUser string
}

func installCredsFromEnv() installCreds {
	return installCreds{
		user:     os.Getenv("LISTMONK_ADMIN_USER"),
		password: os.Getenv("LISTMONK_ADMIN_PASSWORD"),
		apiUser:  os.Getenv("LISTMONK_ADMIN_API_USER"),
	}
}

// validate checks the values before anything is written, so a bad environment can be fixed
// and the install retried. It reports whether an admin user is to be created.
func (c installCreds) validate() (bool, error) {
	hasUser := c.user != "" && c.password != ""
	if hasUser && (len(c.user) < 3 || len(c.password) < 8) {
		return false, fmt.Errorf("LISTMONK_ADMIN_USER should be min 3 chars and LISTMONK_ADMIN_PASSWORD should be min 8 chars")
	}
	if c.apiUser != "" {
		if !hasUser {
			return false, fmt.Errorf("LISTMONK_ADMIN_API_USER requires LISTMONK_ADMIN_USER and LISTMONK_ADMIN_PASSWORD to be set")
		}
		if len(c.apiUser) < 3 {
			return false, fmt.Errorf("LISTMONK_ADMIN_API_USER should be min 3 chars")
		}
	}
	return hasUser, nil
}

// install runs the first time setup of setting up the database.
//
// Without idempotent it is the documented reset: it wipes listmonk's tables and types and
// sets them up again. With idempotent (the default Docker start command) it must never
// touch data: it installs only into a database that has none of listmonk's objects on its
// search path, finishes only its own interrupted install, and refuses everything else.
func install(lastVer string, db *sqlx.DB, fs stuffbin.FileSystem, prompt, idempotent bool) {
	qMap := readQueries(queryFilePath, fs)

	fmt.Println("")
	if !idempotent {
		fmt.Println("** first time installation **")
		fmt.Printf("** IMPORTANT: This will wipe existing listmonk tables and types in the DB '%s' **",
			ko.String("db.database"))
	} else {
		fmt.Println("** first time (idempotent) installation **")
	}
	fmt.Println("")

	if prompt {
		var ok string
		fmt.Print("continue (y/N)?  ")
		if _, err := fmt.Scanf("%s", &ok); err != nil {
			lo.Fatalf("error reading value from terminal: %v", err)
		}
		if strings.ToLower(ok) != "y" {
			fmt.Println("install cancelled.")
			return
		}
	}

	skipped, apiToken, err := runInstall(lastVer, db, fs, qMap, installCredsFromEnv(), idempotent)
	if err != nil {
		lo.Fatal(err)
	}
	if skipped {
		lo.Println("skipping install as database appears to be already setup")
		return
	}

	if apiToken != "" {
		// Print the token to stderr so that it can be grepped out.
		lo.Println("writing API token LISTMONK_ADMIN_API_TOKEN to stderr")
		fmt.Fprintf(os.Stderr, "export LISTMONK_ADMIN_API_TOKEN=\"%s\"\n", apiToken)
	}
	lo.Printf("setup complete")
	lo.Printf(`run the program and access the dashboard at %s`, ko.MustString("app.address"))
}

// runInstall does the install in two atomic steps. The schema goes in one transaction that
// also writes the marker; the sample data, the optional users, the removal of the marker and
// the migration record go in a second one. So the database is either empty, or has the
// schema and the marker (and no data), or is finished; a crash or an error never leaves a
// version recorded for a half-built database. skipped is true when the database was already
// installed. apiToken is the generated token of the optional API user.
func runInstall(lastVer string, db *sqlx.DB, fs stuffbin.FileSystem, qMap goyesql.Queries, c installCreds, idempotent bool) (skipped bool, apiToken string, err error) {
	hasUser, err := c.validate()
	if err != nil {
		return false, "", err
	}

	schemaSQL, err := fs.Read("/schema.sql")
	if err != nil {
		return false, "", fmt.Errorf("error reading schema: %v", err)
	}

	// One installer at a time: containers started together would otherwise both see an
	// empty database.
	ctx := context.Background()
	lock, err := db.Connx(ctx)
	if err != nil {
		return false, "", fmt.Errorf("error connecting to the database: %v", err)
	}
	defer lock.Close()
	if _, err := lock.ExecContext(ctx, "SELECT pg_advisory_lock($1)", installLockID); err != nil {
		return false, "", fmt.Errorf("error locking the database for install: %v", err)
	}
	defer lock.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", installLockID)

	needSchema := true
	if idempotent {
		state, why, err := inspectInstall(db, string(schemaSQL))
		if err != nil {
			return false, "", fmt.Errorf("error checking existing DB schema: %v", err)
		}
		switch state {
		case installDone:
			return true, "", nil
		case installUnfinished:
			lo.Println("finishing an install that was interrupted before its sample data was written")
			needSchema = false
		case installForeign:
			return false, "", fmt.Errorf("refusing to install: %s. Nothing was changed. "+
				"This is not an empty database, and listmonk does not recognise it as a finished or an interrupted install, "+
				"so installing would overwrite its data. Restore the record of the installed version "+
				"(settings key 'migrations') or point listmonk at an empty database. "+
				"Use --install without --idempotent only to wipe on purpose", why)
		}
	}

	if needSchema {
		if err := installSchema(ctx, db, string(schemaSQL)); err != nil {
			return false, "", fmt.Errorf("error migrating DB schema: %v", err)
		}
	}

	// Load the queries.
	q := prepareQueries(qMap, db, ko)

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback()

	if apiToken, err = installData(tx, q, fs, c, hasUser); err != nil {
		return false, "", fmt.Errorf("error installing the sample data (nothing of it was kept, run the install again): %v", err)
	}
	if _, err := tx.Exec(`DELETE FROM settings WHERE key = $1`, installMarkerKey); err != nil {
		return false, "", err
	}
	// Record the version last: from here on the install counts as finished.
	if err := recordMigrationVersion(lastVer, tx); err != nil {
		return false, "", fmt.Errorf("error recording migration version: %v", err)
	}
	if err := tx.Commit(); err != nil {
		return false, "", err
	}
	return false, apiToken, nil
}

// txStmt binds a prepared statement to the transaction. sqlx does not carry the unsafe mode
// of the statement (the app reads rows in it) over to the transaction's statement.
func txStmt(tx *sqlx.Tx, s *sqlx.Stmt) *sqlx.Stmt {
	return tx.Stmtx(s).Unsafe()
}

// installData writes the sample data and the optional admin users in the given transaction.
func installData(tx *sqlx.Tx, q *models.Queries, fs stuffbin.FileSystem, c installCreds, hasUser bool) (string, error) {
	defList, optinList, err := installLists(tx, q)
	if err != nil {
		return "", err
	}
	if err := installSubs(tx, defList, optinList, q); err != nil {
		return "", err
	}
	campTplID, archiveTplID, err := installTemplates(tx, q, fs)
	if err != nil {
		return "", err
	}
	if err := installCampaign(tx, campTplID, archiveTplID, defList, q); err != nil {
		return "", err
	}

	if hasUser {
		lo.Printf("creating superadmin user '%s'", c.user)
		if c.apiUser != "" {
			lo.Printf("creating superadmin API user '%s'", c.apiUser)
		}
		return installUser(tx, c.user, c.password, c.apiUser, q)
	}
	lo.Printf("no superadmin user created. Visit webpage to create user.")
	return "", nil
}

// installSchema executes the SQL schema and the marker in one transaction. The schema is
// one multi-statement string; run inside the transaction, it either applies completely or
// not at all.
func installSchema(ctx context.Context, db *sqlx.DB, schemaSQL string) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(schemaSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES ($1, 'true'::JSONB) ON CONFLICT (key) DO NOTHING`, installMarkerKey); err != nil {
		return err
	}
	return tx.Commit()
}

// installState is what a database holds, as far as installing into it is concerned.
type installState int

const (
	// installFresh: none of the objects the schema creates or drops exists on the search path.
	installFresh installState = iota
	// installDone: a version is recorded in the settings table.
	installDone
	// installUnfinished: this installer wrote the schema and the marker and no data followed.
	installUnfinished
	// installForeign: objects exist, and they are neither of the above.
	installForeign
)

// schemaObjects are the names schema.sql creates or drops. Dropping is what makes a plain
// install destructive: DROP ... IF EXISTS takes the first object of that name on the search
// path, whichever schema it is in and whoever owns it.
type schemaObjects struct {
	relations map[string]bool // tables, materialized views, indexes, sequences
	types     map[string]bool
}

var reSchemaObject = regexp.MustCompile(`(?is)\b(?:DROP|CREATE)\s+(?:UNLOGGED\s+|UNIQUE\s+)?(TABLE|TYPE|INDEX|MATERIALIZED\s+VIEW|VIEW|SEQUENCE)\s+(?:IF\s+(?:NOT\s+)?EXISTS\s+)?("?[A-Za-z_][A-Za-z0-9_]*"?)`)

// parseSchemaObjects reads the names out of the schema script.
func parseSchemaObjects(schemaSQL string) schemaObjects {
	var lines []string
	for _, l := range strings.Split(schemaSQL, "\n") {
		if i := strings.Index(l, "--"); i >= 0 {
			l = l[:i]
		}
		lines = append(lines, l)
	}

	out := schemaObjects{relations: map[string]bool{}, types: map[string]bool{}}
	for _, m := range reSchemaObject.FindAllStringSubmatch(strings.Join(lines, "\n"), -1) {
		name := strings.ToLower(strings.Trim(m[2], `"`))
		if name == "on" || name == "if" {
			continue
		}
		if strings.EqualFold(m[1], "TYPE") {
			out.types[name] = true
		} else {
			out.relations[name] = true
		}
	}
	return out
}

// existingObject is an object of the schema found on the search path.
type existingObject struct {
	Schema string `db:"schema"`
	Name   string `db:"name"`
	Kind   string `db:"kind"` // relkind, or 'type'
}

func (o existingObject) String() string { return o.Schema + "." + o.Name }

// findSchemaObjects lists the objects of the schema that exist in any schema of the search
// path, the first one first.
func findSchemaObjects(db *sqlx.DB, objs schemaObjects) ([]existingObject, error) {
	rels, types := []string{}, []string{}
	for n := range objs.relations {
		rels = append(rels, n)
	}
	for n := range objs.types {
		types = append(types, n)
	}

	var out []existingObject
	err := db.Select(&out, `
		SELECT * FROM (
			SELECT n.nspname::TEXT AS schema, c.relname::TEXT AS name, c.relkind::TEXT AS kind
			FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = ANY(current_schemas(false)) AND c.relname::TEXT = ANY($1)
			UNION ALL
			SELECT n.nspname::TEXT, t.typname::TEXT, 'type'
			FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
			WHERE n.nspname = ANY(current_schemas(false)) AND t.typname::TEXT = ANY($2)
				AND t.typrelid = 0 AND t.typcategory <> 'A'
		) o ORDER BY array_position(current_schemas(false), schema::NAME), name`,
		pq.Array(rels), pq.Array(types))
	return out, err
}

// inspectInstall classifies the database. why describes what was found when it is foreign.
func inspectInstall(db *sqlx.DB, schemaSQL string) (installState, string, error) {
	found, err := findSchemaObjects(db, parseSchemaObjects(schemaSQL))
	if err != nil {
		return 0, "", err
	}
	if len(found) == 0 {
		return installFresh, "", nil
	}

	names := []string{}
	for i, o := range found {
		if i == 5 {
			names = append(names, fmt.Sprintf("and %d more", len(found)-5))
			break
		}
		names = append(names, o.String())
	}
	why := fmt.Sprintf("the database already has listmonk objects (%s) and no record of a finished install", strings.Join(names, ", "))

	recorded, err := getRecordedMigrations(db)
	if err != nil {
		// A settings table of another shape, or a record that is not a list of versions.
		return installForeign, fmt.Sprintf("%s (reading its settings: %v)", why, err), nil
	}
	if len(recorded) > 0 {
		return installDone, "", nil
	}

	// No version recorded. It is this installer's own unfinished install only when it left its
	// marker and no row of data (the sample data is written in one transaction after it).
	var marked bool
	var hasSettings bool
	if err := db.Get(&hasSettings, `SELECT to_regclass('settings') IS NOT NULL`); err != nil {
		return 0, "", err
	}
	if hasSettings {
		if err := db.Get(&marked, `SELECT EXISTS (SELECT 1 FROM settings WHERE key = $1)`, installMarkerKey); err != nil {
			return installForeign, fmt.Sprintf("%s (reading its settings: %v)", why, err), nil
		}
	}
	if !marked {
		return installForeign, why, nil
	}
	for _, o := range found {
		if o.Kind != "r" && o.Kind != "p" || o.Name == "settings" {
			continue
		}
		var has bool
		if err := db.Get(&has, fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s.%s)",
			pq.QuoteIdentifier(o.Schema), pq.QuoteIdentifier(o.Name))); err != nil {
			return 0, "", err
		}
		if has {
			return installForeign, fmt.Sprintf("an unfinished install has rows in %s", o), nil
		}
	}
	return installUnfinished, "", nil
}

func installLists(tx *sqlx.Tx, q *models.Queries) (int, int, error) {
	var (
		defList   int
		optinList int
	)
	if err := txStmt(tx, q.CreateList).Get(&defList,
		uuid.Must(uuid.NewV4()),
		"Default list",
		models.ListTypePrivate,
		models.ListOptinSingle,
		models.ListStatusActive,
		pq.StringArray{"test"},
		"", nil, core.DefaultColumnPreset,
	); err != nil {
		return 0, 0, fmt.Errorf("error creating list: %v", err)
	}

	if err := txStmt(tx, q.CreateList).Get(&optinList, uuid.Must(uuid.NewV4()),
		"Opt-in list",
		models.ListTypePublic,
		models.ListOptinDouble,
		models.ListStatusActive,
		pq.StringArray{"test"},
		"", nil, core.DefaultColumnPreset,
	); err != nil {
		return 0, 0, fmt.Errorf("error creating list: %v", err)
	}

	return defList, optinList, nil
}

func installSubs(tx *sqlx.Tx, defListID, optinListID int, q *models.Queries) error {
	// Sample subscriber.
	if _, err := txStmt(tx, q.UpsertSubscriber).Exec(
		uuid.Must(uuid.NewV4()),
		"john@example.com",
		"John Doe",
		`{"type": "known", "good": true, "city": "Bengaluru"}`,
		pq.Int64Array{int64(defListID)},
		models.SubscriptionStatusUnconfirmed,
		true, true); err != nil {
		return fmt.Errorf("error creating subscriber: %v", err)
	}
	if _, err := txStmt(tx, q.UpsertSubscriber).Exec(
		uuid.Must(uuid.NewV4()),
		"anon@example.com",
		"Anon Doe",
		`{"type": "unknown", "good": true, "city": "Bengaluru"}`,
		pq.Int64Array{int64(optinListID)},
		models.SubscriptionStatusUnconfirmed,
		true, true); err != nil {
		return fmt.Errorf("error creating subscriber: %v", err)
	}
	return nil
}

func installTemplates(tx *sqlx.Tx, q *models.Queries, fs stuffbin.FileSystem) (int, int, error) {
	// Default campaign template.
	campTpl, err := fs.Get("/static/email-templates/default.tpl")
	if err != nil {
		return 0, 0, fmt.Errorf("error reading default e-mail template: %v", err)
	}

	var campTplID int
	if err := txStmt(tx, q.CreateTemplate).Get(&campTplID, "Default campaign template", models.TemplateTypeCampaign, "", campTpl.ReadBytes(), nil); err != nil {
		return 0, 0, fmt.Errorf("error creating default campaign template: %v", err)
	}
	if _, err := txStmt(tx, q.SetDefaultTemplate).Exec(campTplID); err != nil {
		return 0, 0, fmt.Errorf("error setting default template: %v", err)
	}

	// Default campaign archive template.
	archiveTpl, err := fs.Get("/static/email-templates/default-archive.tpl")
	if err != nil {
		return 0, 0, fmt.Errorf("error reading default archive template: %v", err)
	}

	var archiveTplID int
	if err := txStmt(tx, q.CreateTemplate).Get(&archiveTplID, "Default archive template", models.TemplateTypeCampaign, "", archiveTpl.ReadBytes(), nil); err != nil {
		return 0, 0, fmt.Errorf("error creating default archive template: %v", err)
	}

	// Sample tx template.
	txTpl, err := fs.Get("/static/email-templates/sample-tx.tpl")
	if err != nil {
		return 0, 0, fmt.Errorf("error reading sample transactional template: %v", err)
	}

	if _, err := txStmt(tx, q.CreateTemplate).Exec("Sample transactional template", models.TemplateTypeTx, "Welcome {{ .Subscriber.Name }}", txTpl.ReadBytes(), nil); err != nil {
		return 0, 0, fmt.Errorf("error creating sample transactional template: %v", err)
	}

	// Sample visual campaign template.
	visualTpl, err := fs.Get("/static/email-templates/default-visual.tpl")
	if err != nil {
		return 0, 0, fmt.Errorf("error reading default visual template: %v", err)
	}
	visualSrc, err := fs.Get("/static/email-templates/default-visual.json")
	if err != nil {
		return 0, 0, fmt.Errorf("error reading default visual template json: %v", err)
	}

	if _, err := txStmt(tx, q.CreateTemplate).Exec("Sample visual template", models.TemplateTypeCampaignVisual, "", visualTpl.ReadBytes(), visualSrc.ReadBytes()); err != nil {
		return 0, 0, fmt.Errorf("error creating sample visual template: %v", err)
	}

	return campTplID, archiveTplID, nil
}

func installCampaign(tx *sqlx.Tx, campTplID, archiveTplID, listID int, q *models.Queries) error {
	// Sample campaign.
	if _, err := txStmt(tx, q.CreateCampaign).Exec(uuid.Must(uuid.NewV4()),
		models.CampaignTypeRegular,
		"Test campaign",
		"Welcome to listmonk",
		"No Reply <noreply@yoursite.com>",
		`<h3>Hi {{ .Subscriber.FirstName }}!</h3>
		<p>This is a test e-mail campaign. Your second name is {{ .Subscriber.LastName }} and you are from {{ .Subscriber.Attribs.city }}.</p>
		<p>Here is a <a href="https://listmonk.app@TrackLink">tracked link</a>.</p>
		<p>Use the link icon in the editor toolbar or when writing raw HTML or Markdown,
			simply suffix @TrackLink to the end of a URL to turn it into a tracking link. Example:</p>
		<pre>&lt;a href=&quot;https:/&zwnj;/listmonk.app&#064;TrackLink&quot;&gt;&lt;/a&gt;</pre>
		<p>For help, refer to the <a href="https://listmonk.app/docs">documentation</a>.</p>
		`,
		nil,
		"richtext",
		nil,
		json.RawMessage("[]"),
		json.RawMessage("{}"),
		pq.StringArray{"test-campaign"},
		emailMsgr,
		campTplID,
		pq.Int64Array{int64(listID)},
		false,
		"welcome-to-listmonk",
		archiveTplID,
		`{"name": "Subscriber"}`,
		nil,
		nil,
		"", // Reply-To is empty for the sample campaign.
	); err != nil {
		return fmt.Errorf("error creating sample campaign: %v", err)
	}
	return nil
}

// recordMigrationVersion inserts the given version (of DB migration) into the
// `migrations` array in the settings table.
// It takes the DB or a transaction.
func recordMigrationVersion(ver string, db sqlx.Execer) error {
	_, err := db.Exec(`INSERT INTO settings (key, value)
	VALUES('migrations', JSONB_BUILD_ARRAY($1::TEXT))
	ON CONFLICT (key) DO UPDATE SET value = settings.value || EXCLUDED.value`, ver)
	return err
}

func newConfigFile(path string) error {
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return fmt.Errorf("error creating %s: %v", path, err)
	}

	// Initialize the static file system into which all
	// required static assets (.sql, .js files etc.) are loaded.
	fs := initFS(appDir, "", "", "")
	b, err := fs.Read("config.toml.sample")
	if err != nil {
		return fmt.Errorf("error reading sample config (is binary stuffed?): %v", err)
	}

	return os.WriteFile(path, b, 0644)
}

// checkSchema checks if the DB schema is installed.
func checkSchema(db *sqlx.DB) (bool, error) {
	if _, err := db.Exec(`SELECT id FROM templates LIMIT 1`); err != nil {
		if isTableNotExistErr(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func installUser(tx *sqlx.Tx, username, password, apiUsername string, q *models.Queries) (string, error) {
	consts := initConstConfig(ko)

	// Super Admin role gets all permissions.
	perms := []string{}
	for p := range consts.Permissions {
		perms = append(perms, p)
	}

	// Create the Super Admin role in the DB.
	var role auth.Role
	if err := txStmt(tx, q.CreateRole).Get(&role, "Super Admin", auth.RoleTypeUser, pq.Array(perms)); err != nil {
		return "", fmt.Errorf("error creating super admin role: %v", err)
	}

	// Create the admin user.
	if _, err := txStmt(tx, q.CreateUser).Exec(username, true, password, username+"@listmonk", username, auth.RoleTypeUser, role.ID, nil, auth.UserStatusEnabled); err != nil {
		return "", fmt.Errorf("error creating superadmin user: %v", err)
	}

	// Create the admin API user. The token is returned to be printed once the install is committed.
	if apiUsername == "" {
		return "", nil
	}

	// Generate a random API token.
	tk, err := utils.GenerateRandomString(32)
	if err != nil {
		return "", fmt.Errorf("error generating API token: %v", err)
	}

	var (
		email = null.String{String: apiUsername + "@api", Valid: true}
		hash  = null.String{String: auth.HashAPIToken(tk), Valid: true}
	)

	if _, err := txStmt(tx, q.CreateUser).Exec(apiUsername, false, hash, email, apiUsername, auth.UserTypeAPI, role.ID, nil, auth.UserStatusEnabled); err != nil {
		return "", fmt.Errorf("error creating superadmin API user: %v", err)
	}
	return tk, nil
}
