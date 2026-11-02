// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRuntimeMaintenanceRetriesFailedStartupAndOutlivesCore(t *testing.T) {
	repo, err := db.Open(filepath.Join(t.TempDir(), "maintenance.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	coordinator, err := releaseworkflow.NewCoordinator()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Shutdown(t.Context()) })
	maintenance := &RuntimeMaintenance{}
	t.Cleanup(maintenance.Close)
	deps := api.CoreDependencies{
		Config: config.Config{
			MainSettings:       config.MainSettingsConfig{TMDBAPI: "synthetic", DBPath: repo.DBPath()},
			ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		},
		Repository:          repo.RepositoryCapabilities(),
		RepositoryOwner:     repo,
		SkipCookieMigration: true,
	}
	root := releaseworkflow.PrivateArtifactVaultRoot(repo.DBPath())
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(root, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if failed, err := NewWithContextAndCoordinator(t.Context(), deps, coordinator, maintenance); err == nil {
		_ = failed.Close()
		t.Fatal("startup accepted unreadable private artifact metadata")
	}
	if err := os.Remove(corrupt); err != nil {
		t.Fatal(err)
	}
	first, err := NewWithContextAndCoordinator(t.Context(), deps, coordinator, maintenance)
	if err != nil {
		t.Fatalf("retry maintenance after repaired evidence: %v", err)
	}
	first.ActivateMaintenance()
	done := maintenance.done
	if done == nil {
		t.Fatal("successful startup did not start expiry cleanup")
	}
	second, err := NewWithContextAndCoordinator(t.Context(), deps, coordinator, maintenance)
	if err != nil {
		t.Fatal(err)
	}
	if maintenance.done != done {
		t.Fatal("runtime replacement started a second expiry worker")
	}
	currentResource := &maintenanceReleaseProbe{released: make(chan struct{})}
	candidateResource := &maintenanceReleaseProbe{released: make(chan struct{})}
	expired := time.Now().UTC().Add(-time.Minute)
	if err := first.history.privateVault.Put("owner", "workflow", "current", currentResource, expired); err != nil {
		t.Fatal(err)
	}
	if err := second.history.privateVault.Put("owner", "workflow", "candidate", candidateResource, expired); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.cleanupExpired(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-currentResource.released:
	default:
		t.Fatal("expiry cleanup did not release installed runtime's cached resource")
	}
	select {
	case <-candidateResource.released:
		t.Fatal("uninstalled candidate replaced installed runtime's expiry ownership")
	default:
	}
	second.ActivateMaintenance()
	if err := maintenance.cleanupExpired(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-candidateResource.released:
	default:
		t.Fatal("installed replacement did not receive expiry cleanup")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatal("retiring config generations stopped process expiry cleanup")
	default:
	}
	maintenance.Close()
	<-done
}

type maintenanceReleaseProbe struct{ released chan struct{} }

func (p *maintenanceReleaseProbe) Release() error {
	close(p.released)
	return nil
}
