// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/torrentclient"
	"github.com/autobrr/upbrr/pkg/api"
)

type dryRunInjectionReporter struct {
	repository *db.SQLiteRepository
	attempt    int
	record     api.ReleaseWorkflowEffectRecord
	deduped    bool
}

func (r *dryRunInjectionReporter) Begin(ctx context.Context, effect api.WorkflowExternalEffect) (api.WorkflowExternalEffectReceipt, error) {
	r.attempt++
	now := time.Now().UTC()
	operationID := api.WorkflowOperationID(fmt.Sprintf("operation-%d", r.attempt))
	if _, _, err := r.repository.CreateReleaseWorkflowOperation(ctx, api.ReleaseWorkflowOperationRecord{
		OwnerID:            "owner",
		WorkflowID:         "workflow",
		OperationID:        operationID,
		ExpectedRevision:   1,
		CommandFingerprint: "dry-run",
		ProcessEpoch:       "process",
		Status: api.WorkflowOperationStatus{
			ID:          operationID,
			WorkflowID:  "workflow",
			Revision:    1,
			Sequence:    1,
			Command:     "dry_run_uploads",
			Operation:   api.OperationKindDryRun,
			Status:      api.StageStatusCompleted,
			StartedAt:   now,
			UpdatedAt:   now,
			CompletedAt: &now,
		},
	}); err != nil {
		return api.WorkflowExternalEffectReceipt{}, fmt.Errorf("create injection operation: %w", err)
	}
	record, deduped, err := r.repository.BeginReleaseWorkflowEffect(ctx, api.ReleaseWorkflowEffectRecord{
		OwnerID:             "owner",
		WorkflowID:          "workflow",
		OperationID:         operationID,
		EffectID:            fmt.Sprintf("effect-%d", r.attempt),
		Kind:                string(effect.Kind),
		ScopeID:             effect.ScopeID,
		SemanticFingerprint: effect.SemanticFingerprint,
		StartedAt:           now,
		UpdatedAt:           now,
	})
	if err != nil {
		return api.WorkflowExternalEffectReceipt{}, fmt.Errorf("begin injection effect: %w", err)
	}
	r.record, r.deduped = record, deduped
	return api.WorkflowExternalEffectReceipt{EffectID: record.EffectID, AlreadySucceeded: deduped}, nil
}

func (r *dryRunInjectionReporter) Complete(ctx context.Context, _ api.WorkflowExternalEffectReceipt, succeeded bool) error {
	now := time.Now().UTC()
	r.record.UpdatedAt, r.record.CompletedAt = now, &now
	status := api.WorkflowEffectStatusFailed
	if succeeded {
		status = api.WorkflowEffectStatusSucceeded
	}
	if err := r.repository.CompleteReleaseWorkflowEffect(ctx, status, r.record); err != nil {
		return fmt.Errorf("complete injection effect: %w", err)
	}
	return nil
}

func TestDryRunClientInjectionReceiptBindsResolvedDestination(t *testing.T) {
	t.Parallel()
	for _, selection := range []string{"explicit", "tracker", "default", "injection list", "same client new destination", "mixed enabled and disabled"} {
		t.Run(selection, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for _, name := range []string{"first", "second"} {
				if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			torrentPath := filepath.Join(root, "Example.Release.2026.torrent")
			payload := []byte("same prepared torrent")
			if err := os.WriteFile(torrentPath, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			reporter := newInjectionOutcomeReporter(t)
			ctx := api.WithWorkflowExternalEffectReporter(t.Context(), reporter)
			cfg := config.Config{TorrentClients: map[string]config.TorrentClientConfig{
				"first":  {Type: "watch", WatchFolder: filepath.Join(root, "first")},
				"second": {Type: "watch", WatchFolder: filepath.Join(root, "second")},
			}}
			if selection == "mixed enabled and disabled" {
				cfg.ClientSetup.InjectClients = []string{"first", "inactive-none", "inactive-disabled"}
				cfg.TorrentClients["inactive-none"] = config.TorrentClientConfig{Type: "none"}
				cfg.TorrentClients["inactive-disabled"] = config.TorrentClientConfig{Type: "disabled"}
			}
			subject := api.ClientSubject{}
			// Receipts before destination binding used these exact three fields.
			sum := sha256.Sum256(payload)
			legacyFingerprint, err := api.CanonicalWorkflowFingerprint(struct {
				Tracker   api.TrackerID
				Torrent   string
				CrossSeed bool
			}{
				Tracker:   "ALPHA",
				Torrent:   hex.EncodeToString(sum[:]),
				CrossSeed: false,
			})
			if err != nil {
				t.Fatal(err)
			}
			beginLegacy := func() api.WorkflowExternalEffectReceipt {
				t.Helper()
				receipt, err := api.BeginWorkflowExternalEffect(ctx, api.WorkflowExternalEffect{
					Kind:                api.WorkflowExternalEffectClientInjection,
					ScopeID:             "dry-run:ALPHA",
					SemanticFingerprint: legacyFingerprint,
				})
				if err != nil {
					t.Fatal(err)
				}
				return receipt
			}
			selectDestination := func(name string) {
				cfg.ClientSetup.DefaultClient = "first"
				switch selection {
				case "explicit":
					subject.ClientOverrides.Client = &name
				case "tracker":
					cfg.Trackers.Trackers = map[string]config.TrackerConfig{"ALPHA": {TorrentClient: name}}
				case "default":
					cfg.ClientSetup.DefaultClient = name
				case "injection list":
					cfg.ClientSetup.InjectClients = []string{name}
				case "same client new destination", "mixed enabled and disabled":
					cfg.TorrentClients["first"] = config.TorrentClientConfig{Type: "watch", WatchFolder: filepath.Join(root, name)}
				}
			}
			inject := func(wantStatus api.StageStatus) {
				t.Helper()
				status, _, code, err := injectWorkflowDryRunClient(ctx, torrentclient.NewService(cfg, nil), subject,
					api.TrackerReleaseProjection{TrackerID: "ALPHA"}, torrentPath, 0, 1)
				if err != nil || status != wantStatus || (wantStatus == api.StageStatusFailed && code != api.OperationFailureUnknownOutcome) {
					t.Fatalf("injection status=%s code=%s error=%v", status, code, err)
				}
			}
			selectDestination("first")
			firstCopy := filepath.Join(root, "first", filepath.Base(torrentPath))
			if selection == "explicit" {
				receipt := beginLegacy()
				if err := torrentclient.NewService(cfg, nil).Inject(ctx, subject, api.TorrentResult{Path: torrentPath, Tracker: "ALPHA"}); err != nil {
					t.Fatal(err)
				}
				if err := api.CompleteWorkflowExternalEffect(ctx, receipt, true); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(firstCopy); err != nil {
					t.Fatal(err)
				}
			}
			inject(api.StageStatusCompleted)
			if reporter.deduped {
				t.Fatal("first destination-bound injection reused a legacy receipt")
			}
			if err := os.Remove(firstCopy); err != nil {
				t.Fatal(err)
			}
			inject(api.StageStatusCompleted)
			if !reporter.deduped {
				t.Fatal("unchanged destination did not reuse the succeeded receipt")
			}
			if _, err := os.Stat(firstCopy); !os.IsNotExist(err) {
				t.Fatalf("unchanged destination repeated the write: %v", err)
			}
			if selection == "mixed enabled and disabled" {
				for _, name := range []string{"inactive-none", "inactive-disabled"} {
					client := cfg.TorrentClients[name]
					client.Category = name
					cfg.TorrentClients[name] = client
					inject(api.StageStatusCompleted)
					if !reporter.deduped {
						t.Fatalf("editing disabled client %s did not reuse the succeeded receipt", name)
					}
					if _, err := os.Stat(firstCopy); !os.IsNotExist(err) {
						t.Fatalf("editing disabled client %s repeated the enabled write: %v", name, err)
					}
				}
			}
			selectDestination("second")
			inject(api.StageStatusCompleted)
			if reporter.deduped {
				t.Fatal("changed destination reused the prior injection receipt")
			}
			secondCopy := filepath.Join(root, "second", filepath.Base(torrentPath))
			if _, err := os.Stat(secondCopy); err != nil {
				t.Fatalf("new destination did not receive the torrent: %v", err)
			}
			// An unresolved legacy attempt blocks new fingerprints without a client write.
			_ = beginLegacy()
			if err := os.Remove(secondCopy); err != nil {
				t.Fatal(err)
			}
			selectDestination("second")
			inject(api.StageStatusFailed)
			if _, err := os.Stat(secondCopy); !os.IsNotExist(err) {
				t.Fatalf("unresolved legacy receipt allowed a client write: %v", err)
			}
		})
	}
}
