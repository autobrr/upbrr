// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	qbittorrent "github.com/autobrr/go-qbittorrent"

	"github.com/autobrr/upbrr/internal/clientdiscovery"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/torrent"
	"github.com/autobrr/upbrr/internal/torrentclient"
	"github.com/autobrr/upbrr/pkg/api"
)

// TestWorkflowRetainsDiscoveredCandidateAcrossBasenameCollision proves that
// another source's discovery cannot force retained upload evidence to rehash.
func TestWorkflowRetainsDiscoveredCandidateAcrossBasenameCollision(t *testing.T) {
	t.Parallel()

	const name = "Example.Collision.2026.mkv"
	const sourceSize = 5 * 1024 * 1024
	fixtures := make([]struct {
		source string
		hash   string
		data   []byte
	}, 2)
	for index := range fixtures {
		source := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(source, bytes.Repeat([]byte{byte('a' + index)}, sourceSize), 0o600); err != nil {
			t.Fatal(err)
		}
		// Separate roots keep fixture metainfo outside the runtime's reusable paths.
		created, err := torrent.NewService(api.NopLogger{}, t.TempDir()).Create(t.Context(), api.TorrentSubject{SourcePath: source})
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(created.Path)
		if err != nil {
			t.Fatal(err)
		}
		fixtures[index].source = source
		fixtures[index].hash = created.InfoHash
		fixtures[index].data = data
	}
	if fixtures[0].hash == fixtures[1].hash {
		t.Fatal("different source bytes must produce different infohashes")
	}
	// Neither export matches these bytes. Only A's qualifying client evidence
	// can allow reuse; any fallback to full piece verification must fail.
	if err := os.WriteFile(fixtures[0].source, bytes.Repeat([]byte("c"), sourceSize), 0o600); err != nil {
		t.Fatal(err)
	}

	var active atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture := fixtures[active.Load()]
		switch r.URL.Path {
		case "/api/v2/torrents/search":
			_ = json.NewEncoder(w).Encode([]qbittorrent.Torrent{{
				Hash:        fixture.hash,
				Name:        name,
				ContentPath: fixture.source,
				Progress:    1,
				Size:        sourceSize,
				TotalSize:   sourceSize,
				State:       qbittorrent.TorrentStateStalledUp,
			}})
		case "/api/v2/torrents/properties":
			_ = json.NewEncoder(w).Encode(qbittorrent.TorrentProperties{})
		case "/api/v2/torrents/export":
			if r.FormValue("hash") != fixture.hash {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(fixture.data)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	cfg := config.Config{
		MainSettings:   config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "db.sqlite")},
		ClientSetup:    config.ClientSetupConfig{DefaultClient: "qbit", SearchClients: config.CSVList{"qbit"}},
		TorrentClients: map[string]config.TorrentClientConfig{"qbit": {Type: "qui", QuiProxyURL: server.URL}},
	}
	discovery := clientdiscovery.New(torrentclient.NewService(cfg, api.NopLogger{}), api.NopLogger{})
	discover := func(index int32) clientdiscovery.Evidence {
		t.Helper()
		active.Store(index)
		fixture := fixtures[index]
		evidence, err := discovery.Discover(t.Context(), clientdiscovery.SearchInput{
			SourcePath: fixture.source,
			FileList:   []string{fixture.source},
		})
		if err != nil {
			t.Fatal(err)
		}
		if evidence.TorrentPath == "" || evidence.InfoHash != fixture.hash || !evidence.TorrentDataVerified {
			t.Fatalf("source %d did not return qualifying client evidence: %#v", index, evidence)
		}
		return evidence
	}
	evidenceA := discover(0)
	subject := api.UploadSubject{
		SourcePath:                fixtures[0].source,
		InfoHash:                  evidenceA.InfoHash,
		ClientTorrentPath:         evidenceA.TorrentPath,
		ClientTorrentDataVerified: evidenceA.TorrentDataVerified,
	}
	torrents := torrent.NewService(api.NopLogger{}, t.TempDir())
	for _, phase := range []string{"before second discovery", "after second discovery"} {
		if phase == "after second discovery" {
			discover(1)
		}
		t.Run(phase, func(t *testing.T) {
			for _, dryRun := range []bool{false, true} {
				t.Run(map[bool]string{false: "upload", true: "dry-run"}[dryRun], func(t *testing.T) {
					retained := &workflowRetainedUploadServiceFake{torrentPaths: map[api.TrackerID]string{"ALPHA": evidenceA.TorrentPath}}
					builder := workflowUploadPlanBuilder{
						resolver: workflowUploadResolverFixed{subject: subject},
						trackers: retained,
						torrents: torrents,
					}
					reuseOnly := true
					plan, execution, err := builder.Build(t.Context(),
						api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
							TrackerID:   "ALPHA",
							Readiness:   api.ReadinessStatusReady,
							UploadReady: true,
						}}},
						api.DupeAssessment{Results: []api.TrackerDupeAssessment{{
							TrackerID: "ALPHA",
							Decision:  api.DupeDecisionNoMatch,
							Status:    api.StageStatusCompleted,
						}}},
						workflowDupePrivateEvidence{}, api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, api.DescriptionSet{},
						api.DescriptionInstructions{Torrent: api.TorrentOverrides{NoHash: &reuseOnly}},
						releaseworkflow.UploadPlanBuildOptions{DryRun: dryRun, NoSeed: true}, time.Now())
					if execution != nil {
						if releaseErr := execution.Release(); releaseErr != nil {
							t.Fatal(releaseErr)
						}
					}
					if err != nil {
						t.Fatalf("retained source A evidence must skip piece verification: %v", err)
					}
					if plan.Status != api.StageStatusReady || retained.subject.TorrentPath != evidenceA.TorrentPath {
						t.Fatalf("upload plan did not retain source A's candidate: status=%s path=%q", plan.Status, retained.subject.TorrentPath)
					}
				})
			}
		})
	}
}
