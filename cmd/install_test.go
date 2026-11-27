package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/knadh/stuffbin"
	"github.com/lib/pq"
)

func TestInstallInvalidCredentialsDoNotWriteSchema(t *testing.T) {
	if dsn := os.Getenv("AXM_INSTALL_INVALID_CHILD_DSN"); dsn != "" {
		db, err := sqlx.Connect("postgres", dsn)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fs, err := stuffbin.NewLocalFS("/", repoRoot(t)+":/")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		install("6.2.0", db, fs, false, false)
		os.Exit(0)
	}
	dsn := os.Getenv("AXM_TEST_DB_URL")
	if dsn == "" {
		t.Skip("AXM_TEST_DB_URL not set")
	}
	admin, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("axm_invalid_install_%d", time.Now().UnixNano())
	admin.MustExec("CREATE SCHEMA " + pq.QuoteIdentifier(schema))
	t.Cleanup(func() {
		admin.MustExec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		admin.Close()
	})
	childDSN := withSearchPath(t, dsn, schema)
	cmd := exec.Command(os.Args[0], "-test.run=^TestInstallInvalidCredentialsDoNotWriteSchema$")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	env := make([]string, 0, len(os.Environ())+4)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "LISTMONK_ADMIN_USER=") &&
			!strings.HasPrefix(value, "LISTMONK_ADMIN_PASSWORD=") &&
			!strings.HasPrefix(value, "LISTMONK_ADMIN_API_USER=") &&
			!strings.HasPrefix(value, "AXM_INSTALL_INVALID_CHILD_DSN=") {
			env = append(env, value)
		}
	}
	cmd.Env = append(env,
		"AXM_INSTALL_INVALID_CHILD_DSN="+childDSN,
		"LISTMONK_ADMIN_USER=x",
		"LISTMONK_ADMIN_PASSWORD=long-enough-password",
		"LISTMONK_ADMIN_API_USER=",
	)
	err = cmd.Run()
	if err == nil || !strings.Contains(output.String(), "LISTMONK_ADMIN_USER should be min 3 chars") {
		t.Fatalf("CLI did not reject invalid install credentials: err=%v output=%s", err, output.String())
	}
	var tables int
	if err := admin.Get(&tables, `SELECT COUNT(*) FROM pg_tables WHERE schemaname=$1 AND tablename IN ('settings','lists','campaigns','templates')`, schema); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("install wrote %d tables before rejecting invalid credentials", tables)
	}
}

func TestValidateInstallUsersRejectsInvalidCredentialsBeforeInstall(t *testing.T) {
	for _, tc := range []struct {
		name, user, password, apiUser string
		wantUser                     bool
		wantErr                      bool
	}{
		{name: "no optional user", wantUser: false},
		{name: "valid admin", user: "admin", password: "long-enough", wantUser: true},
		{name: "short admin", user: "x", password: "long-enough", wantErr: true},
		{name: "short password", user: "admin", password: "short", wantErr: true},
		{name: "API user without admin", apiUser: "apiuser", wantErr: true},
		{name: "short API user", user: "admin", password: "long-enough", apiUser: "x", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotUser, err := validateInstallUsers(tc.user, tc.password, tc.apiUser)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateInstallUsers() error=%v, wantErr=%t", err, tc.wantErr)
			}
			if err == nil && gotUser != tc.wantUser {
				t.Fatalf("validateInstallUsers() hasUser=%t, want %t", gotUser, tc.wantUser)
			}
		})
	}
}
