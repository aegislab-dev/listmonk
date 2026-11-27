package main

import "testing"

func TestInstallValidatesCredentialsBeforeAnySetupSideEffects(t *testing.T) {
	called := false
	err := validateInstallBeforeSetup("x", "long-enough", "", func(bool) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("invalid credentials: setup called=%t, error=%v", called, err)
	}

	err = validateInstallBeforeSetup("admin", "long-enough", "", func(hasUser bool) error {
		called = true
		if !hasUser {
			t.Fatal("valid admin was not passed to setup")
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("valid credentials: setup called=%t, error=%v", called, err)
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
