// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackerauth

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/pkg/api"
)

const (
	StateConfigured                  = "configured"
	StateHasCookies                  = "has_cookies"
	StateNotConfigured               = "not_configured"
	StateLoginRequired               = "login_required"
	StateEncryptedStorageUnavailable = "encrypted_storage_unavailable"
)

type Service struct {
	cfg        config.Config
	adapters   map[string]Adapter
	challenges *ChallengeManager
}

type trackerSpec struct {
	id               string
	authKind         string
	cookies          bool
	login            bool
	autoLogin        bool
	totp             bool
	manual2FA        bool
	apiKey           bool
	passkey          bool
	needsCredentials bool
	notes            []string
}

func NewService(cfg config.Config) *Service {
	return &Service{
		cfg:        cfg,
		adapters:   defaultAdapters(),
		challenges: NewChallengeManager(defaultChallengeTTL),
	}
}

func (s *Service) Capabilities(_ context.Context) ([]api.TrackerAuthCapability, error) {
	specs := s.specs()
	out := make([]api.TrackerAuthCapability, 0, len(specs))
	for _, spec := range specs {
		out = append(out, capabilityFromSpec(spec))
	}
	return out, nil
}

func (s *Service) Status(ctx context.Context, trackerID string) (api.TrackerAuthStatus, error) {
	spec, err := s.specFor(trackerID)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	return s.statusForSpec(ctx, spec)
}

func (s *Service) ImportCookies(ctx context.Context, trackerID string, fileName string, content string) (api.TrackerAuthStatus, error) {
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
	status, err := s.statusForSpec(ctx, spec)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	status.State = StateHasCookies
	status.CookieCount = len(values)
	status.Message = "cookies imported"
	return status, nil
}

func (s *Service) Validate(ctx context.Context, trackerID string) (api.TrackerAuthStatus, error) {
	spec, err := s.specFor(trackerID)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	status, err := s.statusForSpec(ctx, spec)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}

	if _, ok := s.adapterFor(spec.id); ok {
		session, ensureErr := s.EnsureSession(ctx, EnsureRequest{
			TrackerID: spec.id,
			Config:    mustTrackerConfig(s.cfg, spec.id),
			DBPath:    s.cfg.MainSettings.DBPath,
			AutoLogin: true,
		})
		if ensureErr != nil {
			applyEnsureErrorToStatus(&status, ensureErr)
			if session.ChallengeID != "" {
				status.ChallengeID = session.ChallengeID
			}
			return status, nil
		}
		status.State = StateConfigured
		status.Message = "remote auth check succeeded"
		return status, nil
	}
	status.Message = validationMessage(spec, status)
	return status, nil
}

func (s *Service) Login(ctx context.Context, trackerID string, req api.TrackerAuthLoginRequest) (api.TrackerAuthStatus, error) {
	spec, err := s.specFor(trackerID)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	if !spec.login {
		return api.TrackerAuthStatus{}, fmt.Errorf("tracker auth: %s does not support credential login", spec.id)
	}
	status, err := s.statusForSpec(ctx, spec)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	if status.State == StateLoginRequired {
		status.Message = "username/password missing"
		return status, nil
	}
	if status.Needs2FA {
		status.Message = "manual 2FA is supported only during an active tracker login challenge"
		return status, nil
	}

	if _, ok := s.adapterFor(spec.id); ok {
		session, ensureErr := s.EnsureSession(ctx, EnsureRequest{
			TrackerID: spec.id,
			Config:    mustTrackerConfig(s.cfg, spec.id),
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
		status.State = StateConfigured
		status.Message = "remote login/auth check succeeded"
		return status, nil
	}
	status.State = StateConfigured
	status.Message = "remote login/auth check succeeded"
	return status, nil
}

func (s *Service) Submit2FA(ctx context.Context, challengeID string, code string) (api.TrackerAuthStatus, error) {
	if strings.TrimSpace(challengeID) == "" {
		return api.TrackerAuthStatus{}, errors.New("tracker auth: challenge id is required")
	}
	if strings.TrimSpace(code) == "" {
		return api.TrackerAuthStatus{}, errors.New("tracker auth: 2FA code is required")
	}
	if s.challenges == nil {
		s.challenges = NewChallengeManager(defaultChallengeTTL)
	}
	challenge, ok := s.challenges.Get(challengeID)
	if !ok {
		return api.TrackerAuthStatus{}, errors.New("tracker auth: no active manual 2FA challenge")
	}
	adapter, ok := s.adapterFor(challenge.TrackerID)
	if !ok {
		return api.TrackerAuthStatus{}, newUnknownTrackerError(challenge.TrackerID)
	}
	session, err := adapter.Submit2FA(ctx, challengeID, code)
	if err != nil {
		status, statusErr := s.Status(ctx, challenge.TrackerID)
		if statusErr != nil {
			return api.TrackerAuthStatus{}, statusErr
		}
		applyEnsureErrorToStatus(&status, err)
		status.ChallengeID = challengeID
		return status, nil
	}
	if _, err := s.challenges.Consume(challengeID, challenge.TrackerID); err != nil {
		return api.TrackerAuthStatus{}, err
	}
	status, err := s.Status(ctx, challenge.TrackerID)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	if strings.TrimSpace(session.State) == "" || session.State == SessionStateReady {
		status.State = StateConfigured
	}
	status.Needs2FA = false
	status.ChallengeID = ""
	status.Message = "2FA auth completed"
	return status, nil
}

func (s *Service) Delete(ctx context.Context, trackerID string) (api.TrackerAuthStatus, error) {
	spec, err := s.specFor(trackerID)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	if err := cookies.DeleteTrackerCookies(ctx, s.cfg.MainSettings.DBPath, spec.id); err != nil {
		return api.TrackerAuthStatus{}, fmt.Errorf("tracker auth: delete %s cookies: %w", spec.id, err)
	}
	status, err := s.statusForSpec(ctx, spec)
	if err != nil {
		return api.TrackerAuthStatus{}, err
	}
	status.CookieCount = 0
	status.Message = "stored cookies deleted"
	return status, nil
}

func (s *Service) statusForSpec(ctx context.Context, spec trackerSpec) (api.TrackerAuthStatus, error) {
	encryptedStorage := s.encryptedStorageAvailable()
	status := api.TrackerAuthStatus{
		TrackerID:        spec.id,
		DisplayName:      spec.id,
		State:            StateNotConfigured,
		LastCheckedAt:    time.Now().UTC().Format(time.RFC3339),
		EncryptedStorage: encryptedStorage,
	}

	cfg, hasCfg := trackerConfig(s.cfg, spec.id)
	if spec.cookies {
		values, err := cookies.LoadTrackerCookieMap(ctx, s.cfg.MainSettings.DBPath, spec.id)
		if err == nil && len(values) > 0 {
			status.CookieCount = len(values)
			status.State = StateHasCookies
		} else if err != nil && !strings.Contains(strings.ToLower(err.Error()), "no cookies found") {
			status.LastError = redact(err.Error())
		}
	}

	if spec.apiKey && configAPIKey(cfg) != "" {
		status.State = StateConfigured
	}
	if spec.passkey && strings.TrimSpace(cfg.Passkey) != "" {
		status.State = StateConfigured
	}
	if spec.login {
		hasCredentials := hasCfg && strings.TrimSpace(cfg.Username) != "" && strings.TrimSpace(cfg.Password) != ""
		if hasCredentials {
			if status.State == StateNotConfigured {
				status.State = StateConfigured
			}
			if spec.totp && strings.TrimSpace(cfg.OTPURI) == "" && spec.manual2FA {
				status.Needs2FA = true
			}
		} else if status.CookieCount == 0 && spec.needsCredentials {
			status.State = StateLoginRequired
		}
	}
	if spec.cookies && status.CookieCount == 0 && !encryptedStorage {
		status.State = StateEncryptedStorageUnavailable
	}
	status.Message = validationMessage(spec, status)
	return status, nil
}

func (s *Service) encryptedStorageAvailable() bool {
	if strings.TrimSpace(s.cfg.MainSettings.DBPath) == "" {
		return false
	}
	_, err := authmaterial.LoadFromDBPath(s.cfg.MainSettings.DBPath)
	return err == nil
}

func (s *Service) specFor(trackerID string) (trackerSpec, error) {
	needle := strings.TrimSpace(trackerID)
	if needle == "" {
		return trackerSpec{}, errors.New("tracker auth: tracker id is required")
	}
	for _, spec := range s.specs() {
		if strings.EqualFold(spec.id, needle) {
			return spec, nil
		}
	}
	return trackerSpec{}, fmt.Errorf("tracker auth: unknown tracker %s", needle)
}

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
		upperID := strings.ToUpper(trimmedID)
		spec, ok := index[upperID]
		if !ok {
			spec = trackerSpec{id: upperID, authKind: "config"}
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
			spec.login = true
			spec.autoLogin = spec.autoLogin || spec.id == "AR" || spec.id == "FL" || spec.id == "MTV" || spec.id == "PTP" || spec.id == "THR" || spec.id == "RTF"
			if spec.authKind == "config" {
				spec.authKind = "credential_login"
			}
		}
		index[upperID] = spec
	}

	out := make([]trackerSpec, 0, len(index))
	for _, spec := range index {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func builtInSpecs() []trackerSpec {
	return []trackerSpec{
		{id: "AR", authKind: "cookies_login", cookies: true, login: true, autoLogin: true, needsCredentials: true},
		{id: "FL", authKind: "cookies_login", cookies: true, login: true, autoLogin: true, needsCredentials: true},
		{id: "MTV", authKind: "api_key_cookies_login", cookies: true, login: true, autoLogin: true, totp: true, manual2FA: true, apiKey: true, needsCredentials: true, notes: []string{"API key covers Torznab/search; cookies/login cover upload authkey."}},
		{id: "PTP", authKind: "cookies_login", cookies: true, login: true, autoLogin: true, totp: true, manual2FA: true, needsCredentials: true},
		{id: "THR", authKind: "credential_login", cookies: true, login: true, autoLogin: true, needsCredentials: true},
		{id: "RTF", authKind: "api_key_credential_refresh", login: true, autoLogin: true, apiKey: true, needsCredentials: false},
		{id: "ASC", authKind: "cookies", cookies: true},
		{id: "AZ", authKind: "cookies", cookies: true},
		{id: "BJS", authKind: "cookies", cookies: true},
		{id: "BT", authKind: "cookies", cookies: true},
		{id: "CZ", authKind: "cookies", cookies: true},
		{id: "HDB", authKind: "passkey_cookies", cookies: true, passkey: true},
		{id: "HDS", authKind: "cookies", cookies: true},
		{id: "HDT", authKind: "cookies", cookies: true},
		{id: "IS", authKind: "cookies", cookies: true},
		{id: "PHD", authKind: "cookies", cookies: true},
		{id: "PTS", authKind: "cookies", cookies: true},
		{id: "PTER", authKind: "cookies", cookies: true},
		{id: "TL", authKind: "cookies", cookies: true},
		{id: "TTG", authKind: "cookies_manual_2fa", cookies: true, manual2FA: true},
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

func trackerConfig(cfg config.Config, trackerID string) (config.TrackerConfig, bool) {
	for key, value := range cfg.Trackers.Trackers {
		if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(trackerID)) {
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

func ParseCookieContent(fileName string, content string) (map[string]string, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, errors.New("tracker auth: cookie file content is required")
	}
	if strings.EqualFold(filepath.Ext(strings.TrimSpace(fileName)), ".json") || strings.HasPrefix(trimmed, "{") {
		return parseJSONCookieContent([]byte(trimmed))
	}
	return parseNetscapeCookieContent(trimmed)
}

func parseJSONCookieContent(payload []byte) (map[string]string, error) {
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("tracker auth: parse JSON cookies: %w", err)
	}
	out := make(map[string]string)
	for key, value := range decoded {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				out[name] = trimmed
			}
		case map[string]any:
			if raw, ok := typed["value"]; ok {
				if trimmed := strings.TrimSpace(fmt.Sprint(raw)); trimmed != "" {
					out[name] = trimmed
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("tracker auth: cookie content has no entries")
	}
	return out, nil
}

func parseNetscapeCookieContent(content string) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#HttpOnly_") {
			line = strings.TrimPrefix(line, "#HttpOnly_")
		} else if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		name := strings.TrimSpace(fields[5])
		value := strings.TrimSpace(strings.Join(fields[6:], "\t"))
		if name != "" && value != "" {
			out[name] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("tracker auth: scan Netscape cookies: %w", err)
	}
	if len(out) == 0 {
		return nil, errors.New("tracker auth: cookie content has no entries")
	}
	return out, nil
}

func redact(value string) string {
	return strings.TrimSpace(redaction.RedactValue(value, nil))
}

func mustTrackerConfig(cfg config.Config, trackerID string) config.TrackerConfig {
	trackerCfg, _ := trackerConfig(cfg, trackerID)
	return trackerCfg
}

func applyEnsureErrorToStatus(status *api.TrackerAuthStatus, err error) {
	status.LastError = redact(err.Error())
	status.Message = "remote auth test failed"

	var authRequired *AuthRequiredError
	if errors.As(err, &authRequired) {
		status.State = StateLoginRequired
		status.Message = "login credentials or imported cookies required"
		return
	}

	var needs2FA *Needs2FAError
	if errors.As(err, &needs2FA) {
		status.Needs2FA = true
		status.ChallengeID = strings.TrimSpace(needs2FA.ChallengeID)
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
		status.Message = "stored session expired or invalid"
	}
}

func CookiesToMap(values []*http.Cookie) map[string]string {
	out := make(map[string]string)
	for _, cookie := range values {
		if cookie == nil {
			continue
		}
		name := strings.TrimSpace(cookie.Name)
		value := strings.TrimSpace(cookie.Value)
		if name != "" && value != "" {
			out[name] = value
		}
	}
	return out
}
