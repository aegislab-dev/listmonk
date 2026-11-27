package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/gofrs/uuid/v5"
	"github.com/jmoiron/sqlx"
	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/utils"
	"github.com/knadh/listmonk/models"
	"github.com/knadh/stuffbin"
	"github.com/lib/pq"
	null "gopkg.in/volatiletech/null.v6"
)

// install runs the first time setup of setting up the database.
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

	// If idempotence is on, check if the DB is already setup.
	if idempotent {
		complete, err := isInstallationComplete(db)
		if err != nil {
			lo.Fatalf("error checking existing DB schema: %v", err)
		}
		if complete {
			lo.Println("skipping install as database appears to be already setup")
			os.Exit(0)
		}
	}

	user := os.Getenv("LISTMONK_ADMIN_USER")
	password := os.Getenv("LISTMONK_ADMIN_PASSWORD")
	apiUser := os.Getenv("LISTMONK_ADMIN_API_USER")
	if err := validateInstallBeforeSetup(user, password, apiUser, func(hasUser bool) error {
		// Migrate the tables only after all install credentials have been validated.
		if err := installSchema(db, fs); err != nil {
			return fmt.Errorf("error migrating DB schema: %w", err)
		}

		// Load the queries.
		q := prepareQueries(qMap, db, ko)
		defList, optinList := installLists(q)
		installSubs(defList, optinList, q)
		campTplID, archiveTplID := installTemplates(q, fs)
		installCampaign(campTplID, archiveTplID, q)

		if user != "" && password != "" {
			lo.Printf("creating superadmin user '%s'", user)
		} else {
			lo.Printf("no superadmin user created. Visit webpage to create user.")
		}
		if apiUser != "" {
			lo.Printf("creating superadmin API user '%s'", apiUser)
		}
		if hasUser {
			installUser(user, password, apiUser, q)
		}
		return nil
	}); err != nil {
		lo.Fatal(err)
	}

	// Record installation only after the default lists, subscribers, templates, campaign,
	// and optional user have all been created. An interrupted install remains retryable.
	if err := recordMigrationVersion(lastVer, db); err != nil {
		lo.Fatalf("error recording migration version: %v", err)
	}

	lo.Printf("setup complete")
	lo.Printf(`run the program and access the dashboard at %s`, ko.MustString("app.address"))
}

// validateInstallUsers checks configuration that would otherwise fail after the schema and
// default data have already been written. Keep it before any installation side effects so an
// operator can correct the environment and retry safely.
func validateInstallUsers(user, password, apiUser string) (bool, error) {
	hasUser := user != "" && password != ""
	if hasUser && (len(user) < 3 || len(password) < 8) {
		return false, fmt.Errorf("LISTMONK_ADMIN_USER should be min 3 chars and LISTMONK_ADMIN_PASSWORD should be min 8 chars")
	}
	if apiUser != "" {
		if !hasUser {
			return false, fmt.Errorf("LISTMONK_ADMIN_API_USER requires LISTMONK_ADMIN_USER and LISTMONK_ADMIN_PASSWORD to be set")
		}
		if len(apiUser) < 3 {
			return false, fmt.Errorf("LISTMONK_ADMIN_API_USER should be min 3 chars")
		}
	}
	return hasUser, nil
}

// validateInstallBeforeSetup is the install side-effect barrier: configuration is checked
// before schema or default data setup can run. Keep install's setup behind this function so
// the regression test exercises the same ordering as the CLI.
func validateInstallBeforeSetup(user, password, apiUser string, setup func(hasUser bool) error) error {
	hasUser, err := validateInstallUsers(user, password, apiUser)
	if err != nil {
		return err
	}
	return setup(hasUser)
}

// installSchema executes the SQL schema and creates the necessary tables and types.
func installSchema(db *sqlx.DB, fs stuffbin.FileSystem) error {
	q, err := fs.Read("/schema.sql")
	if err != nil {
		return err
	}

	// Keep the schema execution on one connection so a failed BEGIN/COMMIT script can
	// explicitly roll back before the pool reuses it. schema.sql wraps the complete
	// install in a transaction, including migrations that require transactional locks.
	ctx := context.Background()
	conn, err := db.Connx(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, string(q)); err != nil {
		if _, rollbackErr := conn.ExecContext(ctx, "ROLLBACK"); rollbackErr != nil {
			return fmt.Errorf("schema install failed (%v), and rollback failed: %w", err, rollbackErr)
		}
		return err
	}
	return nil
}

// isInstallationComplete distinguishes a fully installed database from a schema whose
// setup was interrupted before its migration marker was recorded.
func isInstallationComplete(db *sqlx.DB) (bool, error) {
	var complete bool
	err := db.Get(&complete, `SELECT EXISTS (
		SELECT 1 FROM settings WHERE key = 'migrations'
		AND CASE WHEN jsonb_typeof(value) = 'array' THEN jsonb_array_length(value) > 0 ELSE FALSE END
	)`)
	if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "42P01" {
		return false, nil
	}
	if err != nil || complete {
		return complete, err
	}

	// Databases created before migration tracking have no `migrations` setting. Also,
	// preserve a populated legacy install if that setting was removed manually. The
	// fresh schema contains default settings rows but no rows in the other tables, so
	// those defaults do not make an interrupted schema-only install look complete.
	type tableInPath struct {
		Schema string `db:"schemaname"`
		Name   string `db:"tablename"`
	}
	var tables []tableInPath
	if err := db.Select(&tables, `SELECT schemaname, tablename FROM pg_tables
		WHERE schemaname = ANY(current_schemas(false))
		AND tablename IN ('subscribers', 'lists', 'campaigns', 'templates', 'users', 'media')
		ORDER BY array_position(current_schemas(false), schemaname), tablename`); err != nil {
		return false, err
	}
	for _, table := range tables {
		var populated bool
		query := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s.%s LIMIT 1)",
			pq.QuoteIdentifier(table.Schema), pq.QuoteIdentifier(table.Name))
		if err := db.Get(&populated, query); err != nil {
			return false, err
		}
		if populated {
			return true, nil
		}
	}
	// The schema's fresh-install settings have one shared insertion timestamp and
	// the stock English language. A changed setting (or media-only install) is
	// user data even when every campaign/list table has been emptied.
	var customizedSettings bool
	err = db.Get(&customizedSettings, `SELECT
		EXISTS (SELECT 1 FROM settings WHERE key = 'app.lang' AND value <> '"en"'::jsonb)
		OR (SELECT COUNT(DISTINCT updated_at) FROM settings) > 1`)
	if err != nil {
		return false, err
	}
	if customizedSettings {
		return true, nil
	}
	return false, nil
}

func installLists(q *models.Queries) (int, int) {
	var (
		defList   int
		optinList int
	)
	if err := q.CreateList.Get(&defList,
		uuid.Must(uuid.NewV4()),
		"Default list",
		models.ListTypePrivate,
		models.ListOptinSingle,
		models.ListStatusActive,
		pq.StringArray{"test"},
		"", nil, core.DefaultColumnPreset,
	); err != nil {
		lo.Fatalf("error creating list: %v", err)
	}

	if err := q.CreateList.Get(&optinList, uuid.Must(uuid.NewV4()),
		"Opt-in list",
		models.ListTypePublic,
		models.ListOptinDouble,
		models.ListStatusActive,
		pq.StringArray{"test"},
		"", nil, core.DefaultColumnPreset,
	); err != nil {
		lo.Fatalf("error creating list: %v", err)
	}

	return defList, optinList
}

func installSubs(defListID, optinListID int, q *models.Queries) {
	// Sample subscriber.
	if _, err := q.UpsertSubscriber.Exec(
		uuid.Must(uuid.NewV4()),
		"john@example.com",
		"John Doe",
		`{"type": "known", "good": true, "city": "Bengaluru"}`,
		pq.Int64Array{int64(defListID)},
		models.SubscriptionStatusUnconfirmed,
		true, true); err != nil {
		lo.Fatalf("Error creating subscriber: %v", err)
	}
	if _, err := q.UpsertSubscriber.Exec(
		uuid.Must(uuid.NewV4()),
		"anon@example.com",
		"Anon Doe",
		`{"type": "unknown", "good": true, "city": "Bengaluru"}`,
		pq.Int64Array{int64(optinListID)},
		models.SubscriptionStatusUnconfirmed,
		true, true); err != nil {
		lo.Fatalf("error creating subscriber: %v", err)
	}
}

func installTemplates(q *models.Queries, fs stuffbin.FileSystem) (int, int) {
	// Default campaign template.
	campTpl, err := fs.Get("/static/email-templates/default.tpl")
	if err != nil {
		lo.Fatalf("error reading default e-mail template: %v", err)
	}

	var campTplID int
	if err := q.CreateTemplate.Get(&campTplID, "Default campaign template", models.TemplateTypeCampaign, "", campTpl.ReadBytes(), nil); err != nil {
		lo.Fatalf("error creating default campaign template: %v", err)
	}
	if _, err := q.SetDefaultTemplate.Exec(campTplID); err != nil {
		lo.Fatalf("error setting default template: %v", err)
	}

	// Default campaign archive template.
	archiveTpl, err := fs.Get("/static/email-templates/default-archive.tpl")
	if err != nil {
		lo.Fatalf("error reading default archive template: %v", err)
	}

	var archiveTplID int
	if err := q.CreateTemplate.Get(&archiveTplID, "Default archive template", models.TemplateTypeCampaign, "", archiveTpl.ReadBytes(), nil); err != nil {
		lo.Fatalf("error creating default campaign template: %v", err)
	}

	// Sample tx template.
	txTpl, err := fs.Get("/static/email-templates/sample-tx.tpl")
	if err != nil {
		lo.Fatalf("error reading default e-mail template: %v", err)
	}

	if _, err := q.CreateTemplate.Exec("Sample transactional template", models.TemplateTypeTx, "Welcome {{ .Subscriber.Name }}", txTpl.ReadBytes(), nil); err != nil {
		lo.Fatalf("error creating sample transactional template: %v", err)
	}

	// Sample visual campaign template.
	visualTpl, err := fs.Get("/static/email-templates/default-visual.tpl")
	if err != nil {
		lo.Fatalf("error reading default visual template: %v", err)
	}
	visualSrc, err := fs.Get("/static/email-templates/default-visual.json")
	if err != nil {
		lo.Fatalf("error reading default visual template json: %v", err)
	}

	if _, err := q.CreateTemplate.Exec("Sample visual template", models.TemplateTypeCampaignVisual, "", visualTpl.ReadBytes(), visualSrc.ReadBytes()); err != nil {
		lo.Fatalf("error creating default campaign template: %v", err)
	}

	return campTplID, archiveTplID
}

func installCampaign(campTplID, archiveTplID int, q *models.Queries) {
	// Sample campaign.
	if _, err := q.CreateCampaign.Exec(uuid.Must(uuid.NewV4()),
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
		pq.Int64Array{1},
		false,
		"welcome-to-listmonk",
		archiveTplID,
		`{"name": "Subscriber"}`,
		nil,
		nil,
		"", // Reply-To is empty for the sample campaign.
	); err != nil {
		lo.Fatalf("error creating sample campaign: %v", err)
	}

}

// recordMigrationVersion inserts the given version (of DB migration) into the
// `migrations` array in the settings table.
func recordMigrationVersion(ver string, db *sqlx.DB) error {
	_, err := db.Exec(fmt.Sprintf(`INSERT INTO settings (key, value)
	VALUES('migrations', '["%s"]'::JSONB)
	ON CONFLICT (key) DO UPDATE SET value = settings.value || EXCLUDED.value`, ver))
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

func installUser(username, password, apiUsername string, q *models.Queries) {
	consts := initConstConfig(ko)

	// Super Admin role gets all permissions.
	perms := []string{}
	for p := range consts.Permissions {
		perms = append(perms, p)
	}

	// Create the Super Admin role in the DB.
	var role auth.Role
	if err := q.CreateRole.Get(&role, "Super Admin", auth.RoleTypeUser, pq.Array(perms)); err != nil {
		lo.Fatalf("error creating super admin role: %v", err)
	}

	// Create the admin user.
	if _, err := q.CreateUser.Exec(username, true, password, username+"@listmonk", username, auth.RoleTypeUser, role.ID, nil, auth.UserStatusEnabled); err != nil {
		lo.Fatalf("error creating superadmin user: %v", err)
	}

	// Create the admin API user.
	if apiUsername != "" {
		// Generate a random API token.
		tk, err := utils.GenerateRandomString(32)
		if err != nil {
			lo.Fatalf("error generating API token: %v", err)
		}

		var (
			email    = null.String{String: apiUsername + "@api", Valid: true}
			password = null.String{String: auth.HashAPIToken(tk), Valid: true}
		)

		if _, err := q.CreateUser.Exec(apiUsername, false, password, email, apiUsername, auth.UserTypeAPI, role.ID, nil, auth.UserStatusEnabled); err != nil {
			lo.Fatalf("error creating superadmin API user: %v", err)
		}

		// Print the token to stdout so that it can be grepped out.
		lo.Println("writing API token LISTMONK_ADMIN_API_TOKEN to stderr")
		fmt.Fprintf(os.Stderr, "export LISTMONK_ADMIN_API_TOKEN=\"%s\"\n", tk)
	}
}
