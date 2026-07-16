// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package jobs

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/pkg/api"
)

// UploadRunner executes one prepared upload request for the engine.
type UploadRunner interface {
	// RunUpload executes one exact reviewed input and reports progress through the context callback.
	RunUpload(context.Context, api.UploadExecutionPlan) (api.Result, error)
}

// DupeRunner executes one duplicate-check request for the engine.
type DupeRunner interface {
	// CheckDupes executes one exact duplicate-check input and reports progress through the context callback.
	CheckDupes(context.Context, api.DuplicateCheckInput) (api.DupeCheckSummary, error)
}

// Closer releases one per-job resource after its worker exits.
type Closer interface {
	// Close releases the resource and reports cleanup failure.
	Close() error
}

// Resources names the per-job resources whose ownership transfers to the engine after a successful start.
type Resources struct {
	// Core is closed before Logger when job execution ends.
	Core Closer
	// Logger is closed after Core when job execution ends.
	Logger Closer
}

// DuplicateExecutionSnapshot binds accepted duplicate work to one exact
// preparation and runtime generation. It contains no live runtime pointers.
type DuplicateExecutionSnapshot struct {
	Seed               preparedrelease.Seed
	PreparedGeneration api.PreparedGeneration
	RuntimeGeneration  uint64
	Input              api.DuplicateCheckInput
}

// UploadExecutionSnapshot binds reviewed upload work and required outcomes to
// one exact preparation/runtime generation.
type UploadExecutionSnapshot struct {
	Seed               preparedrelease.Seed
	PreparedGeneration api.PreparedGeneration
	RuntimeGeneration  uint64
	Input              api.UploadReviewInput
	Outcome            api.UploadReviewOutcome
	ReviewToken        string
}

// UploadSpec contains immutable input and execution dependencies for a tracker-upload job.
// The engine validates and clones mutable fields before accepting the job.
type UploadSpec struct {
	// CorrelationID is the caller-generated opaque identity for start reconciliation.
	CorrelationID string
	// RetryOf links this Job to the original failed upload Job, when applicable.
	RetryOf string
	// Snapshot binds this accepted upload to immutable preparation/runtime state.
	Snapshot UploadExecutionSnapshot
	// Runner executes one operation-wide request for all selected trackers.
	Runner UploadRunner
	// Resources are closed exactly once after worker exit.
	Resources Resources
}

// UploadRetry is a cloned, resource-free description of failed tracker work eligible for a fresh job.
type UploadRetry struct {
	// RetryOf identifies the original failed upload Job.
	RetryOf string
	// Snapshot preserves the exact reviewed preparation/runtime lineage. A
	// retry is rejected by its runner if this snapshot is no longer compatible.
	Snapshot UploadExecutionSnapshot
}

// Spec builds a fresh upload spec from retained retry input and newly-created
// per-run resources. Runners/resources from the original job are never reused.
func (r UploadRetry) Spec(correlationID string, runner UploadRunner, resources Resources) UploadSpec {
	return UploadSpec{
		CorrelationID: correlationID,
		RetryOf:       r.RetryOf,
		Snapshot:      cloneUploadExecutionSnapshot(r.Snapshot),
		Runner:        runner,
		Resources:     resources,
	}
}

// DupeSpec contains immutable input and execution dependencies for a duplicate-check job.
// The engine validates and clones mutable fields before accepting the job.
type DupeSpec struct {
	// CorrelationID is the caller-generated opaque identity for start reconciliation.
	CorrelationID string
	// Snapshot binds this accepted duplicate check to immutable preparation/runtime state.
	Snapshot DuplicateExecutionSnapshot
	// Runner executes the duplicate check.
	Runner DupeRunner
	// Resources are closed exactly once after worker exit.
	Resources Resources
}

// normalizeUploadSpec validates upload input and deep-copies every mutable caller-owned field.
func normalizeUploadSpec(spec UploadSpec) (UploadSpec, error) {
	correlationID, err := normalizeCorrelationID(spec.CorrelationID)
	if err != nil {
		return UploadSpec{}, err
	}
	spec.CorrelationID = correlationID
	spec.RetryOf = strings.TrimSpace(spec.RetryOf)
	sourcePath := strings.TrimSpace(spec.Snapshot.Input.Release.SourcePath)
	if sourcePath == "" {
		return UploadSpec{}, errors.New("path is required")
	}
	if spec.Runner == nil {
		return UploadSpec{}, errors.New("upload runner is required")
	}
	if err := validateExecutionLineage(
		sourcePath,
		spec.Snapshot.PreparedGeneration,
		spec.Snapshot.RuntimeGeneration,
		spec.Snapshot.Input.Release,
	); err != nil {
		return UploadSpec{}, fmt.Errorf("upload snapshot: %w", err)
	}
	if strings.TrimSpace(spec.Snapshot.ReviewToken) == "" {
		return UploadSpec{}, errors.New("upload snapshot: review token is required")
	}
	spec.Snapshot = cloneUploadExecutionSnapshot(spec.Snapshot)
	spec.Snapshot.Input.Release.SourcePath = sourcePath
	spec.Snapshot.Input.Trackers = normalizeTrackers(spec.Snapshot.Input.Trackers, false)
	if len(spec.Snapshot.Input.Trackers) == 0 {
		return UploadSpec{}, errors.New("at least one tracker must be selected")
	}
	spec.Snapshot.Input.IgnoreDupesFor = normalizeTrackers(spec.Snapshot.Input.IgnoreDupesFor, false)
	spec.Snapshot.Input.IgnoreRuleFailuresFor = normalizeTrackers(spec.Snapshot.Input.IgnoreRuleFailuresFor, false)
	return spec, nil
}

// normalizeDupeSpec validates duplicate-check input and deep-copies mutable caller-owned fields.
func normalizeDupeSpec(spec DupeSpec) (DupeSpec, error) {
	correlationID, err := normalizeCorrelationID(spec.CorrelationID)
	if err != nil {
		return DupeSpec{}, err
	}
	spec.CorrelationID = correlationID
	sourcePath := strings.TrimSpace(spec.Snapshot.Input.Release.SourcePath)
	if sourcePath == "" {
		return DupeSpec{}, errors.New("path is required")
	}
	if spec.Runner == nil {
		return DupeSpec{}, errors.New("dupe runner is required")
	}
	if err := validateExecutionLineage(
		sourcePath,
		spec.Snapshot.PreparedGeneration,
		spec.Snapshot.RuntimeGeneration,
		spec.Snapshot.Input.Release,
	); err != nil {
		return DupeSpec{}, fmt.Errorf("dupe snapshot: %w", err)
	}
	spec.Snapshot = cloneDuplicateExecutionSnapshot(spec.Snapshot)
	spec.Snapshot.Input.Release.SourcePath = sourcePath
	spec.Snapshot.Input.Trackers = normalizeTrackers(spec.Snapshot.Input.Trackers, true)
	if len(spec.Snapshot.Input.Trackers) == 0 {
		return DupeSpec{}, errors.New("at least one tracker must be selected")
	}
	spec.Snapshot.Input.IgnoreFor = normalizeTrackers(spec.Snapshot.Input.IgnoreFor, true)
	return spec, nil
}

func validateExecutionLineage(
	sourcePath string,
	preparedGeneration api.PreparedGeneration,
	runtimeGeneration uint64,
	release api.ReleaseRef,
) error {
	if preparedGeneration == 0 || runtimeGeneration == 0 || release.Generation == 0 {
		return errors.New("prepared and runtime generations are required")
	}
	if preparedGeneration != release.Generation {
		return errors.New("prepared generation differs from operation release")
	}
	if !strings.EqualFold(filepath.Clean(strings.TrimSpace(sourcePath)), filepath.Clean(strings.TrimSpace(release.SourcePath))) {
		return errors.New("operation release source differs from job source")
	}
	return nil
}

func cloneDuplicateExecutionSnapshot(value DuplicateExecutionSnapshot) DuplicateExecutionSnapshot {
	value.Input.Trackers = append([]string(nil), value.Input.Trackers...)
	value.Input.IgnoreFor = append([]string(nil), value.Input.IgnoreFor...)
	value.Input.Authorizations = append([]string(nil), value.Input.Authorizations...)
	return value
}

func cloneUploadExecutionSnapshot(value UploadExecutionSnapshot) UploadExecutionSnapshot {
	value.Input.Trackers = append([]string(nil), value.Input.Trackers...)
	value.Input.IgnoreDupesFor = append([]string(nil), value.Input.IgnoreDupesFor...)
	value.Input.IgnoreRuleFailuresFor = append([]string(nil), value.Input.IgnoreRuleFailuresFor...)
	value.Input.QuestionnaireAnswers = cloneAnswers(value.Input.QuestionnaireAnswers)
	value.Input.DescriptionGroups = api.CloneDescriptionBuilderGroups(value.Input.DescriptionGroups)
	value.Input.TrackerConfigOverrides = clonePointerStruct(value.Input.TrackerConfigOverrides)
	value.Input.TrackerSiteOverrides.TIK = clonePointerStruct(value.Input.TrackerSiteOverrides.TIK)
	value.Input.ClientOverrides = clonePointerStruct(value.Input.ClientOverrides)
	value.Input.ImageHostOverrides = clonePointerStruct(value.Input.ImageHostOverrides)
	value.Input.ScreenshotOverrides = cloneScreenshotOverrides(value.Input.ScreenshotOverrides)
	value.Input.TorrentOverrides = clonePointerStruct(value.Input.TorrentOverrides)
	value.Outcome.ResolvedTrackers = append([]string(nil), value.Outcome.ResolvedTrackers...)
	value.Outcome.Eligibility = cloneTrackerEligibility(value.Outcome.Eligibility)
	value.Outcome.MatchedTrackers = append([]string(nil), value.Outcome.MatchedTrackers...)
	value.Outcome.BlockedTrackers = cloneBlockedTrackers(value.Outcome.BlockedTrackers)
	value.Outcome.TrackerRuleFailures = cloneRuleFailures(value.Outcome.TrackerRuleFailures)
	value.Outcome.CrossSeedTorrents = append([]api.UploadedTorrent(nil), value.Outcome.CrossSeedTorrents...)
	return value
}

func cloneTrackerEligibility(value api.TrackerEligibility) api.TrackerEligibility {
	value.EligibleTrackers = append([]string(nil), value.EligibleTrackers...)
	value.Trackers = append([]api.TrackerEligibilityState(nil), value.Trackers...)
	for index := range value.Trackers {
		value.Trackers[index].Reasons = append([]api.TrackerEligibilityReason(nil), value.Trackers[index].Reasons...)
	}
	return value
}

func cloneBlockedTrackers(input map[string][]api.TrackerBlockReason) map[string][]api.TrackerBlockReason {
	if input == nil {
		return nil
	}
	result := make(map[string][]api.TrackerBlockReason, len(input))
	for tracker, reasons := range input {
		result[tracker] = append([]api.TrackerBlockReason(nil), reasons...)
	}
	return result
}

func cloneRuleFailures(input map[string][]api.RuleFailure) map[string][]api.RuleFailure {
	if input == nil {
		return nil
	}
	result := make(map[string][]api.RuleFailure, len(input))
	for tracker, failures := range input {
		result[tracker] = append([]api.RuleFailure(nil), failures...)
	}
	return result
}

func cloneScreenshotOverrides(value api.ScreenshotOverrides) api.ScreenshotOverrides {
	value.ManualFrames = append([]int(nil), value.ManualFrames...)
	value.ComparisonPaths = append([]string(nil), value.ComparisonPaths...)
	value.ComparisonPrimaryIndex = cloneIntPointer(value.ComparisonPrimaryIndex)
	value.MenuPaths = append([]string(nil), value.MenuPaths...)
	return value
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func normalizeCorrelationID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("correlation id is required")
	}
	if len(value) > 128 {
		return "", errors.New("correlation id is too long")
	}
	return value, nil
}

// normalizeTrackers trims, removes blanks, and deduplicates tracker names case-insensitively.
func normalizeTrackers(input []string, upper bool) []string {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, raw := range input {
		tracker := strings.TrimSpace(raw)
		key := strings.ToUpper(tracker)
		if tracker == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if upper {
			tracker = key
		}
		result = append(result, tracker)
	}
	return result
}

// cloneAnswers deep-copies tracker questionnaire maps while preserving explicit empty inner maps.
func cloneAnswers(input map[string]map[string]string) map[string]map[string]string {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]map[string]string, len(input))
	for tracker, answers := range input {
		result[tracker] = maps.Clone(answers)
		if result[tracker] == nil {
			result[tracker] = map[string]string{}
		}
	}
	return result
}

// clonePointerStruct deep-copies scalar pointer fields in API override value
// structs. Those structs intentionally contain no slices, maps, or interfaces.
func clonePointerStruct[T any](input T) T {
	value := reflect.ValueOf(&input).Elem()
	for _, field := range value.Fields() {
		if field.Kind() != reflect.Pointer || field.IsNil() {
			continue
		}
		pointer := reflect.New(field.Elem().Type())
		pointer.Elem().Set(field.Elem())
		field.Set(pointer)
	}
	return input
}
