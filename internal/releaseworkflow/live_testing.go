// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"fmt"

	"github.com/autobrr/upbrr/pkg/api"
)

// WithLiveTestPolicy installs process-owned safety independently of contexts.
func WithLiveTestPolicy(policy *api.LiveTestPolicy) Option {
	return func(module *Module) error {
		module.liveTest = policy
		return nil
	}
}

func (m *Module) rejectLiveTestCommand(command mutation) error {
	if m.liveTest == nil {
		return nil
	}
	operation := api.OperationKindUnknown
	switch typed := command.(type) {
	case ExecuteUploadsCommand, RetryFailedUploadsCommand:
		operation = api.OperationKindUploadExecute
	case RetryClientInjectionsCommand:
		operation = api.OperationKindClientInjection
	case CompositeUploadCommand:
		if typed.Goal == api.WorkflowGoalUploaded {
			operation = api.OperationKindUploadExecute
		}
	}
	if operation == api.OperationKindUnknown {
		return nil
	}
	m.logger.Warnf("workflow: operation=%s state=blocked reason=live_test", operation)
	return fmt.Errorf("live-test workflow command: %w", m.liveTest.RejectRequest(operation))
}
