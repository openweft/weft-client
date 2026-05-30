package weftclient

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestTokenCachePath_XDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	got := TokenCachePath()
	want := filepath.Join("/xdg", "weft", "token.hcl")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTokenCachePath_HomeFallback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/tmp/home")
	got := TokenCachePath()
	if got != filepath.Join("/tmp/home", ".config", "weft", "token.hcl") {
		t.Fatalf("unexpected path: %s", got)
	}
}

func TestTokenCachePath_NoHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USER", "") // best effort to make UserHomeDir return ""
	got := TokenCachePath()
	// We accept either the empty-path branch or the resolved one
	// (depends on whether the runner has /etc/passwd fallback).
	if got != "" && !strings.HasSuffix(got, ".config/weft/token.hcl") {
		t.Fatalf("unexpected path: %q", got)
	}
}

func TestLoadCachedToken_Missing(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	got, err := LoadCachedToken()
	if err != nil {
		t.Fatalf("missing token should not error, got: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil token, got: %+v", got)
	}
}

func TestLoadCachedToken_StatError(t *testing.T) {
	// Create a directory at the token-cache path's location, then
	// make it unreadable. Easier: set the cache file to a path that
	// is itself a directory — Stat succeeds, but later decode will
	// fail. To trigger the stat-error branch we instead create the
	// parent as a regular file so traversing into it errors.
	tmp := t.TempDir()
	// Make $XDG_CONFIG_HOME/weft a *file*, so $XDG_CONFIG_HOME/weft/token.hcl
	// triggers a stat ENOTDIR.
	t.Setenv("XDG_CONFIG_HOME", tmp)
	if err := os.WriteFile(filepath.Join(tmp, "weft"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCachedToken()
	if err == nil {
		t.Fatal("expected stat error")
	}
	if !strings.Contains(err.Error(), "token cache: stat") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCachedToken_NoPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USER", "")
	// If TokenCachePath actually resolves "" we'll exercise the
	// error branch; otherwise the test still asserts a sane result.
	if TokenCachePath() != "" {
		t.Skip("UserHomeDir resolved a path despite empty HOME; skipping no-path branch")
	}
	_, err := LoadCachedToken()
	if err == nil {
		t.Fatal("expected error when path is empty")
	}
}

func TestSaveLoadDeleteCachedToken_Roundtrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	tok := &CachedToken{
		Issuer:       "https://example.com",
		ClientID:     "weft",
		AccessToken:  "access",
		RefreshToken: "refresh",
		IDToken:      "id",
		ExpiresAt:    "2026-05-23T12:34:56Z",
	}
	if err := SaveCachedToken(tok); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadCachedToken()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil || got.AccessToken != tok.AccessToken || got.RefreshToken != tok.RefreshToken || got.IDToken != tok.IDToken {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	// File mode should be 0600.
	info, err := os.Stat(filepath.Join(tmp, "weft", "token.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	// Delete then confirm gone.
	if err := DeleteCachedToken(); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err = LoadCachedToken()
	if err != nil || got != nil {
		t.Fatalf("after delete: %+v err=%v", got, err)
	}
	// Second delete is a no-op.
	if err := DeleteCachedToken(); err != nil {
		t.Fatalf("delete (second) returned %v", err)
	}
}

func TestSaveCachedToken_Minimal(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	tok := &CachedToken{
		Issuer:      "i",
		ClientID:    "c",
		AccessToken: "a",
		ExpiresAt:   "2026-01-01T00:00:00Z",
	}
	if err := SaveCachedToken(tok); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCachedToken()
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "" || got.IDToken != "" {
		t.Fatalf("optional fields should be empty: %+v", got)
	}
}

func TestSaveCachedToken_NilRefuse(t *testing.T) {
	err := SaveCachedToken(nil)
	if err == nil || !strings.Contains(err.Error(), "refuse to save nil") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestSaveCachedToken_NoPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USER", "")
	if TokenCachePath() != "" {
		t.Skip("UserHomeDir still resolves; skipping no-path branch")
	}
	err := SaveCachedToken(&CachedToken{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSaveCachedToken_MkdirFailure(t *testing.T) {
	tmp := t.TempDir()
	// Place a regular file where the weft directory would go so
	// MkdirAll fails.
	t.Setenv("XDG_CONFIG_HOME", tmp)
	if err := os.WriteFile(filepath.Join(tmp, "weft"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := SaveCachedToken(&CachedToken{
		Issuer:      "i",
		ClientID:    "c",
		AccessToken: "a",
		ExpiresAt:   "2026-01-01T00:00:00Z",
	})
	if err == nil {
		t.Fatal("expected mkdir failure")
	}
	if !strings.Contains(err.Error(), "mkdir") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestSaveCachedToken_WriteFailure(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	// Make the destination directory exist but read-only so the
	// .tmp write fails.
	dir := filepath.Join(tmp, "weft")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Place a directory at the .tmp path so WriteFile fails (cannot
	// write a regular file to a directory path).
	if err := os.MkdirAll(filepath.Join(dir, "token.hcl.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := SaveCachedToken(&CachedToken{
		Issuer:      "i",
		ClientID:    "c",
		AccessToken: "a",
		ExpiresAt:   "2026-01-01T00:00:00Z",
	})
	if err == nil {
		t.Fatal("expected write failure")
	}
	if !strings.Contains(err.Error(), "write tmp") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestSaveCachedToken_RenameFailure(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	dir := filepath.Join(tmp, "weft")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Place a directory at the final destination so the rename
	// fails (cannot rename a regular file over a non-empty
	// directory).
	if err := os.MkdirAll(filepath.Join(dir, "token.hcl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token.hcl", "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := SaveCachedToken(&CachedToken{
		Issuer:      "i",
		ClientID:    "c",
		AccessToken: "a",
		ExpiresAt:   "2026-01-01T00:00:00Z",
	})
	if err == nil {
		t.Fatal("expected rename failure")
	}
	if !strings.Contains(err.Error(), "commit") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestLoadCachedToken_DecodeError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	dir := filepath.Join(tmp, "weft")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Garbage HCL.
	if err := os.WriteFile(filepath.Join(dir, "token.hcl"), []byte("not valid = ==="), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCachedToken()
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got: %v", err)
	}
}

func TestDeleteCachedToken_NoPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USER", "")
	if TokenCachePath() != "" {
		t.Skip("UserHomeDir still resolves; skipping no-path branch")
	}
	if err := DeleteCachedToken(); err != nil {
		t.Fatalf("expected nil error when path is empty, got %v", err)
	}
}

func TestDeleteCachedToken_RemoveError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	// Place a non-empty directory at the cache path so os.Remove
	// fails (directory not empty).
	dir := filepath.Join(tmp, "weft", "token.hcl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := DeleteCachedToken()
	if err == nil {
		t.Fatal("expected remove error")
	}
	if !strings.Contains(err.Error(), "remove") {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify not-exist is treated as success — covered by the
	// roundtrip second-delete already, but also probe directly via
	// errors.Is(err, os.ErrNotExist) on a manually constructed
	// scenario.
	if errors.Is(err, os.ErrNotExist) {
		t.Fatal("should not be not-exist")
	}
}

func TestBearer_NilSafety(t *testing.T) {
	var t0 *CachedToken
	if t0.Bearer() != "" {
		t.Fatal("nil bearer should be empty")
	}
	t1 := &CachedToken{AccessToken: "abc"}
	if t1.Bearer() != "abc" {
		t.Fatalf("got %q", t1.Bearer())
	}
}

func TestExpiresAtTime(t *testing.T) {
	var t0 *CachedToken
	if !t0.ExpiresAtTime().IsZero() {
		t.Fatal("nil should be zero")
	}
	t1 := &CachedToken{}
	if !t1.ExpiresAtTime().IsZero() {
		t.Fatal("empty should be zero")
	}
	t2 := &CachedToken{ExpiresAt: "not-a-time"}
	if !t2.ExpiresAtTime().IsZero() {
		t.Fatal("malformed should be zero")
	}
	t3 := &CachedToken{ExpiresAt: "2026-05-23T12:34:56Z"}
	got := t3.ExpiresAtTime()
	want, _ := time.Parse(time.RFC3339, "2026-05-23T12:34:56Z")
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestFromOAuth2(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 34, 56, 0, time.UTC)
	tok := &oauth2.Token{
		AccessToken:  "a",
		RefreshToken: "r",
		Expiry:       now,
	}
	out := FromOAuth2(tok, "iss", "cid", "id")
	if out.Issuer != "iss" || out.ClientID != "cid" || out.AccessToken != "a" ||
		out.RefreshToken != "r" || out.IDToken != "id" {
		t.Fatalf("got %+v", out)
	}
	if got, _ := time.Parse(time.RFC3339, out.ExpiresAt); !got.Equal(now) {
		t.Fatalf("expires-at = %s", out.ExpiresAt)
	}
}
