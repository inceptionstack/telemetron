// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/inceptionstack/telemetron/internal/config"
)

func TestDeclaredForExporterIncludesInstallIDWhenPresent(t *testing.T) {
	t.Parallel()

	prevReadInstallID := readInstallID
	prevInstallIDPath := setupInstallIDPath
	t.Cleanup(func() {
		readInstallID = prevReadInstallID
		setupInstallIDPath = prevInstallIDPath
	})
	setupInstallIDPath = "/tmp/test-install-id"
	readInstallID = func(path string) (string, error) {
		if path != setupInstallIDPath {
			t.Fatalf("unexpected path: %q", path)
		}
		return "550e8400-e29b-41d4-a716-446655440000", nil
	}

	declared := declaredForExporter(config.Config{
		Declared: config.DeclaredConfig{
			DeploymentID: "dep",
			Tier:         "production",
			Environment:  "prod",
			PackVersion:  "0.3.0",
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if declared["install_id"] != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("expected install_id in declared attrs, got %#v", declared)
	}
}

func TestDeclaredForExporterSkipsMissingInstallID(t *testing.T) {
	t.Parallel()

	prevReadInstallID := readInstallID
	prevInstallIDPath := setupInstallIDPath
	t.Cleanup(func() {
		readInstallID = prevReadInstallID
		setupInstallIDPath = prevInstallIDPath
	})
	setupInstallIDPath = "/tmp/test-install-id"
	readInstallID = func(path string) (string, error) {
		return "", os.ErrNotExist
	}

	declared := declaredForExporter(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, ok := declared["install_id"]; ok {
		t.Fatalf("did not expect install_id, got %#v", declared)
	}
}
