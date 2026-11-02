// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

type timedHistoryRepository struct {
	api.HistoryRepository
	transactionTime time.Duration
}

func (r *timedHistoryRepository) WithHistoryDeletion(ctx context.Context, cleanup func(context.Context) error) error {
	start := time.Now()
	err := r.HistoryRepository.WithHistoryDeletion(ctx, cleanup)
	r.transactionTime += time.Since(start)
	if err != nil {
		return fmt.Errorf("benchmark history deletion: %w", err)
	}
	return nil
}

// BenchmarkHistoryDeleteRetainedWorkflows times the real filesystem/database
// cleanup boundary. transaction_ns/op includes connection acquisition and commit;
// setup and restoring the deleted target are outside the measured interval.
func BenchmarkHistoryDeleteRetainedWorkflows(b *testing.B) {
	for _, retained := range []int{20, 200} {
		b.Run(fmt.Sprintf("workflows=%d/payload=1MiB", retained), func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "history.sqlite")
			repo, err := db.Open(path)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = repo.Close() })
			if err := repo.MigrateContext(b.Context()); err != nil {
				b.Fatal(err)
			}
			now := time.Now().UTC()
			opaque := strings.Repeat("x", 1<<20)
			seed := func(index int, contents string) string {
				source := filepath.Join(filepath.Dir(path), fmt.Sprintf("Example.Release.%04d.mkv", index))
				payload, err := json.Marshal(map[string]any{
					"SourcePath": source,
					"Releases": map[string]any{"release": map[string]any{
						"id":       "release",
						"revision": 1,
						"release":  map[string]any{"source": map[string]string{"sourcePath": source}},
						"opaque":   contents,
					}},
				})
				if err != nil {
					b.Fatal(err)
				}
				if err := repo.Save(b.Context(), db.FileMetadata{
					Path:      source,
					Title:     "Example Release",
					UpdatedAt: now,
				}); err != nil {
					b.Fatal(err)
				}
				if _, _, err := repo.CreateReleaseWorkflowState(b.Context(), api.ReleaseWorkflowStateRecord{
					OwnerID:    "benchmark",
					WorkflowID: api.WorkflowID(fmt.Sprintf("workflow-%d", index)),
					Revision:   1,
					Status:     api.WorkflowStatusCompleted,
					Payload:    payload,
					CreatedAt:  now,
					UpdatedAt:  now,
				}); err != nil {
					b.Fatal(err)
				}
				return source
			}
			for index := range retained {
				seed(index, opaque)
			}
			timed := &timedHistoryRepository{HistoryRepository: repo}
			history := newHistoryModule(timed, path, api.NopLogger{})
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				b.StopTimer()
				target := seed(retained, "target")
				b.StartTimer()
				if err := history.Delete(b.Context(), target); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(timed.transactionTime.Nanoseconds())/float64(b.N), "transaction_ns/op")
		})
	}
}
