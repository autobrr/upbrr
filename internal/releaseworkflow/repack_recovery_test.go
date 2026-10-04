// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestReplaceFactInstructionsRecoversLegacyRepack(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		patch   api.ReleaseCorrectionPatch
		want    api.ReleaseNameOverrides
		prepare bool
	}{
		{
			name: "auto",
			patch: api.ReleaseCorrectionPatch{
				ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldReleaseNameRepack}},
			},
			want:    api.ReleaseNameOverrides{Edition: new("Uncut")},
			prepare: true,
		},
		{
			name: "replacement",
			patch: api.ReleaseCorrectionPatch{
				Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Repack: new(" proper2 ")}},
			},
			want:    api.ReleaseNameOverrides{Repack: new(" proper2 "), Edition: new("Uncut")},
			prepare: true,
		},
		{
			name: "unrelated edition",
			patch: api.ReleaseCorrectionPatch{
				Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Edition: new("Extended")}},
			},
			want: api.ReleaseNameOverrides{Repack: new("Legacy-Version"), Edition: new("Extended")},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sourcePath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")
			if err := os.WriteFile(sourcePath, []byte("synthetic media"), 0o600); err != nil {
				t.Fatal(err)
			}
			repo, err := db.Open(filepath.Join(t.TempDir(), "legacy-repack.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			legacy := api.ReleaseNameOverrides{Repack: new("Legacy-Version"), Edition: new("Uncut")}
			payload, err := json.Marshal(api.StoredReleaseCorrectionsV1{Version: 1, ReleaseName: legacy})
			if err != nil {
				t.Fatal(err)
			}
			// Seed a retained record from before incoming release-version validation.
			if _, err := repo.RawDB().ExecContext(t.Context(), `
				INSERT INTO release_overrides (source_path, corrections_json, corrections_revision, updated_at)
				VALUES (?, ?, ?, ?)`, sourcePath, string(payload), 7, "2026-09-09T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
			module, _ := newTestModule(t, newCompositePersistencePreparer(t, repo))
			current := executeCommand(t, module, CreateWorkflowCommand{
				SourcePath:   sourcePath,
				Instructions: api.ReleaseFactInstructions{ReleaseName: legacy},
			})
			test.patch.ExpectedRevision = new(uint64(7))
			current = executeCommand(t, module, ReplaceFactInstructionsCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				SourcePath:       sourcePath,
				CorrectionPatch:  &test.patch,
			})
			if test.prepare {
				current = executeCommand(t, module, PrepareReleaseCommand{
					WorkflowID:       current.Workflow.ID,
					ExpectedRevision: current.Workflow.Revision,
					Input:            api.PrepareInput{SourcePath: sourcePath},
				})
				if current.Release == nil || current.Workflow.Status != api.WorkflowStatusActive {
					t.Fatalf("legacy correction recovery did not prepare a release: %#v", current)
				}
			}
			if current.FactInstructions == nil {
				t.Fatal("accepted correction instructions are unavailable")
			}
			stored, err := repo.LoadReleaseCorrections(t.Context(), sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Revision <= 7 || current.FactInstructions.CorrectionRevision != stored.Revision {
				t.Fatalf("accepted correction revision = %d, stored = %d", current.FactInstructions.CorrectionRevision, stored.Revision)
			}
			for _, got := range []api.ReleaseNameOverrides{current.FactInstructions.Instructions.ReleaseName, stored.Corrections.ReleaseName} {
				if !reflect.DeepEqual(got.Repack, test.want.Repack) || !reflect.DeepEqual(got.Edition, test.want.Edition) {
					t.Fatalf("recovered release version/edition = %#v, want %#v", got, test.want)
				}
			}
		})
	}
}
