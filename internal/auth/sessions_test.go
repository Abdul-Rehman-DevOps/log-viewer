package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionsEpochStableAcrossManagers(t *testing.T) {
	t.Setenv("LOG_VIEWER_SESSION_SECRET", "unit-test-secret")
	t.Setenv("LOG_VIEWER_SESSION_EPOCH", "")
	t.Setenv("LOG_VIEWER_ADMIN_PASSWORD", "")

	dir1 := t.TempDir()
	dir2 := t.TempDir()
	a := NewSessions(dir1)
	b := NewSessions(dir2)
	if a.currentGen() == "" || a.currentGen() != b.currentGen() {
		t.Fatalf("replicas must share epoch: %q vs %q", a.currentGen(), b.currentGen())
	}

	tok, err := a.Create("alice", "user", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, st := b.Lookup(tok); st != StatusOK {
		t.Fatalf("token from replica A must validate on B, got %v", st)
	}
}

func TestSessionsEpochFromEnv(t *testing.T) {
	t.Setenv("LOG_VIEWER_SESSION_SECRET", "unit-test-secret")
	t.Setenv("LOG_VIEWER_SESSION_EPOCH", "shared-epoch-1")

	a := NewSessions(t.TempDir())
	b := NewSessions(t.TempDir())
	if a.currentGen() != "shared-epoch-1" || b.currentGen() != "shared-epoch-1" {
		t.Fatalf("env epoch not applied: %q %q", a.currentGen(), b.currentGen())
	}
}

func TestSessionsEpochFromExistingFile(t *testing.T) {
	t.Setenv("LOG_VIEWER_SESSION_SECRET", "unit-test-secret")
	t.Setenv("LOG_VIEWER_SESSION_EPOCH", "")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session.epoch"), []byte("file-epoch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewSessions(dir)
	if s.currentGen() != "file-epoch" {
		t.Fatalf("want file-epoch, got %q", s.currentGen())
	}
}
