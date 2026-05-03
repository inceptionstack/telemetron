// SPDX-License-Identifier: Apache-2.0

package installid

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadOrGenerateCreatesInstallID(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "etc", "telemetron", "install-id")
	got, err := ReadOrGenerate(path)
	if err != nil {
		t.Fatal(err)
	}
	if !Validate(got) {
		t.Fatalf("generated invalid install id: %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("expected 0644 perms, got %o", info.Mode().Perm())
	}
}

func TestReadOrGenerateReusesExistingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "install-id")
	want := "550e8400-e29b-41d4-a716-446655440000"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadOrGenerate(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestReadRejectsMalformedFileWithoutOverwrite(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "install-id")
	if err := os.WriteFile(path, []byte("not-a-uuid"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Read(path); err == nil {
		t.Fatal("expected read to fail")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "not-a-uuid" {
		t.Fatalf("file was unexpectedly overwritten: %q", string(data))
	}
}

func TestReadTrimsWhitespace(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "install-id")
	want := "550e8400-e29b-41d4-a716-446655440000"
	if err := os.WriteFile(path, []byte(" \n"+want+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
