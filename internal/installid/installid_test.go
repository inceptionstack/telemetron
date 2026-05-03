// SPDX-License-Identifier: Apache-2.0

package installid

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestValidateAcceptsUUIDv4AndRejectsOtherVersions(t *testing.T) {
	t.Parallel()

	if !Validate("550e8400-e29b-41d4-a716-446655440000") {
		t.Fatal("expected UUIDv4 to validate")
	}
	for _, invalid := range []string{
		"550e8400-e29b-11d4-a716-446655440000",
		"550e8400-e29b-31d4-a716-446655440000",
		"550e8400-e29b-51d4-a716-446655440000",
	} {
		if Validate(invalid) {
			t.Fatalf("expected %q to be rejected", invalid)
		}
	}
}

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

func TestReadReturnsExistingUUIDv4(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "install-id")
	want := "550e8400-e29b-41d4-a716-446655440000"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
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

func TestReadOrGenerateIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "install-id")
	results := make([]string, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = ReadOrGenerate(path)
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if results[0] != results[1] {
		t.Fatalf("expected identical install ids, got %q and %q", results[0], results[1])
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != results[0] {
		t.Fatalf("expected persisted install id %q, got %q", results[0], string(data))
	}
}

func TestReadOrGenerateReturnsErrorForInvalidExistingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "install-id")
	if err := os.WriteFile(path, []byte("not-a-uuid"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadOrGenerate(path); err == nil {
		t.Fatal("expected invalid existing install-id to fail")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "not-a-uuid" {
		t.Fatalf("file was unexpectedly overwritten: %q", string(data))
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
