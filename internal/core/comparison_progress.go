// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

// comparisonProgress reports completion of known phases, not estimated runtime.
// Overall updates use fixed normalized units as the selected path changes phases.
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
	nextWindow    int
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
		"main_discovery":        "Analyzing the main input",
		"manual_matching":       "Checking explicit comparison moments",
		"alignment_calibration": "Discovering comparison offsets",
		"alignment_validation":  "Checking comparison offsets",
		"alignment_rows":        "Matching bounded comparison regions",
		"sampling":              "Scanning comparison videos",
		"matching":              "Matching common scenes",
		"refinement":            "Refining comparison frames",
		"matching_refined":      "Matching refined frames",
		"capture":               "Capturing comparison images",
	}[phase]
	p.logger.Infof("comparisons: phase=%s state=running items=%d", phase, items)
	p.update(0, 1)
}

func (p *comparisonProgress) update(completed, total int) {
	total = max(1, total)
	completed = (p.stage*1000 + 1000*min(total, max(0, completed))/total) / max(1, p.stages)
	p.completed = max(p.completed, min(1000, completed))
	api.EmitWorkflowProgress(p.ctx, api.WorkflowProgressUpdate{
		Phase:     "comparisons_" + p.phase,
		Status:    api.StageStatusRunning,
		Completed: p.completed,
		Total:     1000,
		Message:   p.message,
	})
}

func (p *comparisonProgress) complete() {
	p.update(1, 1)
	p.stage++
	p.finishPhase()
}

func (p *comparisonProgress) finishPhase() {
	p.logger.Infof("comparisons: phase=%s state=completed elapsed_seconds=%.3f", p.phase, time.Since(p.started).Seconds())
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

func (p *comparisonProgress) item(id, kind, label string, status api.StageStatus, completed int) {
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
}

func (p *comparisonProgress) window(column, row int) func(api.StageStatus, int) {
	id := "window-" + strconv.Itoa(p.nextWindow)
	p.nextWindow++
	label := fmt.Sprintf("Column %d", column)
	if row > 0 {
		label += fmt.Sprintf(", row %d", row)
	}
	return func(status api.StageStatus, completed int) {
		p.item(id, "comparison_window", label, status, completed)
	}
}

func (p *comparisonProgress) decodeWindow(ctx context.Context, column, row int) (context.Context, func(error)) {
	report := p.window(column, row)
	report(api.StageStatusRunning, 0)
	workCtx := p.child(ctx, func(completed int) {
		report(api.StageStatusRunning, completed)
	})
	return workCtx, func(err error) {
		if err != nil {
			report(api.StageStatusSkipped, 0)
		} else {
			report(api.StageStatusCompleted, 1000)
		}
	}
}

func (p *comparisonProgress) child(ctx context.Context, report func(int)) context.Context {
	logged := 0
	return api.WithWorkflowProgressReporter(ctx, func(update api.WorkflowProgressUpdate) {
		completed := max(0, min(999, 1000*update.Completed/max(1, update.Total)))
		report(completed)
		if completed >= logged+100 {
			logged = completed
			p.logger.Debugf(
				"comparisons: phase=%s decoder_progress=%d elapsed_seconds=%.3f",
				p.phase,
				completed/10,
				time.Since(p.started).Seconds(),
			)
		}
	})
}
