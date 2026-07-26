// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

const privateResourceKindOperationCommand = "releaseworkflow/operation-command/v1"

type durableOperationCommand struct {
	command Command
}

type persistedOperationCommand struct {
	Name    string          `json:"name"`
	Payload json.RawMessage `json:"payload"`
}

func (c durableOperationCommand) MarshalPrivateResource() (string, []byte, error) {
	if c.command == nil {
		return "", nil, errors.New("release workflow operation command is required")
	}
	if _, longRunning := CommandOperationKind(c.command); !longRunning {
		return "", nil, fmt.Errorf("release workflow command %s is not resumable work", c.command.commandName())
	}
	payload, err := json.Marshal(c.command)
	if err != nil {
		return "", nil, fmt.Errorf("marshal release workflow operation command %s: %w", c.command.commandName(), err)
	}
	envelope, err := json.Marshal(persistedOperationCommand{
		Name:    c.command.commandName(),
		Payload: payload,
	})
	if err != nil {
		return "", nil, fmt.Errorf("marshal release workflow operation command envelope: %w", err)
	}
	return privateResourceKindOperationCommand, envelope, nil
}

func decodeDurableOperationCommand(payload []byte) (any, error) {
	var envelope persistedOperationCommand
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("decode release workflow operation command envelope: %w", err)
	}
	var command Command
	switch strings.TrimSpace(envelope.Name) {
	case (PrepareReleaseCommand{}).commandName():
		command = &PrepareReleaseCommand{}
	case (ResetReleaseCommand{}).commandName():
		command = &ResetReleaseCommand{}
	case (SelectBlurayCandidateCommand{}).commandName():
		command = &SelectBlurayCandidateCommand{}
	case (ProjectTrackersCommand{}).commandName():
		command = &ProjectTrackersCommand{}
	case (PreflightTrackersCommand{}).commandName():
		command = &PreflightTrackersCommand{}
	case (CheckDuplicatesCommand{}).commandName():
		command = &CheckDuplicatesCommand{}
	case (CaptureMediaCommand{}).commandName():
		command = &CaptureMediaCommand{}
	case (UploadMediaImagesCommand{}).commandName():
		command = &UploadMediaImagesCommand{}
	case (GenerateDescriptionsCommand{}).commandName():
		command = &GenerateDescriptionsCommand{}
	case (DryRunUploadsCommand{}).commandName():
		command = &DryRunUploadsCommand{}
	case (ExecuteUploadsCommand{}).commandName():
		command = &ExecuteUploadsCommand{}
	case (RetryFailedUploadsCommand{}).commandName():
		command = &RetryFailedUploadsCommand{}
	case (RetryClientInjectionsCommand{}).commandName():
		command = &RetryClientInjectionsCommand{}
	case (CompositeUploadCommand{}).commandName():
		command = &CompositeUploadCommand{}
	default:
		return nil, fmt.Errorf("unsupported release workflow operation command %q", envelope.Name)
	}
	if len(envelope.Payload) == 0 {
		return nil, errors.New("release workflow operation command payload is required")
	}
	if err := json.Unmarshal(envelope.Payload, command); err != nil {
		return nil, fmt.Errorf("decode release workflow operation command %s: %w", envelope.Name, err)
	}
	command = operationCommandValue(command)
	if _, longRunning := CommandOperationKind(command); !longRunning {
		return nil, fmt.Errorf("release workflow operation command %s is not resumable work", envelope.Name)
	}
	return durableOperationCommand{command: command}, nil
}

func operationCommandValue(command Command) Command {
	switch typed := command.(type) {
	case *PrepareReleaseCommand:
		return *typed
	case *ResetReleaseCommand:
		return *typed
	case *SelectBlurayCandidateCommand:
		return *typed
	case *ProjectTrackersCommand:
		return *typed
	case *PreflightTrackersCommand:
		return *typed
	case *CheckDuplicatesCommand:
		return *typed
	case *CaptureMediaCommand:
		return *typed
	case *UploadMediaImagesCommand:
		return *typed
	case *GenerateDescriptionsCommand:
		return *typed
	case *DryRunUploadsCommand:
		return *typed
	case *ExecuteUploadsCommand:
		return *typed
	case *RetryFailedUploadsCommand:
		return *typed
	case *RetryClientInjectionsCommand:
		return *typed
	case *CompositeUploadCommand:
		return *typed
	default:
		return command
	}
}

func operationCommandResourceID(operationID api.WorkflowOperationID) string {
	return "operation-command:" + string(operationID)
}
