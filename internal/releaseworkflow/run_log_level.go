// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/pkg/api"
)

type runLogLevelContextKey struct{}

func descriptionRunLogLevel(instructions *api.DescriptionInstructions) string {
	if instructions == nil {
		return ""
	}
	return instructions.Options.RunLogLevel
}

// withRunLogLevel borrows the application sink without changing its thresholds.
// An omitted override preserves the caller's context, including quiet views used
// internally when rebuilding projections.
func (m *Module) withRunLogLevel(ctx context.Context, override string) (context.Context, error) {
	if strings.TrimSpace(override) == "" {
		return ctx, nil
	}
	level, err := api.ParseLogLevel(override)
	if err != nil {
		return nil, fmt.Errorf("release workflow run log level: %w", err)
	}
	root, ok := m.logger.(*logging.Logger)
	if !ok {
		return ctx, nil
	}
	logger, err := logging.NewOperationLogger(root, level)
	if err != nil {
		return nil, fmt.Errorf("release workflow run log level: %w", err)
	}
	ctx = context.WithValue(ctx, runLogLevelContextKey{}, level)
	return logging.WithOperationLogger(ctx, logger), nil
}

func (m *Module) withCommandRunLogLevel(ctx context.Context, ownerID string, state State, command mutation) (context.Context, error) {
	if ctx.Value(runLogLevelContextKey{}) != nil {
		return ctx, nil
	}
	switch typed := command.(type) {
	case CompositeUploadCommand, applyCompositeUploadFeedbackCommand:
		if state.Composite != nil {
			return m.withRunLogLevel(ctx, descriptionRunLogLevel(state.Composite.Intent.Descriptions))
		}
	case GenerateDescriptionsCommand:
		return m.withRunLogLevel(ctx, typed.Instructions.Options.RunLogLevel)
	case DryRunUploadsCommand, ExecuteUploadsCommand, RetryFailedUploadsCommand, RetryClientInjectionsCommand:
		if state.Workflow.Descriptions != nil {
			value, err := m.private.Get(ownerID, state.Workflow.ID, descriptionPrivateResourceID(state.Workflow.Descriptions.ID), m.clock.Now().UTC())
			if err == nil {
				if instructions, ok := value.(api.DescriptionInstructions); ok {
					return m.withRunLogLevel(ctx, instructions.Options.RunLogLevel)
				}
			}
		}
	}
	return ctx, nil
}
