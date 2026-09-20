// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package db

import (
	"errors"
	"testing"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBeginReleaseWorkflowEffectRequiresMatchingConfigGenerationWhenStamped(t *testing.T) {
	t.Parallel()
	repo := openMigratedTestRepo(t)
	ctx := t.Context()
	now := time.Now().UTC()
	state := workflowStateRecordForTest("config-effect-workflow", api.WorkflowStatusActive, now, `{}`)
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, state); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE config_activation SET generation = ?, fingerprint = ? WHERE singleton = 1`, 4, "runtime-four"); err != nil {
		t.Fatal(err)
	}
	authority := api.ConfigActivationAuthority{Generation: 4, Fingerprint: "runtime-four"}
	stamped := api.WithConfigActivationAuthority(ctx, authority)
	if _, err := repo.db.ExecContext(ctx, `
		INSERT INTO config_activation_pending (
			singleton, activation_id, initiating_owner, status, base_generation, impacts_json, candidate_json, updated_at
		) VALUES (1, 'pending-five', 'owner', 'pending', 4, '[]', '{}', ?)
	`, formatWorkflowStateTime(now)); err != nil {
		t.Fatal(err)
	}
	effect := workflowEffectForTest(state, "config-effect-operation", "config-effect", "PTP", now)
	if _, _, err := repo.BeginReleaseWorkflowEffect(stamped, effect); err != nil {
		t.Fatalf("begin effect while next generation is pending: %v", err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE config_activation SET generation = ?, fingerprint = ? WHERE singleton = 1`, 5, "runtime-five"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM config_activation_pending WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}
	changed := effect
	changed.EffectID = "config-effect-after-change"
	changed.ScopeID = "BTN"
	changed.UpdatedAt = now.Add(time.Second)
	if _, _, err := repo.BeginReleaseWorkflowEffect(stamped, changed); !errors.Is(err, api.ErrConfigActivationChanged) {
		t.Fatalf("begin effect after generation change = %v", err)
	}
}
