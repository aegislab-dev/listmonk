package tmptokens

import (
	"testing"
	"time"
)

// A wrong value must not use up or delete the stored token: anyone could
// otherwise invalidate another person's password reset link.
func TestMatch(t *testing.T) {
	Set("match@example.invalid", time.Minute, "right-token")

	for i := 0; i < 100; i++ {
		if Match("match@example.invalid", "wrong-token", true) {
			t.Fatal("wrong value matched")
		}
	}
	if Match("match@example.invalid", "", true) || Match("nobody@example.invalid", "right-token", true) {
		t.Fatal("empty value or unknown ID matched")
	}

	// Checking without consuming can be repeated.
	for i := 0; i < 100; i++ {
		if !Match("match@example.invalid", "right-token", false) {
			t.Fatalf("right value did not match on try %d", i)
		}
	}

	// Consuming works exactly once.
	if !Match("match@example.invalid", "right-token", true) {
		t.Fatal("right value did not match")
	}
	if Match("match@example.invalid", "right-token", true) || Match("match@example.invalid", "right-token", false) {
		t.Fatal("consumed token matched again")
	}
}

func TestMatchExpiredAndNonString(t *testing.T) {
	Set("expired", time.Nanosecond, "v")
	time.Sleep(time.Millisecond)
	if Match("expired", "v", false) {
		t.Fatal("expired token matched")
	}

	// Tokens that hold something other than a string (2FA login tokens hold a
	// user ID) never match.
	Set("int", time.Minute, 42)
	if Match("int", "42", false) || Match("int", "", false) {
		t.Fatal("non-string token matched")
	}
}

func TestSetNX(t *testing.T) {
	if got := SetNX("nx", time.Minute, "first"); got != "first" {
		t.Fatalf("SetNX on a new ID = %v", got)
	}
	// A live token is kept, and returned, with its original expiry.
	mu.Lock()
	created := tokens["nx"].CreatedAt
	mu.Unlock()
	if got := SetNX("nx", time.Hour, "second"); got != "first" {
		t.Fatalf("SetNX on a live ID = %v, want the stored value", got)
	}
	mu.Lock()
	tk := tokens["nx"]
	mu.Unlock()
	if !tk.CreatedAt.Equal(created) || tk.TTL != time.Minute {
		t.Fatalf("SetNX changed the live token: %+v", tk)
	}

	// An expired one is replaced.
	Set("nx-expired", time.Nanosecond, "old")
	time.Sleep(time.Millisecond)
	if got := SetNX("nx-expired", time.Minute, "new"); got != "new" {
		t.Fatalf("SetNX on an expired ID = %v", got)
	}
	if !Match("nx-expired", "new", false) {
		t.Fatal("replacement was not stored")
	}
}

// A used-up budget stays used up until it expires. Before, the token was
// deleted on the last try, so SetNX (or a new login) started a fresh count.
func TestCheckBudgetIsNotReset(t *testing.T) {
	SetNX("budget", 50*time.Millisecond, nil)
	for i := 0; i < maxTries; i++ {
		if _, err := Check("budget"); err != nil {
			t.Fatalf("check %d failed: %v", i+1, err)
		}
	}
	for i := 0; i < 5; i++ {
		SetNX("budget", 50*time.Millisecond, nil)
		if _, err := Check("budget"); err == nil {
			t.Fatalf("check %d passed, want an error", maxTries+1+i)
		}
	}

	// After it expires, the budget starts again.
	time.Sleep(60 * time.Millisecond)
	SetNX("budget", time.Minute, nil)
	if _, err := Check("budget"); err != nil {
		t.Fatalf("check after expiry failed: %v", err)
	}
}

func TestGetConsumes(t *testing.T) {
	Set("once", time.Minute, 7)
	if v, err := Get("once"); err != nil || v != 7 {
		t.Fatalf("Get = %v, %v", v, err)
	}
	if _, err := Get("once"); err == nil {
		t.Fatal("Get returned a consumed token")
	}
}
