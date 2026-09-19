// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/cookies"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/livetest"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// ActivationStage identifies the failed stage of a runtime activation.
type ActivationStage string

const (
	ActivationStageNormalize       ActivationStage = "normalize candidate"
	ActivationStageValidateStored  ActivationStage = "validate stored config"
	ActivationStageValidateRuntime ActivationStage = "validate runtime config"
	ActivationStageBuild           ActivationStage = "build runtime generation"
	ActivationStageCookies         ActivationStage = "maintain cookies"
	ActivationStagePersist         ActivationStage = "persist stored config"
)

// ActivationError preserves the failed activation stage and wrapped cause.
type ActivationError struct {
	Stage ActivationStage
	Err   error
}

func (e *ActivationError) Error() string {
	if e == nil {
		return "runtime activation failed"
	}
	return fmt.Sprintf("runtime activation: %s: %v", e.Stage, e.Err)
}

// Unwrap returns the stage cause.
func (e *ActivationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// RetiredRuntime owns the resources replaced by one successful installation.
type RetiredRuntime struct {
	Bundle *runtimeBundle
	Owner  LifecycleOwner
	Logger *logging.Logger
}

// Close releases the replaced runtime after its installer has finished all
// transport-specific rebinding.
func (r RetiredRuntime) Close() {
	if r.Bundle != nil {
		r.Bundle.retire()
		return
	}
	if r.Owner != nil {
		_ = r.Owner.Close()
	}
	if r.Logger != nil {
		_ = r.Logger.Close()
	}
}

// RuntimeInstaller atomically publishes one complete runtime generation and
// returns resources that can be retired after transport rebinding completes.
// Install must not fail.
type RuntimeInstaller interface {
	Install(RuntimeGeneration) RetiredRuntime
}

type runtimeActivationDeps struct {
	build                      func(context.Context, config.Config, *db.SQLiteRepository) (RuntimeGeneration, error)
	cookies                    func(context.Context, *db.SQLiteRepository, string, api.Logger) error
	persist                    func(context.Context, *config.Config, *db.SQLiteRepository, string, api.Logger) error
	loadStored                 func(context.Context, *db.SQLiteRepository) (*config.Config, error)
	activationSafe             func(context.Context, *db.SQLiteRepository) (bool, error)
	loadActivation             func(context.Context, *db.SQLiteRepository) (api.ConfigActivation, error)
	savePending                func(context.Context, *db.SQLiteRepository, string, []byte, []api.ConfigImpactDetail) (api.ConfigActivation, error)
	failPending                func(context.Context, *db.SQLiteRepository, string, api.ConfigActivationFailureCode) (api.ConfigActivation, error)
	clearFailure               func(context.Context, *db.SQLiteRepository, string) (api.ConfigActivation, error)
	transform                  db.ConfigActivationWorkflowTransform
	tryAcquireRuntimeAdmission func() (func(), bool)
	persistActivated           func(context.Context, *config.Config, *db.SQLiteRepository, string, api.Logger, uint64, api.WorkflowFingerprint, []api.ConfigImpactDetail, db.ConfigActivationWorkflowTransform) (api.ConfigActivation, error)
}

type runtimeCookiePersistenceError struct {
	err error
}

// initializeRuntimeConfigActivation binds a runtime built from an existing
// config store to the durable generation before Core admits any effects.
func initializeRuntimeConfigActivation(
	ctx context.Context, repo *db.SQLiteRepository, runtimeCfg config.Config,
) (api.ConfigActivation, error) {
	if repo == nil {
		return api.ConfigActivation{}, errors.New("runtime activation: repository is required")
	}
	fingerprint, err := config.EffectiveConfigFingerprint(runtimeCfg)
	if err != nil {
		return api.ConfigActivation{}, fmt.Errorf("fingerprint runtime config: %w", err)
	}
	stored, err := config.LoadFromDatabase(ctx, repo)
	if err == nil {
		config.ApplyEnvOverrides(stored)
		stored.MainSettings.DBPath = repo.DBPath()
		storedFingerprint, fingerprintErr := config.EffectiveConfigFingerprint(*stored)
		if fingerprintErr != nil {
			return api.ConfigActivation{}, fmt.Errorf("fingerprint durable config: %w", fingerprintErr)
		}
		if storedFingerprint != fingerprint {
			return api.ConfigActivation{}, api.ErrConfigActivationChanged
		}
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		return api.ConfigActivation{}, fmt.Errorf("load durable config: %w", err)
	}
	activation, err := repo.InitializeConfigActivationFingerprint(ctx, fingerprint)
	if err != nil {
		return api.ConfigActivation{}, fmt.Errorf("initialize runtime config activation fingerprint: %w", err)
	}
	return activation, nil
}

func (e *runtimeCookiePersistenceError) Error() string {
	return fmt.Sprintf("runtime cookie persistence: %v", e.err)
}

func (e *runtimeCookiePersistenceError) Unwrap() error {
	return e.err
}

// RuntimeActivator serializes and owns the complete config-candidate to active
// runtime transition for one WebUI host.
type RuntimeActivator struct {
	liveProfile               *livetest.Profile
	liveImageConfig           config.ImageHostingConfig
	liveImageRegistry         *trackers.Registry
	liveImageTrackerInputs    []liveTestImageTrackerInput
	mu                        sync.Mutex
	repo                      *db.SQLiteRepository
	fixedDBPath               string
	installer                 RuntimeInstaller
	currentConfig             func() config.Config
	buildingConfigGeneration  uint64
	buildingConfigFingerprint api.WorkflowFingerprint
	deps                      runtimeActivationDeps
}

type liveTestImageTrackerInput struct {
	Tracker   string
	Username  string
	Passkey   string
	ImgAPI    string
	ImageHost string
	ImgRehost bool
}

// NewRuntimeActivator constructs an activator for one already-open repository
// and one host-specific runtime installer.
func NewRuntimeActivator(repo *db.SQLiteRepository, fixedDBPath string, installer RuntimeInstaller) (*RuntimeActivator, error) {
	if repo == nil {
		return nil, errors.New("runtime activation: repository is required")
	}
	fixedDBPath = strings.TrimSpace(fixedDBPath)
	if fixedDBPath == "" {
		return nil, errors.New("runtime activation: fixed database path is required")
	}
	if installer == nil {
		return nil, errors.New("runtime activation: installer is required")
	}
	return &RuntimeActivator{
		repo:        repo,
		fixedDBPath: fixedDBPath,
		installer:   installer,
		deps: runtimeActivationDeps{
			build:   buildRuntimeGeneration,
			cookies: validateRuntimeCookies,
			persist: persistRuntimeConfigAndCookies,
			failPending: func(ctx context.Context, repo *db.SQLiteRepository, activationID string, code api.ConfigActivationFailureCode) (api.ConfigActivation, error) {
				return repo.FailPendingConfigActivation(ctx, activationID, code)
			},
			clearFailure: func(ctx context.Context, repo *db.SQLiteRepository, activationID string) (api.ConfigActivation, error) {
				return repo.ClearConfigActivationFailure(ctx, activationID)
			},
			loadStored: func(ctx context.Context, repo *db.SQLiteRepository) (*config.Config, error) {
				return config.LoadFromDatabase(ctx, repo)
			},
		},
	}, nil
}

// Activate normalizes, validates, builds, maintains, persists, and publishes a
// config candidate as one serialized runtime transition.
func (a *RuntimeActivator) Activate(ctx context.Context, candidate config.Config) error {
	_, err := a.ActivateResult(ctx, candidate)
	return err
}

// ActivateResult persists a settings candidate immediately when it leaves the
// effective runtime unchanged. Effective changes wait durably while a workflow
// owns the active input, otherwise they build before the atomic commit.
func (a *RuntimeActivator) ActivateResult(ctx context.Context, candidate config.Config) (api.ConfigActivation, error) {
	return a.ActivateResultForOwner(ctx, "system", candidate)
}

// ActivateResultForOwner saves one settings candidate on behalf of its session
// owner. A later candidate cannot replace a durable pending activation.
func (a *RuntimeActivator) ActivateResultForOwner(
	ctx context.Context, ownerID string, candidate config.Config,
) (api.ConfigActivation, error) {
	if a == nil {
		return api.ConfigActivation{}, errors.New("runtime activation: activator is required")
	}
	if ctx == nil {
		return api.ConfigActivation{}, errors.New("runtime activation: context is required")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	return a.activateResultLocked(ctx, ownerID, candidate, false, true)
}

// ActivateImmediate activates a candidate only when it can become effective
// in this call. It never creates a pending candidate, which keeps callers
// without a pending-status protocol from reporting an unapplied config as a
// successful import.
func (a *RuntimeActivator) ActivateImmediate(ctx context.Context, candidate config.Config) error {
	if a == nil {
		return errors.New("runtime activation: activator is required")
	}
	if ctx == nil {
		return errors.New("runtime activation: context is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err := a.activateResultLocked(ctx, "system", candidate, false, false)
	return err
}

// ActivatePending attempts the one durable candidate after a browser poll. It
// does no construction while a workflow remains active, and the commit path
// repeats the safety check in the config transaction.
func (a *RuntimeActivator) ActivatePending(ctx context.Context) (api.ConfigActivation, error) {
	if a == nil {
		return api.ConfigActivation{}, errors.New("runtime activation: activator is required")
	}
	if ctx == nil {
		return api.ConfigActivation{}, errors.New("runtime activation: context is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.deps.loadActivation == nil {
		return api.ConfigActivation{}, errors.New("runtime activation: config activation state is required")
	}
	activation, err := a.deps.loadActivation(ctx, a.repo)
	if err != nil {
		return api.ConfigActivation{}, activationError(ActivationStagePersist, err)
	}
	if activation.Status != api.ConfigActivationPending {
		return activation, nil
	}
	if safe, err := a.activationSafe(ctx); err != nil {
		return api.ConfigActivation{}, activationError(ActivationStagePersist, err)
	} else if !safe {
		return activation, nil
	}
	payload, persisted, err := a.repo.LoadPendingConfigActivationCandidate(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) && persisted.Status != api.ConfigActivationPending {
			return persisted, nil
		}
		return a.failPendingActivation(ctx, activation, activationError(ActivationStagePersist, err))
	}
	if persisted.Status != api.ConfigActivationPending {
		return persisted, nil
	}
	var encrypted config.Config
	if err := json.Unmarshal(payload, &encrypted); err != nil {
		return a.failPendingActivation(ctx, persisted, activationError(ActivationStagePersist, fmt.Errorf("decode pending config: %w", err)))
	}
	candidate, err := config.DecryptConfigSecrets(&encrypted)
	if err != nil {
		return a.failPendingActivation(ctx, persisted, activationError(ActivationStagePersist, fmt.Errorf("decrypt pending config: %w", err)))
	}
	result, err := a.activateResultLocked(ctx, "system", *candidate, true, true)
	if err != nil {
		return a.failPendingActivation(ctx, persisted, err)
	}
	return result, nil
}

func (a *RuntimeActivator) failPendingActivation(
	ctx context.Context, activation api.ConfigActivation, activationErr error,
) (api.ConfigActivation, error) {
	if !terminalPendingActivationFailure(activationErr) {
		return api.ConfigActivation{}, activationErr
	}
	if a.deps.failPending == nil {
		return api.ConfigActivation{}, activationErr
	}
	failed, err := a.deps.failPending(ctx, a.repo, activation.ActivationID, configActivationFailureCode(activationErr))
	if err != nil {
		return api.ConfigActivation{}, fmt.Errorf("record failed deferred config activation: %w", err)
	}
	return failed, nil
}

func terminalPendingActivationFailure(err error) bool {
	return !errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, api.ErrActiveInputBusy) &&
		!errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) &&
		!errors.Is(err, api.ErrConfigActivationPending)
}

func configActivationFailureCode(err error) api.ConfigActivationFailureCode {
	var activationErr *ActivationError
	if !errors.As(err, &activationErr) {
		return api.ConfigActivationFailurePersist
	}
	switch activationErr.Stage {
	case ActivationStageNormalize:
		return api.ConfigActivationFailureNormalize
	case ActivationStageValidateStored:
		return api.ConfigActivationFailureValidateStored
	case ActivationStageValidateRuntime:
		return api.ConfigActivationFailureValidateRuntime
	case ActivationStageBuild:
		return api.ConfigActivationFailureBuild
	case ActivationStageCookies:
		return api.ConfigActivationFailureCookies
	case ActivationStagePersist:
		return api.ConfigActivationFailurePersist
	default:
		return api.ConfigActivationFailurePersist
	}
}

func (a *RuntimeActivator) activateResultLocked(
	ctx context.Context, ownerID string, candidate config.Config, consumingPending, allowPending bool,
) (api.ConfigActivation, error) {
	stored, err := cloneConfig(candidate)
	if err != nil {
		return api.ConfigActivation{}, activationError(ActivationStageNormalize, err)
	}
	if _, err := config.MergeMissingTrackerDefaults(stored); err != nil {
		return api.ConfigActivation{}, activationError(ActivationStageNormalize, err)
	}
	stored.MainSettings.DBPath = a.fixedDBPath
	if a.liveProfile != nil {
		if err := configstore.ValidateLiveTestTrackerConfigNames(stored.Trackers.Trackers); err != nil {
			return api.ConfigActivation{}, activationError(ActivationStageValidateStored, err)
		}
		trackerInputs := liveTestImageTrackerInputs(*stored, a.liveImageRegistry)
		if stored.ImageHosting != a.liveImageConfig || !slices.Equal(trackerInputs, a.liveImageTrackerInputs) {
			return api.ConfigActivation{}, activationError(
				ActivationStageValidateStored,
				errors.New("live-test image-host configuration cannot change during a run; create a new profile"),
			)
		}
		configstore.ApplyLiveTestPaths(stored, *a.liveProfile)
	}
	if err := stored.Validate(); err != nil {
		return api.ConfigActivation{}, activationError(ActivationStageValidateStored, err)
	}

	runtimeCfg, err := cloneConfig(*stored)
	if err != nil {
		return api.ConfigActivation{}, activationError(ActivationStageNormalize, err)
	}
	if a.liveProfile == nil {
		config.ApplyEnvOverrides(runtimeCfg)
	}
	runtimeCfg.MainSettings.DBPath = a.fixedDBPath
	if err := runtimeCfg.Validate(); err != nil {
		return api.ConfigActivation{}, activationError(ActivationStageValidateRuntime, err)
	}

	currentStored, storedKnown, err := a.currentStored(ctx)
	if err != nil {
		return api.ConfigActivation{}, activationError(ActivationStageNormalize, err)
	}
	currentRuntime, err := a.currentRuntime(currentStored, storedKnown)
	if err != nil {
		return api.ConfigActivation{}, activationError(ActivationStageNormalize, err)
	}
	storedChanged := !storedKnown || !configsEqual(*stored, currentStored)
	effectiveChanged := !configsEqual(*runtimeCfg, currentRuntime)
	activation, activationKnown, err := a.currentActivation(ctx)
	if err != nil {
		return api.ConfigActivation{}, activationError(ActivationStagePersist, err)
	}
	if activation.Status == api.ConfigActivationPending && !consumingPending {
		return activation, &api.ConfigActivationPendingError{Activation: activation}
	}
	if !storedChanged && !effectiveChanged {
		if activation.Status == api.ConfigActivationFailed {
			return a.clearFailedActivation(ctx, activation)
		}
		return activation, nil
	}
	if !effectiveChanged {
		if err := a.persistStored(ctx, stored, nil); err != nil {
			return api.ConfigActivation{}, err
		}
		if activation.Status == api.ConfigActivationFailed {
			return a.clearFailedActivation(ctx, activation)
		}
		return activation, nil
	}

	impacts := configImpactDetails(currentRuntime, *runtimeCfg)
	deferActivation := func(blockingErr error) (api.ConfigActivation, error) {
		if !allowPending {
			return api.ConfigActivation{}, activationError(ActivationStagePersist, blockingErr)
		}
		if consumingPending {
			return activation, nil
		}
		return a.savePending(ctx, ownerID, stored, impacts)
	}
	if safe, safeErr := a.activationSafe(ctx); safeErr != nil {
		return api.ConfigActivation{}, activationError(ActivationStagePersist, safeErr)
	} else if !safe {
		return deferActivation(api.ErrActiveInputBusy)
	}

	if activationKnown {
		a.buildingConfigGeneration = activation.ActiveGeneration + 1
	}
	fingerprint, err := config.EffectiveConfigFingerprint(*runtimeCfg)
	if err != nil {
		return api.ConfigActivation{}, activationError(ActivationStageNormalize, fmt.Errorf("fingerprint runtime config: %w", err))
	}
	a.buildingConfigFingerprint = fingerprint
	defer func() {
		a.buildingConfigGeneration = 0
		a.buildingConfigFingerprint = ""
	}()
	generation, err := a.deps.build(ctx, *runtimeCfg, a.repo)
	if err != nil {
		return api.ConfigActivation{}, activationError(ActivationStageBuild, err)
	}
	installed := false
	defer func() {
		if !installed {
			RetiredRuntime{Owner: generation.Owner, Logger: generation.Logger}.Close()
		}
	}()

	if err := a.deps.cookies(ctx, a.repo, a.fixedDBPath, generation.Logger); err != nil {
		return api.ConfigActivation{}, activationError(ActivationStageCookies, err)
	}
	if a.deps.tryAcquireRuntimeAdmission != nil {
		releaseAdmission, admitted := a.deps.tryAcquireRuntimeAdmission()
		if !admitted {
			return deferActivation(api.ErrActiveInputBusy)
		}
		defer releaseAdmission()
	}
	if safe, safeErr := a.activationSafe(ctx); safeErr != nil {
		return api.ConfigActivation{}, activationError(ActivationStagePersist, safeErr)
	} else if !safe {
		return deferActivation(api.ErrActiveInputBusy)
	}
	activated, err := a.persistActivated(ctx, stored, activation, activationKnown, fingerprint, impacts, generation.Logger)
	if err != nil {
		if _, ok := errors.AsType[*runtimeCookiePersistenceError](err); ok {
			return api.ConfigActivation{}, activationError(ActivationStageCookies, err)
		}
		if errors.Is(err, api.ErrActiveInputBusy) || errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
			return deferActivation(err)
		}
		return api.ConfigActivation{}, activationError(ActivationStagePersist, err)
	}

	generation.Config = *runtimeCfg
	retired := a.installer.Install(generation)
	installed = true
	retired.Close()
	return activated, nil
}

func (a *RuntimeActivator) clearFailedActivation(ctx context.Context, activation api.ConfigActivation) (api.ConfigActivation, error) {
	if a.deps.clearFailure == nil {
		return activation, nil
	}
	cleared, err := a.deps.clearFailure(ctx, a.repo, activation.ActivationID)
	if err != nil {
		return api.ConfigActivation{}, activationError(ActivationStagePersist, fmt.Errorf("clear failed config activation: %w", err))
	}
	return cleared, nil
}

func configsEqual(left, right config.Config) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func (a *RuntimeActivator) currentStored(ctx context.Context) (config.Config, bool, error) {
	if a.deps.loadStored == nil {
		return config.Config{}, false, nil
	}
	stored, err := a.deps.loadStored(ctx, a.repo)
	if errors.Is(err, internalerrors.ErrNotFound) {
		return config.Config{}, false, nil
	}
	if err != nil {
		return config.Config{}, false, fmt.Errorf("load stored config: %w", err)
	}
	if stored == nil {
		return config.Config{}, false, errors.New("load stored config: nil config")
	}
	if _, err := config.MergeMissingTrackerDefaults(stored); err != nil {
		return config.Config{}, false, fmt.Errorf("normalize stored config: %w", err)
	}
	stored.MainSettings.DBPath = a.fixedDBPath
	return *stored, true, nil
}

func (a *RuntimeActivator) currentRuntime(stored config.Config, storedKnown bool) (config.Config, error) {
	if storedKnown {
		runtimeCfg, err := cloneConfig(stored)
		if err != nil {
			return config.Config{}, err
		}
		if a.liveProfile == nil {
			config.ApplyEnvOverrides(runtimeCfg)
		} else {
			configstore.ApplyLiveTestPaths(runtimeCfg, *a.liveProfile)
		}
		runtimeCfg.MainSettings.DBPath = a.fixedDBPath
		return *runtimeCfg, nil
	}
	if a.currentConfig == nil {
		return config.Config{}, nil
	}
	current, err := cloneConfig(a.currentConfig())
	if err != nil {
		return config.Config{}, err
	}
	if _, err := config.MergeMissingTrackerDefaults(current); err != nil {
		return config.Config{}, fmt.Errorf("normalize current config: %w", err)
	}
	current.MainSettings.DBPath = a.fixedDBPath
	return *current, nil
}

func (a *RuntimeActivator) currentActivation(ctx context.Context) (api.ConfigActivation, bool, error) {
	if a.deps.loadActivation == nil {
		return api.ConfigActivation{Status: api.ConfigActivationActive, Impacts: []api.ConfigImpact{}}, false, nil
	}
	activation, err := a.deps.loadActivation(ctx, a.repo)
	if err != nil {
		return api.ConfigActivation{}, false, err
	}
	return activation, true, nil
}

func (a *RuntimeActivator) activationSafe(ctx context.Context) (bool, error) {
	if a.deps.activationSafe == nil {
		return true, nil
	}
	return a.deps.activationSafe(ctx, a.repo)
}

func (a *RuntimeActivator) persistStored(ctx context.Context, stored *config.Config, logger api.Logger) error {
	if a.deps.persist == nil {
		return errors.New("runtime activation: config persistence is required")
	}
	if err := a.deps.persist(ctx, stored, a.repo, a.fixedDBPath, logger); err != nil {
		if _, ok := errors.AsType[*runtimeCookiePersistenceError](err); ok {
			return activationError(ActivationStageCookies, err)
		}
		return activationError(ActivationStagePersist, err)
	}
	return nil
}

func (a *RuntimeActivator) savePending(
	ctx context.Context, ownerID string, stored *config.Config, impacts []api.ConfigImpactDetail,
) (api.ConfigActivation, error) {
	if a.deps.savePending == nil {
		return api.ConfigActivation{}, errors.New("runtime activation: pending activation persistence is required")
	}
	encrypted, err := config.EncryptConfigSecrets(stored)
	if err != nil {
		return api.ConfigActivation{}, activationError(ActivationStagePersist, fmt.Errorf("encrypt pending config: %w", err))
	}
	payload, err := json.Marshal(encrypted)
	if err != nil {
		return api.ConfigActivation{}, activationError(ActivationStagePersist, fmt.Errorf("encode pending config: %w", err))
	}
	activation, err := a.deps.savePending(ctx, a.repo, ownerID, payload, impacts)
	if err != nil {
		return activation, activationError(ActivationStagePersist, err)
	}
	return activation, nil
}

func (a *RuntimeActivator) persistActivated(
	ctx context.Context,
	stored *config.Config,
	activation api.ConfigActivation,
	activationKnown bool,
	fingerprint api.WorkflowFingerprint,
	impacts []api.ConfigImpactDetail,
	logger api.Logger,
) (api.ConfigActivation, error) {
	if a.deps.persistActivated != nil {
		if !activationKnown {
			return api.ConfigActivation{}, errors.New("runtime activation: config activation generation is unavailable")
		}
		return a.deps.persistActivated(ctx, stored, a.repo, a.fixedDBPath, logger, activation.ActiveGeneration, fingerprint, impacts, a.deps.transform)
	}
	if err := a.persistStored(ctx, stored, logger); err != nil {
		return api.ConfigActivation{}, err
	}
	if !activationKnown {
		activation = api.ConfigActivation{Status: api.ConfigActivationActive, Impacts: []api.ConfigImpact{}}
	}
	return activation, nil
}

func liveTestImageTrackerInputs(cfg config.Config, registry *trackers.Registry) []liveTestImageTrackerInput {
	if registry == nil {
		return nil
	}
	hdbOwner := registry.OwnerForImageHost("hdb")
	thrOwner := registry.OwnerForImageHost("thr")
	inputs := make([]liveTestImageTrackerInput, 0, len(registry.Names()))
	for _, tracker := range registry.Names() {
		trackerCfg, _ := config.TrackerConfigByName(cfg.Trackers.Trackers, tracker)
		input := liveTestImageTrackerInput{
			Tracker:   tracker,
			ImageHost: strings.ToLower(strings.TrimSpace(trackerCfg.ImageHost)),
		}
		policy, hasPolicy := registry.LookupImageHostPolicy(tracker)
		if hasPolicy && policy.DisableWithoutRehost {
			input.ImgRehost = trackerCfg.ImgRehost
		}
		if (hasPolicy && policy.DisableWithoutAPI) || strings.EqualFold(tracker, thrOwner) {
			input.ImgAPI = strings.TrimSpace(trackerCfg.ImgAPI)
		}
		if strings.EqualFold(tracker, hdbOwner) {
			input.Username = strings.TrimSpace(trackerCfg.Username)
			input.Passkey = strings.TrimSpace(trackerCfg.Passkey)
		}
		inputs = append(inputs, input)
	}
	return inputs
}

func activationError(stage ActivationStage, err error) error {
	return &ActivationError{Stage: stage, Err: err}
}

func cloneConfig(source config.Config) (*config.Config, error) {
	payload, err := json.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("clone config: marshal: %w", err)
	}
	var cloned config.Config
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil, fmt.Errorf("clone config: unmarshal: %w", err)
	}
	return &cloned, nil
}

func validateRuntimeCookies(_ context.Context, _ *db.SQLiteRepository, dbPath string, _ api.Logger) error {
	if err := validateRuntimeCookieAuth(dbPath); err != nil && !errors.Is(err, cookies.ErrAuthHelperUnavailable) {
		return fmt.Errorf("validate cookie auth: %w", err)
	}
	return nil
}

func persistRuntimeConfigAndCookies(
	ctx context.Context,
	cfg *config.Config,
	repo *db.SQLiteRepository,
	dbPath string,
	logger api.Logger,
) error {
	return persistRuntimeConfigAndCookiesWithPreSave(ctx, cfg, repo, dbPath, logger, nil)
}

func persistRuntimeConfigAndActivate(
	ctx context.Context,
	cfg *config.Config,
	repo *db.SQLiteRepository,
	dbPath string,
	logger api.Logger,
	expectedGeneration uint64,
	fingerprint api.WorkflowFingerprint,
	impacts []api.ConfigImpactDetail,
	transform db.ConfigActivationWorkflowTransform,
) (api.ConfigActivation, error) {
	var activation api.ConfigActivation
	err := persistRuntimeConfigAndCookiesWithPreSave(ctx, cfg, repo, dbPath, logger, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		activation, err = repo.ActivateConfigTx(ctx, tx, expectedGeneration, fingerprint, impacts, transform)
		if err != nil {
			return fmt.Errorf("activate config generation: %w", err)
		}
		return nil
	})
	if err != nil {
		return activation, fmt.Errorf("persist activated runtime config: %w", err)
	}
	return activation, nil
}

func persistRuntimeConfigAndCookiesWithPreSave(
	ctx context.Context,
	cfg *config.Config,
	repo *db.SQLiteRepository,
	dbPath string,
	logger api.Logger,
	activate func(context.Context, *sql.Tx) error,
) error {
	if logger == nil {
		logger = api.NopLogger{}
	}
	save := func(preSave func(context.Context, *sql.Tx, []byte) error) error {
		return configstore.SaveToRepositoryWithPreSave(ctx, cfg, repo, dbPath, preSave)
	}
	runActivate := func(ctx context.Context, tx *sql.Tx) error {
		if activate == nil {
			return nil
		}
		return activate(ctx, tx)
	}
	cookiesDir, err := db.CookiePath(dbPath, "")
	if err != nil {
		logger.Debugf("runtime activation: cookie directory unavailable: %v", err)
		if err := save(func(ctx context.Context, tx *sql.Tx, _ []byte) error { return runActivate(ctx, tx) }); err != nil {
			return fmt.Errorf("persist runtime config without cookie directory: %w", err)
		}
		return nil
	}
	if !cookies.HasLegacyCookieFiles(cookiesDir) {
		if err := save(func(ctx context.Context, tx *sql.Tx, _ []byte) error { return runActivate(ctx, tx) }); err != nil {
			return fmt.Errorf("persist runtime config without legacy cookies: %w", err)
		}
		return nil
	}

	store, err := cookies.NewCookieStore(repo.RawDB())
	if err != nil {
		return &runtimeCookiePersistenceError{err: fmt.Errorf("create cookie store: %w", err)}
	}

	migratedCount := 0
	failedCookies := make([]cookies.FailedCookie, 0)
	err = save(func(ctx context.Context, tx *sql.Tx, key []byte) error {
		if len(key) == 0 {
			logger.Debugf("runtime activation: cookie migration skipped: web auth helper unavailable")
			return runActivate(ctx, tx)
		}
		var migrateErr error
		migratedCount, failedCookies, migrateErr = cookies.MigrateFromFilesToDBTx(ctx, cookiesDir, store, tx, key, logger)
		if migrateErr != nil {
			return &runtimeCookiePersistenceError{err: fmt.Errorf("migrate cookies: %w", migrateErr)}
		}
		return runActivate(ctx, tx)
	})
	if err != nil {
		return fmt.Errorf("persist runtime config with cookie migration: %w", err)
	}
	if migratedCount == 0 || len(failedCookies) > 0 {
		return nil
	}
	if err := cookies.DeleteMigratedCookieFiles(cookiesDir, logger); err != nil {
		logger.Warnf("cookies: migration cleanup failed dir=%s migrated=%d: %v", cookiesDir, migratedCount, err)
	}
	return nil
}

func validateRuntimeCookieAuth(dbPath string) error {
	material, err := authmaterial.LoadFromDBPath(dbPath)
	if err != nil {
		if errors.Is(err, authmaterial.ErrUnavailable) {
			return cookies.ErrAuthHelperUnavailable
		}
		return fmt.Errorf("load auth helper: %w", err)
	}
	if _, _, err := material.PrimaryHelper(); err != nil {
		if errors.Is(err, authmaterial.ErrUnavailable) {
			return cookies.ErrAuthHelperUnavailable
		}
		return fmt.Errorf("derive auth helper: %w", err)
	}
	return nil
}
