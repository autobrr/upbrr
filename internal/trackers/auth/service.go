// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/redaction"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	trackerscatalog "github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// Tracker auth status values returned in api.TrackerAuthStatus.State.
const (
	// MaxCookieImportContentBytes is the shared decoded cookie text byte limit for WebUI and service imports.
	MaxCookieImportContentBytes = 1024 * 1024

	// StateConfigured means required non-cookie config auth material is present.
	StateConfigured = "configured"
	// StateHasCookies means encrypted cookie storage contains at least one cookie for the tracker.
	StateHasCookies = "has_cookies"
	// StateNotConfigured means no supported auth material is currently configured.
	StateNotConfigured = "not_configured"
	// StateLoginRequired means credentials or imported cookies are required before tracker auth can proceed.
	StateLoginRequired = "login_required"
	// StateEncryptedStorageUnavailable means cookie import cannot be used until web auth material exists.
	StateEncryptedStorageUnavailable = "encrypted_storage_unavailable"

	// maxConcurrentValidations bounds simultaneous tracker and auth DB work.
	maxConcurrentValidations = 4

	expiredSessionActionMessage = "stored session expired or invalid; log in again or import fresh cookies"
)

// Service reports and manages persisted tracker auth material for configured trackers.
type Service struct {
	cfg                config.Config
	adapters           map[string]Adapter
	challenges         *ChallengeManager
	logger             api.Logger
	now                func() time.Time
	afterDeleteCleanup func()
	registry           *trackerscatalog.Registry
}

type trackerSpec struct {
	id        string
	authKind  string
	cookies   bool
	login     bool
	autoLogin bool
	totp      bool
	// manual2FA permits adapter Needs2FA errors to become reusable Submit2FA challenges.
	manual2FA        bool
	apiKey           bool
	passkey          bool
	needsCredentials bool
	notes            []string
}

// NewService builds a tracker auth service with default remote adapters and the shared manual 2FA challenge manager.
func NewService(cfg config.Config) *Service {
	return NewServiceWithLogger(cfg, api.NopLogger{})
}

// NewServiceWithLogger builds a tracker auth service that writes auth operation
// outcomes to logger without logging cookie values, credentials, 2FA codes, or
// challenge identifiers. Nil logger uses [api.NopLogger].
func NewServiceWithLogger(cfg config.Config, logger api.Logger) *Service {
	return NewServiceWithRegistryAndLogger(cfg, nil, logger)
}

// NewServiceWithRegistryAndLogger builds a tracker auth service using tracker-owned auth capabilities.
func NewServiceWithRegistryAndLogger(cfg config.Config, registry *trackerscatalog.Registry, logger api.Logger) *Service {
	if logger == nil {
		logger = api.NopLogger{}
	}
	return &Service{
		cfg:        cfg,
		adapters:   defaultAdapters(registry),
		challenges: sharedChallengeManager,
		logger:     logger,
		now:        time.Now,
		registry:   registry,
	}
}

func (s *Service) logInfof(format string, args ...any) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Infof(format, args...)
}

func (s *Service) logTracef(format string, args ...any) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Tracef(format, args...)
}

func (s *Service) logWarnf(format string, args ...any) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Warnf(format, args...)
}

// logStatus records the user-visible auth state returned to callers, keeping
// the log payload to tracker ID, state, cookie count, encrypted-storage state,
// 2FA requirement, and already-redacted status errors.
func (s *Service) logStatus(operation string, status api.TrackerAuthStatus) {
	if strings.TrimSpace(status.LastError) != "" {
		s.logWarnf(
			"tracker auth: %s warning tracker=%s state=%s error=%s",
			operation,
			trackerLogID(status.TrackerID),
			status.State,
			status.LastError,
		)
		return
	}
	logf := s.logTracef
	if operation == "login completed" && status.State == StateConfigured {
		logf = s.logInfof
	}
	logf(
		"tracker auth: %s tracker=%s state=%s cookies=%d encrypted_storage=%t needs_2fa=%t",
		operation,
		trackerLogID(status.TrackerID),
		status.State,
		status.CookieCount,
		status.EncryptedStorage,
		status.Needs2FA,
	)
}

// trackerLogID normalizes tracker IDs for log messages and avoids empty tracker
// fields when validation fails before a concrete tracker is resolved.
func trackerLogID(trackerID string) string {
	trackerID = normalizeTrackerID(trackerID)
	if trackerID == "" {
		return "unknown"
	}
	return trackerID
}

// Capabilities returns tracker auth support metadata for built-in trackers and configured custom trackers.
func (s *Service) Capabilities(_ context.Context) (caps []api.TrackerAuthCapability, err error) {
	defer func() {
		if err != nil {
			s.logWarnf("tracker auth: capabilities failed: %v", err)
			return
		}
		s.logTracef("tracker auth: capabilities loaded count=%d", len(caps))
	}()
	if err := s.validateTrackerConfigIDs(); err != nil {
		return nil, err
	}
	specs := s.specs()
	out := make([]api.TrackerAuthCapability, 0, len(specs))
	for _, spec := range specs {
		out = append(out, s.capabilityForSpec(spec))
	}
	return out, nil
}

// Status returns local tracker auth state derived from config, encrypted cookie storage, and stored cookies.
func (s *Service) Status(ctx context.Context, trackerID string) (status api.TrackerAuthStatus, err error) {
	defer func() {
		if err != nil {
			s.logWarnf("tracker auth: status failed tracker=%s: %v", trackerLogID(trackerID), err)
			return
		}
		s.logStatus("status checked", status)
	}()
	if err := s.validateTrackerConfigIDs(); err != nil {
		return api.TrackerAuthStatus{}, err
	}
	spec, err := s.specFor(trackerID)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	return s.statusForSpec(ctx, spec), nil
}

// ImportCookies parses supplied cookie content, saves it for trackerID, and
// returns refreshed auth status from persisted cookie/auth state. Parser errors
// leave existing stored cookies unchanged. BTN imports still report a missing
// API-key prerequisite when cookies alone do not make the tracker upload-ready.
func (s *Service) ImportCookies(ctx context.Context, trackerID string, fileName string, content string) (status api.TrackerAuthStatus, err error) {
	defer func() {
		if err != nil {
			s.logWarnf("tracker auth: cookie import failed tracker=%s bytes=%d: %v", trackerLogID(trackerID), len(content), err)
			return
		}
		s.logStatus("cookies imported", status)
	}()
	if err := s.validateTrackerConfigIDs(); err != nil {
		return api.TrackerAuthStatus{}, err
	}
	spec, err := s.specFor(trackerID)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	if !spec.cookies {
		return api.TrackerAuthStatus{}, fmt.Errorf("tracker auth: %s does not support cookie import", spec.id)
	}
	values, err := ParseCookieContent(fileName, content)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	if err := cookies.SaveTrackerCookieMap(ctx, s.cfg.MainSettings.DBPath, spec.id, values); err != nil {
		return api.TrackerAuthStatus{}, fmt.Errorf("tracker auth: import %s cookies: %w", spec.id, err)
	}
	status = s.statusForSpec(ctx, spec)
	if status.State != StateLoginRequired || !isBTNSpec(spec) || !strings.Contains(status.Message, "API key is required") {
		status.State = StateHasCookies
		status.Message = "cookies imported"
	}
	return status, nil
}

// Validate returns tracker auth status after a remote validation check when the
// tracker has an adapter. Confirmed-invalid stored sessions are deleted and
// reported as login-required status without returning an error. BTN session
// success remains login-required until the API token needed for torrent
// resolution is configured. Returned cookie counts and RFC3339 timestamps are
// rebuilt after login or deletion side effects complete.
func (s *Service) Validate(ctx context.Context, trackerID string) (status api.TrackerAuthStatus, err error) {
	defer func() {
		if err != nil {
			s.logWarnf("tracker auth: validation failed tracker=%s: %v", trackerLogID(trackerID), err)
			return
		}
		s.logStatus("validation completed", status)
	}()
	if err := s.validateTrackerConfigIDs(); err != nil {
		return api.TrackerAuthStatus{}, err
	}
	spec, err := s.specFor(trackerID)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	status = s.statusForSpec(ctx, spec)

	if _, ok := s.adapterFor(spec.id); ok {
		session, ensureErr := s.EnsureSession(ctx, EnsureRequest{
			TrackerID: spec.id,
			Config:    mustTrackerConfig(s.cfg, spec.id),
			DBPath:    s.cfg.MainSettings.DBPath,
			AutoLogin: true,
		})
		if ensureErr != nil {
			status = s.statusForSpec(ctx, spec)
			applyEnsureErrorToStatus(&status, ensureErr)
			if session.ChallengeID != "" {
				status.ChallengeID = session.ChallengeID
			}
			return status, nil
		}
		status = s.statusForSpec(ctx, spec)
		if isBTNSpec(spec) && !s.btnAPIKeyConfigured() {
			status.State = StateLoginRequired
			status.Message = btnMissingAPIKeyMessage()
			return status, nil
		}
		status.State = StateConfigured
		status.Message = "remote auth check succeeded"
		return status, nil
	}
	status.Message = "remote auth validation is not supported for this tracker"
	return status, nil
}

// ValidateMany validates every tracker concurrently and returns statuses in
// input order. It waits for the full batch with bounded concurrency; if any
// validation fails, it returns the first input-ordered error and no statuses.
func (s *Service) ValidateMany(ctx context.Context, trackerIDs []string) ([]api.TrackerAuthStatus, error) {
	statuses := make([]api.TrackerAuthStatus, len(trackerIDs))
	errs := make([]error, len(trackerIDs))

	var group errgroup.Group
	group.SetLimit(maxConcurrentValidations)
	for i, trackerID := range trackerIDs {
		group.Go(func() error {
			statuses[i], errs[i] = s.Validate(ctx, trackerID)
			return nil
		})
	}
	_ = group.Wait()

	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("tracker auth: validate %s: %w", trackerLogID(trackerIDs[i]), err)
		}
	}
	return statuses, nil
}

// IsManagedCapability reports whether a capability requires session, login,
// refresh, or 2FA handling rather than only static API-key/passkey config.
func IsManagedCapability(capability api.TrackerAuthCapability) bool {
	authKind := strings.ToLower(strings.TrimSpace(capability.AuthKind))
	return capability.SupportsCookieFile ||
		capability.SupportsLogin ||
		capability.SupportsAutoLogin ||
		capability.SupportsTOTP ||
		capability.SupportsManual2FA ||
		strings.Contains(authKind, "refresh") ||
		strings.Contains(authKind, "2fa")
}

// IsReadyStatus reports whether a status represents usable auth without further
// user input. A pending 2FA challenge is never ready, regardless of state.
func IsReadyStatus(status api.TrackerAuthStatus) bool {
	if status.Needs2FA {
		return false
	}
	switch strings.TrimSpace(status.State) {
	case StateConfigured, StateHasCookies:
		return true
	default:
		return false
	}
}

// Login runs credential-based tracker auth when supported and returns status for
// missing credentials, unsupported remote login, or 2FA. Trackers with separate
// API prerequisites, such as BTN, keep the narrower prerequisite status when
// credential login cannot proceed or completes without making the tracker ready.
func (s *Service) Login(ctx context.Context, trackerID string, req api.TrackerAuthLoginRequest) (status api.TrackerAuthStatus, err error) {
	defer func() {
		if err != nil {
			s.logWarnf("tracker auth: login failed tracker=%s: %v", trackerLogID(trackerID), err)
			return
		}
		s.logStatus("login completed", status)
	}()
	if err := s.validateTrackerConfigIDs(); err != nil {
		return api.TrackerAuthStatus{}, err
	}
	spec, err := s.specFor(trackerID)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	if !spec.login {
		return api.TrackerAuthStatus{}, fmt.Errorf("tracker auth: %s does not support credential login", spec.id)
	}
	trackerCfg := mustTrackerConfig(s.cfg, spec.id)
	status = s.statusForSpec(ctx, spec)
	if status.State == StateLoginRequired && !hasUsableLoginConfig(spec, trackerCfg) {
		if !hasSpecificLoginBlocker(status) {
			status.Message = "username/password missing"
		}
		return status, nil
	}

	if _, ok := s.adapterFor(spec.id); ok {
		session, ensureErr := s.EnsureSession(ctx, EnsureRequest{
			TrackerID: spec.id,
			Config:    trackerCfg,
			DBPath:    s.cfg.MainSettings.DBPath,
			AutoLogin: true,
			Login:     req,
		})
		if ensureErr != nil {
			applyEnsureErrorToStatus(&status, ensureErr)
			if session.ChallengeID != "" {
				status.ChallengeID = session.ChallengeID
			}
			return status, nil
		}
		if isBTNSpec(spec) && !s.btnAPIKeyConfigured() {
			return s.statusForSpec(ctx, spec), nil
		}
		status.State = StateConfigured
		status.Message = "remote login/auth check succeeded"
		return status, nil
	}
	status.Message = "remote credential login is not supported for this tracker"
	return status, nil
}

// Submit2FA completes an active manual 2FA challenge. Adapter failures return
// refreshed status without consuming the challenge; only failures classified
// with [ValidationError.Submitted2FARejected] keep the challenge visible for
// another try. Successful challenge completion clears the challenge fields but
// does not override separate tracker prerequisites such as BTN's API key.
func (s *Service) Submit2FA(ctx context.Context, challengeID string, code string) (status api.TrackerAuthStatus, err error) {
	logTrackerID := ""
	defer func() {
		if logTrackerID == "" {
			logTrackerID = status.TrackerID
		}
		if err != nil {
			s.logWarnf("tracker auth: 2FA submit failed tracker=%s: %v", trackerLogID(logTrackerID), err)
			return
		}
		s.logStatus("2FA completed", status)
	}()
	if strings.TrimSpace(challengeID) == "" {
		return api.TrackerAuthStatus{}, errors.New("tracker auth: challenge id is required")
	}
	if strings.TrimSpace(code) == "" {
		return api.TrackerAuthStatus{}, errors.New("tracker auth: 2FA code is required")
	}
	challenges := s.challengeManager()
	challenge, ok := challenges.Get(challengeID)
	if !ok {
		return api.TrackerAuthStatus{}, errors.New("tracker auth: no active manual 2FA challenge")
	}
	logTrackerID = challenge.TrackerID
	ownerKey, err := s.challengeOwnerKey(challenge.TrackerID)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	if !challengeOwnerMatches(challenge, ownerKey) {
		return api.TrackerAuthStatus{}, errors.New("tracker auth: stale manual 2FA challenge")
	}
	adapter, ok := s.adapterFor(challenge.TrackerID)
	if !ok {
		return api.TrackerAuthStatus{}, newUnknownTrackerError(challenge.TrackerID)
	}
	session, err := adapter.Submit2FA(ctx, mustTrackerConfig(s.cfg, challenge.TrackerID), s.cfg.MainSettings.DBPath, api.TrackerAuthLoginRequest{Code: code})
	if err != nil {
		status, statusErr := s.Status(ctx, challenge.TrackerID)
		if statusErr != nil {
			return api.TrackerAuthStatus{}, statusErr
		}
		applyEnsureErrorToStatus(&status, err)
		if !shouldKeepSubmitted2FARetryVisible(challenge.TrackerID, err) {
			return status, nil
		}
		status.Needs2FA = true
		status.ChallengeID = challengeID
		status.State = StateLoginRequired
		status.Message = "2FA code rejected; enter a new 2FA code"
		return status, nil
	}
	if _, err := challenges.Consume(challengeID, challenge.TrackerID, ownerKey); err != nil {
		return api.TrackerAuthStatus{}, err
	}
	status, err = s.Status(ctx, challenge.TrackerID)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	missingBTNAPIKey := isBTNSpec(trackerSpec{id: challenge.TrackerID}) && !s.btnAPIKeyConfigured()
	if (strings.TrimSpace(session.State) == "" || session.State == SessionStateReady) && !missingBTNAPIKey {
		status.State = StateConfigured
	}
	status.Needs2FA = false
	status.ChallengeID = ""
	if !missingBTNAPIKey {
		status.Message = "2FA auth completed"
	}
	return status, nil
}

// shouldKeepSubmitted2FARetryVisible reports whether an adapter proved the
// submitted manual code reached the tracker and was rejected, so the UI can
// retry the same active challenge.
func shouldKeepSubmitted2FARetryVisible(_ string, err error) bool {
	validation, ok := asValidationError(err)
	return ok && validation.Transient && !validation.ConfirmedInvalid && validation.Submitted2FARejected
}

func (s *Service) challengeManager() *ChallengeManager {
	if s.challenges == nil {
		s.challenges = sharedChallengeManager
	}
	return s.challenges
}

// Delete removes tracker-specific auth state and generic cookies, then returns
// the refreshed local auth status. AR auth-state and cookie cleanup failures
// restore the previous AR auth state before returning the error even when the
// caller's context has been canceled.
func (s *Service) Delete(ctx context.Context, trackerID string) (status api.TrackerAuthStatus, err error) {
	defer func() {
		if err != nil {
			s.logWarnf("tracker auth: delete failed tracker=%s: %v", trackerLogID(trackerID), err)
			return
		}
		s.logStatus("auth deleted", status)
	}()
	if err := s.validateTrackerConfigIDs(); err != nil {
		return api.TrackerAuthStatus{}, err
	}
	spec, err := s.specFor(trackerID)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	authSnapshot, err := snapshotTrackerAuthState(ctx, s.cfg.MainSettings.DBPath, spec.id)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	if err := deleteTrackerAuthState(ctx, s.cfg.MainSettings.DBPath, spec.id); err != nil {
		restoreErr := authSnapshot.restore(ctx)
		deleteErr := fmt.Errorf("tracker auth: delete %s auth state: %w", spec.id, err)
		if restoreErr != nil {
			return api.TrackerAuthStatus{}, errors.Join(deleteErr, restoreErr)
		}
		return api.TrackerAuthStatus{}, deleteErr
	}
	if err := cookies.DeleteTrackerCookies(ctx, s.cfg.MainSettings.DBPath, spec.id); err != nil {
		restoreErr := authSnapshot.restore(ctx)
		deleteErr := fmt.Errorf("tracker auth: delete %s cookies: %w", spec.id, err)
		if restoreErr != nil {
			return api.TrackerAuthStatus{}, errors.Join(deleteErr, restoreErr)
		}
		return api.TrackerAuthStatus{}, deleteErr
	}
	if s.afterDeleteCleanup != nil {
		s.afterDeleteCleanup()
	}
	status = s.statusForSpec(contextWithoutCancel(ctx), spec)
	status.CookieCount = 0
	status.Message = "stored auth deleted"
	return status, nil
}

// statusForSpec reports configured auth material without hiding encrypted
// storage availability. It does not create manual 2FA status; Login, Validate,
// and Submit2FA attach active challenges only when a challenge ID exists.
func (s *Service) statusForSpec(ctx context.Context, spec trackerSpec) api.TrackerAuthStatus {
	encryptedStorage := s.encryptedStorageAvailable()
	status := api.TrackerAuthStatus{
		TrackerID:        spec.id,
		DisplayName:      spec.id,
		State:            StateNotConfigured,
		LastCheckedAt:    s.currentTime().UTC().Format(time.RFC3339),
		EncryptedStorage: encryptedStorage,
	}

	cfg, hasCfg := trackerConfig(s.cfg, spec.id)
	hasAPIKey := spec.apiKey && configAPIKey(cfg) != ""
	if isBTNSpec(spec) {
		hasAPIKey = s.btnAPIKeyConfigured()
	}
	hasPasskey := strings.TrimSpace(cfg.Passkey) != ""
	hasCredentials := spec.login && hasCfg && hasUsableLoginConfig(spec, cfg)
	if spec.cookies {
		values, err := cookies.LoadTrackerCookieMap(ctx, s.cfg.MainSettings.DBPath, spec.id)
		if err == nil && len(values) > 0 {
			status.CookieCount = len(values)
			status.State = StateHasCookies
		} else if err != nil && !strings.Contains(strings.ToLower(err.Error()), "no cookies found") {
			status.LastError = redact(err.Error())
		}
	}

	if hasAPIKey && (!uploadAuthNeedsCookiesOrLogin(spec) || hasCredentials || (isMTVSpec(spec) && status.CookieCount > 0)) {
		status.State = StateConfigured
	}
	missingBTNAPIKey := isBTNSpec(spec) && !hasAPIKey && (status.CookieCount > 0 || hasCredentials)
	if missingBTNAPIKey {
		status.State = StateLoginRequired
	}
	if passkeyCoversAuth(spec) && hasPasskey {
		status.State = StateConfigured
	}
	if isHDBSpec(spec) && hasPasskey && strings.TrimSpace(cfg.Username) != "" && status.CookieCount > 0 {
		status.State = StateConfigured
	}
	if isHDBSpec(spec) && hasPasskey && strings.TrimSpace(cfg.Username) != "" && status.CookieCount == 0 {
		status.State = StateLoginRequired
	}
	if spec.login {
		if hasCredentials {
			if status.State == StateNotConfigured {
				status.State = StateConfigured
			}
		} else if status.CookieCount == 0 && spec.needsCredentials {
			status.State = StateLoginRequired
		}
	}
	if !missingBTNAPIKey && spec.cookies && status.CookieCount == 0 && !encryptedStorage &&
		authStatusRequiresEncryptedStorage(spec, hasCredentials, hasAPIKey, hasPasskey) {
		status.State = StateEncryptedStorageUnavailable
	}
	status.Message = validationMessage(spec, status)
	if status.State != StateEncryptedStorageUnavailable && uploadAuthNeedsCookiesOrLogin(spec) && hasAPIKey && status.CookieCount == 0 && !hasCredentials {
		status.Message = uploadAuthRequiredMessage(spec)
	}
	if missingBTNAPIKey {
		status.Message = btnMissingAPIKeyMessage()
	}
	return status
}

// currentTime returns the service clock, falling back to the wall clock for
// zero-value or test-constructed services without an injected clock.
func (s *Service) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

// btnAPIKeyConfigured reports whether BTN has an API token in tracker config or
// legacy metadata; a valid browser session is not upload-ready without it.
func (s *Service) btnAPIKeyConfigured() bool {
	return strings.TrimSpace(config.ResolveBTNAPIToken(s.cfg)) != ""
}

// btnMissingAPIKeyMessage is the shared user-visible status for BTN when
// cookies or credentials can cover upload auth but torrent resolution cannot run.
func btnMissingAPIKeyMessage() string {
	return "API key is required for torrent resolution; imported cookies or login credentials cover upload auth"
}

func isMTVSpec(spec trackerSpec) bool {
	return strings.EqualFold(spec.id, "MTV")
}

func isBTNSpec(spec trackerSpec) bool {
	return strings.EqualFold(spec.id, "BTN")
}

func isHDBSpec(spec trackerSpec) bool {
	return strings.EqualFold(spec.id, "HDB")
}

// uploadAuthNeedsCookiesOrLogin reports trackers where an API key is necessary
// but does not prove upload-session readiness.
func uploadAuthNeedsCookiesOrLogin(spec trackerSpec) bool {
	return isMTVSpec(spec) || isBTNSpec(spec)
}

// uploadAuthRequiredMessage explains the split between API/search auth and
// browser-session upload auth for trackers that need both.
func uploadAuthRequiredMessage(spec trackerSpec) string {
	if isBTNSpec(spec) {
		return "API key covers torrent resolution; imported cookies or login credentials required for upload auth"
	}
	return "API key covers Torznab/search; imported cookies or login credentials required for upload auth"
}

func passkeyCoversAuth(spec trackerSpec) bool {
	return spec.passkey && !isHDBSpec(spec)
}

// hasUsableLoginConfig reports whether credential login has every config value
// required by the tracker implementation. PTP also needs announce_url because
// login derives the passkey from it.
func hasUsableLoginConfig(spec trackerSpec, cfg config.TrackerConfig) bool {
	if strings.TrimSpace(cfg.Username) == "" || strings.TrimSpace(cfg.Password) == "" {
		return false
	}
	if strings.EqualFold(spec.id, "PTP") && strings.TrimSpace(cfg.AnnounceURL) == "" {
		return false
	}
	return true
}

// hasSpecificLoginBlocker reports whether status already explains a prerequisite
// more specific than the generic login-required message.
func hasSpecificLoginBlocker(status api.TrackerAuthStatus) bool {
	message := strings.TrimSpace(status.Message)
	return message != "" && message != validationMessage(trackerSpec{}, status)
}

func (s *Service) encryptedStorageAvailable() bool {
	if strings.TrimSpace(s.cfg.MainSettings.DBPath) == "" {
		return false
	}
	_, err := authmaterial.LoadFromDBPath(s.cfg.MainSettings.DBPath)
	return err == nil
}

func authStatusRequiresEncryptedStorage(spec trackerSpec, hasCredentials bool, hasAPIKey bool, hasPasskey bool) bool {
	if passkeyCoversAuth(spec) && hasPasskey {
		return false
	}
	if spec.apiKey && hasAPIKey && !uploadAuthNeedsCookiesOrLogin(spec) {
		return false
	}
	return !hasAPIKey || uploadAuthNeedsCookiesOrLogin(spec) || hasCredentials
}

// specFor resolves built-in and configured tracker IDs through normalizeTrackerID
// so ASCII case differences match while non-ASCII runes keep their original case.
func (s *Service) specFor(trackerID string) (trackerSpec, error) {
	needle := normalizeTrackerID(trackerID)
	if needle == "" {
		return trackerSpec{}, errors.New("tracker auth: tracker id is required")
	}
	for _, spec := range s.specs() {
		if spec.id == needle {
			return spec, nil
		}
	}
	return trackerSpec{}, fmt.Errorf("tracker auth: unknown tracker %s", needle)
}

func (s *Service) validateTrackerConfigIDs() error {
	seen := map[string]string{}
	for id := range s.cfg.Trackers.Trackers {
		trimmedID := strings.TrimSpace(id)
		if trimmedID == "" {
			continue
		}
		normalizedID := normalizeTrackerID(trimmedID)
		if previous, ok := seen[normalizedID]; ok && previous != trimmedID {
			return fmt.Errorf("tracker auth: duplicate tracker config id %q conflicts with %q", trimmedID, previous)
		}
		seen[normalizedID] = trimmedID
	}
	return nil
}

// specs returns built-ins plus configured trackers indexed by the same
// canonical ID used by lookup and duplicate-config validation.
func (s *Service) specs() []trackerSpec {
	index := map[string]trackerSpec{}
	for _, spec := range builtInSpecs() {
		index[spec.id] = spec
	}
	for id, cfg := range s.cfg.Trackers.Trackers {
		trimmedID := strings.TrimSpace(id)
		if trimmedID == "" {
			continue
		}
		normalizedID := normalizeTrackerID(trimmedID)
		spec, ok := index[normalizedID]
		if !ok {
			spec = trackerSpec{id: normalizedID, authKind: "config"}
		}
		if configAPIKey(cfg) != "" {
			spec.apiKey = true
			if spec.authKind == "config" {
				spec.authKind = "api_key"
			}
		}
		if strings.TrimSpace(cfg.Passkey) != "" {
			spec.passkey = true
			if spec.authKind == "config" {
				spec.authKind = "passkey"
			}
		}
		if strings.TrimSpace(cfg.Username) != "" || strings.TrimSpace(cfg.Password) != "" {
			if !ok || spec.login {
				spec.login = true
				spec.autoLogin = spec.autoLogin || spec.id == "AR" || spec.id == "BTN" || spec.id == "FF" || spec.id == "FL" || spec.id == "MTV" ||
					spec.id == "PTP" ||
					spec.id == "RTF" ||
					spec.id == "THR"
				if spec.authKind == "config" {
					spec.authKind = "credential_login"
				}
			}
		}
		index[normalizedID] = spec
	}

	out := make([]trackerSpec, 0, len(index))
	for _, spec := range index {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// builtInSpecs defines static tracker auth capabilities before adapter support
// is applied. Manual 2FA is enabled only where login can retry with a submitted
// api.TrackerAuthLoginRequest.Code.
func builtInSpecs() []trackerSpec {
	return []trackerSpec{
		{
			id:               "AR",
			authKind:         "cookies_login",
			cookies:          true,
			login:            true,
			autoLogin:        true,
			needsCredentials: true,
		},
		{
			id:               "BTN",
			authKind:         "api_key_cookies_login_manual_2fa",
			cookies:          true,
			login:            true,
			autoLogin:        true,
			totp:             true,
			manual2FA:        true,
			apiKey:           true,
			needsCredentials: true,
			notes:            []string{"API key is required for torrent resolution; cookies/login cover upload auth."},
		},
		{
			id:               "FF",
			authKind:         "cookies_login",
			cookies:          true,
			login:            true,
			autoLogin:        true,
			needsCredentials: true,
		},
		{
			id:               "FL",
			authKind:         "cookies_login",
			cookies:          true,
			login:            true,
			autoLogin:        true,
			needsCredentials: true,
		},
		{
			id:               "MTV",
			authKind:         "api_key_cookies_login_manual_2fa",
			cookies:          true,
			login:            true,
			autoLogin:        true,
			totp:             true,
			manual2FA:        true,
			apiKey:           true,
			needsCredentials: true,
			notes:            []string{"API key covers Torznab/search; cookies/login cover upload authkey."},
		},
		{
			id:               "PTP",
			authKind:         "cookies_login_manual_2fa",
			cookies:          true,
			login:            true,
			autoLogin:        true,
			totp:             true,
			manual2FA:        true,
			needsCredentials: true,
		},
		{
			id:               "THR",
			authKind:         "credential_login",
			login:            true,
			autoLogin:        true,
			needsCredentials: true,
		},
		{
			id:               "RTF",
			authKind:         "api_key_credential_refresh",
			login:            true,
			autoLogin:        true,
			apiKey:           true,
			needsCredentials: false,
		},
		{
			id:       "ASC",
			authKind: "cookies",
			cookies:  true,
		},
		{
			id:       "AZ",
			authKind: "cookies",
			cookies:  true,
		},
		{
			id:       "BJS",
			authKind: "cookies",
			cookies:  true,
		},
		{
			id:       "BT",
			authKind: "cookies",
			cookies:  true,
		},
		{
			id:       "CZ",
			authKind: "cookies",
			cookies:  true,
		},
		{
			id:       "HDB",
			authKind: "passkey_cookies",
			cookies:  true,
			passkey:  true,
		},
		{
			id:       "HDS",
			authKind: "cookies",
			cookies:  true,
		},
		{
			id:       "HDT",
			authKind: "cookies",
			cookies:  true,
		},
		{
			id:       "IS",
			authKind: "cookies",
			cookies:  true,
		},
		{
			id:       "PHD",
			authKind: "cookies",
			cookies:  true,
		},
		{
			id:       "PTS",
			authKind: "cookies",
			cookies:  true,
		},
		{
			id:       "TL",
			authKind: "cookies",
			cookies:  true,
		},
	}
}

func capabilityFromSpec(spec trackerSpec) api.TrackerAuthCapability {
	return api.TrackerAuthCapability{
		TrackerID:          spec.id,
		DisplayName:        spec.id,
		AuthKind:           spec.authKind,
		SupportsCookieFile: spec.cookies,
		SupportsLogin:      spec.login,
		SupportsAutoLogin:  spec.autoLogin,
		SupportsTOTP:       spec.totp,
		SupportsManual2FA:  spec.manual2FA,
		RequiresAPIKey:     spec.apiKey,
		RequiresPasskey:    spec.passkey,
		Notes:              append([]string{}, spec.notes...),
	}
}

// capabilityForSpec exposes remote login, auto-login, and 2FA actions only
// when this service has an adapter that can execute them.
func (s *Service) capabilityForSpec(spec trackerSpec) api.TrackerAuthCapability {
	if capability, ok := s.registry.LookupAuthCapability(spec.id); ok {
		if _, hasAdapter := s.adapterFor(spec.id); !hasAdapter {
			capability.SupportsLogin = false
			capability.SupportsAutoLogin = false
			capability.SupportsTOTP = false
			capability.SupportsManual2FA = false
		}
		return capability
	}
	if adapter, ok := s.adapterFor(spec.id); ok {
		return adapter.Capability()
	}
	capability := capabilityFromSpec(spec)
	capability.SupportsLogin = false
	capability.SupportsAutoLogin = false
	capability.SupportsTOTP = false
	capability.SupportsManual2FA = false
	return capability
}

func (s *Service) challengeOwnerKey(trackerID string) (string, error) {
	if err := s.validateTrackerConfigIDs(); err != nil {
		return "", err
	}
	cfg, ok := trackerConfig(s.cfg, trackerID)
	if !ok {
		cfg = config.TrackerConfig{}
	}
	// #nosec G117 -- payload is only hashed for a local challenge owner key.
	payload, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("tracker auth: encode challenge owner: %w", err)
	}
	sum := sha256.Sum256(payload)
	return normalizeTrackerID(trackerID) + ":" + hex.EncodeToString(sum[:]), nil
}

func trackerConfig(cfg config.Config, trackerID string) (config.TrackerConfig, bool) {
	normalizedTrackerID := normalizeTrackerID(trackerID)
	for key, value := range cfg.Trackers.Trackers {
		if normalizeTrackerID(key) == normalizedTrackerID {
			return value, true
		}
	}
	return config.TrackerConfig{}, false
}

func configAPIKey(cfg config.TrackerConfig) string {
	if key := strings.TrimSpace(cfg.APIKey); key != "" {
		return key
	}
	return strings.TrimSpace(cfg.PTPAPIKey)
}

func validationMessage(spec trackerSpec, status api.TrackerAuthStatus) string {
	switch status.State {
	case StateHasCookies:
		return fmt.Sprintf("%d stored cookie(s); tracker upload/search will validate remotely when used", status.CookieCount)
	case StateConfigured:
		if status.CookieCount > 0 {
			return fmt.Sprintf("required config auth material is present; %d stored cookie(s)", status.CookieCount)
		}
		return "required config auth material is present"
	case StateLoginRequired:
		return "login credentials or imported cookies required"
	case StateEncryptedStorageUnavailable:
		return "encrypted cookie storage unavailable; create web-auth.json before importing cookies"
	default:
		if spec.cookies || spec.login || spec.apiKey || spec.passkey {
			return "auth material not configured"
		}
		return "no tracker-specific auth handling"
	}
}

// ParseCookieContent parses JSON cookie exports or Netscape cookie files into
// a non-empty name/value map. Raw content above [MaxCookieImportContentBytes],
// malformed JSON-looking payloads, JSON array entries without name and value,
// and duplicate trimmed cookie names are rejected; cookie values are preserved
// byte-for-byte after decoding.
func ParseCookieContent(fileName string, content string) (map[string]string, error) {
	if len(content) > MaxCookieImportContentBytes {
		return nil, fmt.Errorf("tracker auth: cookie file content exceeds %d byte limit", MaxCookieImportContentBytes)
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, errors.New("tracker auth: cookie file content is required")
	}
	if shouldParseJSONCookies(fileName, trimmed) {
		return parseJSONCookieContent([]byte(trimmed))
	}
	return parseNetscapeCookieContent(content)
}

// shouldParseJSONCookies accepts JSON by extension, leading object, or a valid
// leading array so exported array payloads are not filename-dependent.
func shouldParseJSONCookies(fileName string, trimmed string) bool {
	return strings.EqualFold(filepath.Ext(strings.TrimSpace(fileName)), ".json") ||
		strings.HasPrefix(trimmed, "{") ||
		strings.HasPrefix(trimmed, "[")
}

func parseJSONCookieContent(payload []byte) (map[string]string, error) {
	if err := rejectDuplicateJSONCookieNames(payload); err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
	}
	out := make(map[string]string)
	switch typed := decoded.(type) {
	case map[string]any:
		if err := addJSONCookieObject(out, typed); err != nil {
			return nil, err
		}
	case []any:
		for _, item := range typed {
			entry, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("tracker auth: JSON cookie array entries must be objects")
			}
			if err := addJSONCookieArrayEntry(out, entry); err != nil {
				return nil, err
			}
		}
	default:
		return nil, errors.New("tracker auth: JSON cookie content must be an object or array")
	}
	if len(out) == 0 {
		return nil, errors.New("tracker auth: cookie content has no entries")
	}
	return out, nil
}

// rejectDuplicateJSONCookieNames rejects duplicates only where JSON fields
// become effective cookie names or values before json.Unmarshal can collapse
// them into a map.
func rejectDuplicateJSONCookieNames(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		return rejectDuplicateJSONRootCookieNames(decoder)
	case '[':
		return rejectDuplicateJSONArrayCookieFields(decoder)
	default:
		return nil
	}
}

func rejectDuplicateJSONRootCookieNames(decoder *json.Decoder) error {
	seen := map[string]struct{}{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("tracker auth: JSON cookie object has non-string key")
		}
		name := strings.TrimSpace(key)
		if name != "" {
			if _, ok := seen[name]; ok {
				return fmt.Errorf("tracker auth: duplicate cookie name %q", name)
			}
			seen[name] = struct{}{}
		}
		valueToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
		}
		if err := rejectDuplicateJSONObjectCookieFields(decoder, valueToken, map[string]struct{}{"value": {}}); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
	}
	return nil
}

func rejectDuplicateJSONArrayCookieFields(decoder *json.Decoder) error {
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
		}
		if err := rejectDuplicateJSONObjectCookieFields(decoder, token, map[string]struct{}{"name": {}, "value": {}}); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
	}
	return nil
}

func rejectDuplicateJSONObjectCookieFields(decoder *json.Decoder, token json.Token, fields map[string]struct{}) error {
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' {
		return discardJSONValue(decoder, token)
	}
	seen := map[string]struct{}{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("tracker auth: JSON cookie object has non-string key")
		}
		name := strings.TrimSpace(key)
		if _, relevant := fields[name]; relevant {
			if _, ok := seen[name]; ok {
				return fmt.Errorf("tracker auth: duplicate cookie name %q", name)
			}
			seen[name] = struct{}{}
		}
		valueToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
		}
		if err := discardJSONValue(decoder, valueToken); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
	}
	return nil
}

func discardJSONValue(decoder *json.Decoder, token json.Token) error {
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		for decoder.More() {
			if _, err := decoder.Token(); err != nil {
				return fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
			}
			valueToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
			}
			if err := discardJSONValue(decoder, valueToken); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			valueToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
			}
			if err := discardJSONValue(decoder, valueToken); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
	}
	return nil
}

func addJSONCookieObject(out map[string]string, decoded map[string]any) error {
	for key, value := range decoded {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		switch typed := value.(type) {
		case string:
			if err := addCookieValue(out, name, typed); err != nil {
				return err
			}
		case map[string]any:
			if raw, ok := typed["value"]; ok && raw != nil {
				if err := addCookieValue(out, name, fmt.Sprint(raw)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func addJSONCookieArrayEntry(out map[string]string, entry map[string]any) error {
	name, hasName := jsonCookieNameField(entry, "name")
	value, hasValue := jsonCookieValueField(entry, "value")
	if !hasName || !hasValue {
		return errors.New("tracker auth: JSON cookie array entries require name and value")
	}
	return addCookieValue(out, name, value)
}

func jsonCookieNameField(entry map[string]any, key string) (string, bool) {
	raw, ok := entry[key]
	if !ok || raw == nil {
		return "", false
	}
	return strings.TrimSpace(fmt.Sprint(raw)), true
}

func jsonCookieValueField(entry map[string]any, key string) (string, bool) {
	raw, ok := entry[key]
	if !ok || raw == nil {
		return "", false
	}
	return fmt.Sprint(raw), true
}

func addCookieValue(out map[string]string, name string, value string) error {
	name = strings.TrimSpace(name)
	if name != "" && value != "" {
		if _, ok := out[name]; ok {
			return fmt.Errorf("tracker auth: duplicate cookie name %q", name)
		}
		out[name] = value
	}
	return nil
}

// parseNetscapeCookieContent extracts cookie name/value pairs from Netscape
// cookie lines without scanner token-size limits. Names are trimmed; values are
// preserved after the value column, including surrounding spaces and tabs.
func parseNetscapeCookieContent(content string) (map[string]string, error) {
	out := make(map[string]string)
	for rawLine := range strings.SplitSeq(content, "\n") {
		line := strings.TrimSuffix(rawLine, "\r")
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}
		if strings.HasPrefix(trimmedLine, "#HttpOnly_") {
			line = line[strings.Index(line, "#HttpOnly_")+len("#HttpOnly_"):]
		} else if strings.HasPrefix(trimmedLine, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		name := strings.TrimSpace(fields[5])
		value := strings.Join(fields[6:], "\t")
		if name != "" && value != "" {
			if _, ok := out[name]; ok {
				return nil, fmt.Errorf("tracker auth: duplicate cookie name %q", name)
			}
			out[name] = value
		}
	}
	if len(out) == 0 {
		return nil, errors.New("tracker auth: cookie content has no entries")
	}
	return out, nil
}

var statusURLRe = regexp.MustCompile(`https?://[^\s"'>)]+`)

func redact(value string) string {
	redacted := redaction.RedactValue(value, nil)
	redacted = statusURLRe.ReplaceAllStringFunc(redacted, func(raw string) string {
		trimmed := strings.TrimRight(raw, ".,;:")
		suffix := strings.TrimPrefix(raw, trimmed)
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return raw
		}
		if parsed.EscapedPath() == "" && parsed.RawQuery == "" {
			return raw
		}
		return parsed.Scheme + "://" + parsed.Host + "/[REDACTED]" + suffix
	})
	return strings.TrimSpace(redacted)
}

func mustTrackerConfig(cfg config.Config, trackerID string) config.TrackerConfig {
	trackerCfg, _ := trackerConfig(cfg, trackerID)
	return trackerCfg
}

// applyEnsureErrorToStatus maps typed ensure errors onto API status. Confirmed
// invalid sessions clear the reported cookie count and include recovery action;
// 2FA is actionable only when a reusable challenge id exists.
func applyEnsureErrorToStatus(status *api.TrackerAuthStatus, err error) {
	status.LastError = redact(err.Error())
	status.Message = "remote auth test failed"

	if isCookieStorageFailure(err) {
		status.State = StateEncryptedStorageUnavailable
		status.Message = "cookie storage unavailable; see error details"
		return
	}

	var authRequired *AuthRequiredError
	if errors.As(err, &authRequired) {
		status.State = StateLoginRequired
		if validation, ok := asValidationError(err); ok && validation.ConfirmedInvalid {
			status.CookieCount = 0
			status.Message = expiredSessionActionMessage
		} else {
			status.Message = "login credentials or imported cookies required"
		}
		return
	}

	var needs2FA *Needs2FAError
	if errors.As(err, &needs2FA) {
		challengeID := strings.TrimSpace(needs2FA.ChallengeID)
		if challengeID == "" {
			status.State = StateLoginRequired
			status.Needs2FA = false
			status.ChallengeID = ""
			status.Message = "2FA required but no manual challenge is available"
			return
		}
		status.Needs2FA = true
		status.ChallengeID = challengeID
		status.State = StateLoginRequired
		status.Message = "2FA required"
		return
	}

	var unsupported *UnsupportedAuthError
	if errors.As(err, &unsupported) {
		status.Message = "remote auth validation is not supported"
		return
	}

	var validation *ValidationError
	if errors.As(err, &validation) && validation.ConfirmedInvalid {
		status.State = StateLoginRequired
		status.CookieCount = 0
		status.Message = expiredSessionActionMessage
	}
}

// isCookieStorageFailure recognizes cookie persistence/load failures that should
// be exposed as storage-unavailable rather than login-required auth states.
func isCookieStorageFailure(err error) bool {
	if err == nil || errors.Is(err, cookies.ErrTrackerCookiesNotFound) {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "cookies:") ||
		strings.Contains(lower, "cookie store") ||
		strings.Contains(lower, "legacy cookie file")
}

// CookiesToMap converts HTTP cookies to a name/value map, ignoring nil cookies
// and blank names or values while preserving cookie value bytes.
func CookiesToMap(values []*http.Cookie) map[string]string {
	out := make(map[string]string)
	for _, cookie := range values {
		if cookie == nil {
			continue
		}
		name := strings.TrimSpace(cookie.Name)
		value := cookie.Value
		if name != "" && value != "" {
			out[name] = value
		}
	}
	return out
}

// ReadCookieImportContent reads at most [MaxCookieImportContentBytes] from r and
// returns the shared parser cap error before allocating a larger payload.
func ReadCookieImportContent(r io.Reader) (string, error) {
	if r == nil {
		return "", errors.New("tracker auth: cookie file content is required")
	}
	limited := io.LimitReader(r, MaxCookieImportContentBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("tracker auth: read cookie file content: %w", err)
	}
	if len(payload) > MaxCookieImportContentBytes {
		return "", fmt.Errorf("tracker auth: cookie file content exceeds %d byte limit", MaxCookieImportContentBytes)
	}
	return string(payload), nil
}

// trackerAuthStateSnapshot keeps the AR auth material needed to roll back a
// partial delete after tracker cookie cleanup reports failure.
type trackerAuthStateSnapshot struct {
	dbPath          string
	trackerID       string
	authKeyValue    string
	hadAuthKey      bool
	legacyAuthPath  string
	legacyAuthValue []byte
	hadLegacyAuth   bool
}

// snapshotTrackerAuthState captures AR encrypted and legacy auth keys before a
// destructive delete; non-AR trackers have no tracker-specific auth state here.
func snapshotTrackerAuthState(ctx context.Context, dbPath string, trackerID string) (trackerAuthStateSnapshot, error) {
	snapshot := trackerAuthStateSnapshot{dbPath: dbPath, trackerID: trackerID}
	if !strings.EqualFold(strings.TrimSpace(trackerID), "AR") {
		return snapshot, nil
	}
	authKey, err := LoadAuthState(ctx, dbPath, "AR", "auth_key")
	if err == nil {
		snapshot.authKeyValue = authKey
		snapshot.hadAuthKey = true
	} else if !errors.Is(err, ErrAuthStateNotFound) && encryptedAuthStateMayExist(dbPath) {
		return snapshot, fmt.Errorf("tracker auth: snapshot AR auth state: %w", err)
	}
	if legacyPath, err := servicedb.CookiePath(dbPath, "AR_auth.txt"); err == nil {
		snapshot.legacyAuthPath = legacyPath
		legacyValue, readErr := os.ReadFile(legacyPath)
		if readErr == nil {
			snapshot.legacyAuthValue = legacyValue
			snapshot.hadLegacyAuth = true
		} else if !os.IsNotExist(readErr) {
			return snapshot, fmt.Errorf("tracker auth: snapshot AR legacy auth key: %w", readErr)
		}
	}
	return snapshot, nil
}

// restore writes captured AR auth material back using a rollback context so a
// canceled request cannot prevent best-effort restoration.
func (s trackerAuthStateSnapshot) restore(ctx context.Context) error {
	if !strings.EqualFold(strings.TrimSpace(s.trackerID), "AR") {
		return nil
	}
	rollbackCtx := contextWithoutCancel(ctx)
	var errs []error
	if s.hadAuthKey {
		if err := SaveAuthState(rollbackCtx, s.dbPath, "AR", "auth_key", s.authKeyValue); err != nil {
			errs = append(errs, fmt.Errorf("tracker auth: restore AR auth state: %w", err))
		}
	}
	if s.hadLegacyAuth && s.legacyAuthPath != "" {
		if err := os.MkdirAll(filepath.Dir(s.legacyAuthPath), 0o700); err != nil {
			errs = append(errs, fmt.Errorf("tracker auth: restore AR legacy auth key dir: %w", err))
		} else if err := os.WriteFile(s.legacyAuthPath, s.legacyAuthValue, 0o600); err != nil {
			errs = append(errs, fmt.Errorf("tracker auth: restore AR legacy auth key: %w", err))
		}
	}
	return errors.Join(errs...)
}

// contextWithoutCancel preserves context values for rollback work while
// detaching cancellation and deadline state.
func contextWithoutCancel(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

// deleteTrackerAuthState removes tracker-specific encrypted state in addition
// to generic cookie storage. AR also has a legacy plaintext auth key path, so
// deletion reports uncertain encrypted cleanup instead of silently leaving stale
// auth material when helper state cannot prove absence.
func deleteTrackerAuthState(ctx context.Context, dbPath string, trackerID string) error {
	if !strings.EqualFold(strings.TrimSpace(trackerID), "AR") {
		return nil
	}
	var errs []error
	if err := DeleteAuthState(ctx, dbPath, "AR", "auth_key"); err != nil && encryptedAuthStateMayExist(dbPath) {
		errs = append(errs, fmt.Errorf("tracker auth: delete AR auth state: %w", err))
	} else if err != nil && !errors.Is(err, ErrAuthStateNotFound) {
		errs = append(errs, fmt.Errorf("tracker auth: delete AR auth state uncertain: %w", err))
	}
	if legacyPath, err := servicedb.CookiePath(dbPath, "AR_auth.txt"); err == nil {
		if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("tracker auth: delete AR legacy auth key: %w", err))
		}
	} else {
		errs = append(errs, fmt.Errorf("tracker auth: resolve AR legacy auth key path: %w", err))
	}
	return errors.Join(errs...)
}

func encryptedAuthStateMayExist(dbPath string) bool {
	if strings.TrimSpace(dbPath) == "" {
		return false
	}
	_, err := authmaterial.LoadFromDBPath(dbPath)
	return err == nil
}
