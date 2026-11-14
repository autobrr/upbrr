// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"
	"time"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

// comparisonProgress reports completion of known phases, not estimated runtime.
// Child decoder progress is source-clock progress within one phase item.
type comparisonProgress struct {
	ctx           context.Context
	logger        api.Logger
	stages, stage int
	completed     int
	phase         string
	message       string
	started       time.Time
	active        bool
}

func (b workflowMediaBuilder) comparisonProgress(ctx context.Context, stages int) *comparisonProgress {
	logger := api.Logger(api.NopLogger{})
	if b.media != nil && b.media.logger != nil {
		logger = b.media.logger
	}
	return &comparisonProgress{
		ctx:    ctx,
		logger: logging.FromContext(ctx, logger),
		stages: stages,
	}
}

func (p *comparisonProgress) begin(phase string, items int) {
	p.phase, p.started, p.active = phase, time.Now(), true
	p.message = map[string]string{
		"sampling":         "Scanning comparison videos",
		"matching":         "Matching common scenes",
		"refinement":       "Refining comparison frames",
		"matching_refined": "Matching refined frames",
		"capture":          "Capturing comparison images",
	}[phase]
	p.logger.Infof("comparisons: phase=%s state=running items=%d", phase, items)
	p.update(0, 1)
}

func (p *comparisonProgress) update(completed, total int) {
	p.completed = max(p.completed, p.stage*1000+1000*completed/max(1, total))
	api.EmitWorkflowProgress(p.ctx, api.WorkflowProgressUpdate{
		Phase:     "comparisons_" + p.phase,
		Status:    api.StageStatusRunning,
		Completed: p.completed,
		Total:     p.stages * 1000,
		Message:   p.message,
	})
}

func (p *comparisonProgress) complete() {
	p.update(1, 1)
	p.logger.Infof("comparisons: phase=%s state=completed elapsed_seconds=%.3f", p.phase, time.Since(p.started).Seconds())
	p.stage++
	p.active = false
}

func (p *comparisonProgress) close() {
	if !p.active {
		return
	}
	state := "failed"
	if p.ctx.Err() != nil {
		state = "canceled"
	}
	p.logger.Warnf("comparisons: phase=%s state=%s elapsed_seconds=%.3f", p.phase, state, time.Since(p.started).Seconds())
}

func (p *comparisonProgress) item(index, total int, id, kind, label string, status api.StageStatus, completed int) {
	if kind == "comparison_source" && (completed == 0 || status == api.StageStatusCompleted) {
		p.logger.Infof("comparisons: phase=%s item=%d/%d state=%s", p.phase, index+1, total, status)
	}
	api.EmitWorkflowProgress(p.ctx, api.WorkflowProgressUpdate{
		Phase:     "comparisons_" + p.phase,
		ItemID:    p.phase + "/" + id,
		Kind:      kind,
		Label:     label,
		Status:    status,
		Completed: completed,
		Total:     1000,
		ItemOnly:  true,
		Message:   fmt.Sprintf("%s — %d%%", status, completed/10),
	})
	p.update(index*1000+completed, max(1, total)*1000)
}

func (p *comparisonProgress) child(ctx context.Context, index, total int, id, kind, label string) context.Context {
	logged := 0
	return api.WithWorkflowProgressReporter(ctx, func(update api.WorkflowProgressUpdate) {
		completed := max(0, min(999, 1000*update.Completed/max(1, update.Total)))
		p.item(index, total, id, kind, label, api.StageStatusRunning, completed)
		if completed >= logged+100 {
			logged = completed
			p.logger.Debugf(
				"comparisons: phase=%s item=%d/%d progress=%d elapsed_seconds=%.3f",
				p.phase,
				index+1,
				total,
				completed/10,
				time.Since(p.started).Seconds(),
			)
		}
	})
}
