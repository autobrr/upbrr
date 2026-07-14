// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/autobrr/upbrr/internal/redaction"
)

const (
	// oidcStateCookieName binds a callback to the browser that started the
	// flow. Without it, an attacker could feed a victim their own callback URL.
	oidcStateCookieName = "ua_oidc_state"
	// oidcFlowTTL bounds how long an authorization request stays valid.
	oidcFlowTTL = 10 * time.Minute
	// oidcFlowLimit caps in-flight authorization requests so unfinished flows
	// cannot grow memory without bound.
	oidcFlowLimit = 64
)

// errOIDCDisabled is returned when an OIDC route is reached while OIDC is off.
var errOIDCDisabled = errors.New("oidc: not enabled")

// oidcFlow holds the per-request material that must survive the round trip to
// the provider and back.
type oidcFlow struct {
	Nonce        string
	CodeVerifier string
	ExpiresAt    time.Time
}

// oidcService owns provider discovery and the authorization code flow.
//
// Discovery is lazy and retried on demand rather than performed once at
// startup: the provider is a separate service that may boot after upbrr, or be
// briefly unreachable. A failed discovery must degrade the login page, not
// prevent the server from starting.
type oidcService struct {
	cfg  OIDCConfig
	logf func(string, ...any)

	mu       sync.Mutex
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauthCfg *oauth2.Config
	pkce     bool

	flowMu sync.Mutex
	flows  map[string]oidcFlow
}

// newOIDCService returns nil when OIDC is not enabled, so callers can treat a
// nil service as "feature off".
func newOIDCService(cfg OIDCConfig, logf func(string, ...any)) *oidcService {
	if !cfg.Enabled {
		return nil
	}
	return &oidcService{
		cfg:   cfg,
		logf:  logf,
		flows: make(map[string]oidcFlow),
	}
}

// Enabled reports whether OIDC login is configured.
func (s *oidcService) Enabled() bool {
	return s != nil && s.cfg.Enabled
}

// DisableBuiltInLogin reports whether password login must be refused.
func (s *oidcService) DisableBuiltInLogin() bool {
	return s.Enabled() && s.cfg.DisableBuiltInLogin
}

// ensureProvider performs discovery once and caches the result. It is safe to
// call on every request: after the first success it is a mutex and a nil check.
func (s *oidcService) ensureProvider(ctx context.Context) error {
	if !s.Enabled() {
		return errOIDCDisabled
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider != nil {
		return nil
	}

	provider, err := oidc.NewProvider(ctx, s.cfg.Issuer)
	if err != nil {
		return fmt.Errorf("oidc: discover issuer: %s", redaction.RedactValue(err.Error(), nil))
	}

	var claims struct {
		CodeChallengeMethods []string `json:"code_challenge_methods_supported"`
	}
	if err := provider.Claims(&claims); err != nil {
		return fmt.Errorf("oidc: read provider metadata: %s", redaction.RedactValue(err.Error(), nil))
	}

	s.provider = provider
	s.verifier = provider.Verifier(&oidc.Config{ClientID: s.cfg.ClientID})
	s.oauthCfg = &oauth2.Config{
		ClientID:     s.cfg.ClientID,
		ClientSecret: s.cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  s.cfg.RedirectURL,
		Scopes:       oidcScopeList(s.cfg.Scopes),
	}
	s.pkce = supportsPKCE(claims.CodeChallengeMethods)

	s.logf("web: oidc discovery succeeded issuer=%s pkce=%t", redaction.RedactValue(s.cfg.Issuer, nil), s.pkce)
	return nil
}

// oidcScopeList splits the configured scopes and guarantees "openid", without
// which the provider will not return an ID token.
func oidcScopeList(scopes string) []string {
	seen := make(map[string]struct{})
	list := make([]string, 0, 4)
	for _, scope := range strings.Fields(scopes) {
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		list = append(list, scope)
	}
	if _, ok := seen[oidc.ScopeOpenID]; !ok {
		list = append([]string{oidc.ScopeOpenID}, list...)
	}
	return list
}

func supportsPKCE(methods []string) bool {
	for _, method := range methods {
		if strings.EqualFold(strings.TrimSpace(method), "S256") {
			return true
		}
	}
	return false
}

// AuthCodeURL starts a flow: it mints state, nonce, and (when the provider
// advertises S256) a PKCE verifier, stores them, and returns the provider URL
// plus the state to bind to the browser via cookie.
func (s *oidcService) AuthCodeURL(ctx context.Context) (authURL string, state string, err error) {
	if err := s.ensureProvider(ctx); err != nil {
		return "", "", err
	}

	state, err = randomString(32)
	if err != nil {
		return "", "", err
	}
	nonce, err := randomString(32)
	if err != nil {
		return "", "", err
	}

	flow := oidcFlow{Nonce: nonce, ExpiresAt: time.Now().UTC().Add(oidcFlowTTL)}
	opts := []oauth2.AuthCodeOption{oidc.Nonce(nonce)}

	s.mu.Lock()
	usePKCE := s.pkce
	oauthCfg := s.oauthCfg
	s.mu.Unlock()

	if usePKCE {
		verifier := oauth2.GenerateVerifier()
		flow.CodeVerifier = verifier
		opts = append(opts, oauth2.S256ChallengeOption(verifier))
	}

	s.storeFlow(state, flow)
	return oauthCfg.AuthCodeURL(state, opts...), state, nil
}

// Exchange completes a flow: it consumes the stored state, exchanges the code,
// verifies the ID token, and checks the nonce. It returns the verified claims.
func (s *oidcService) Exchange(ctx context.Context, state string, code string) (*oidcClaims, error) {
	if err := s.ensureProvider(ctx); err != nil {
		return nil, err
	}

	flow, ok := s.consumeFlow(state)
	if !ok {
		return nil, errors.New("oidc: unknown or expired authorization state")
	}

	s.mu.Lock()
	oauthCfg := s.oauthCfg
	verifier := s.verifier
	s.mu.Unlock()

	opts := make([]oauth2.AuthCodeOption, 0, 1)
	if flow.CodeVerifier != "" {
		opts = append(opts, oauth2.VerifierOption(flow.CodeVerifier))
	}

	token, err := oauthCfg.Exchange(ctx, code, opts...)
	if err != nil {
		return nil, fmt.Errorf("oidc: exchange authorization code: %s", redaction.RedactValue(err.Error(), nil))
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || strings.TrimSpace(rawIDToken) == "" {
		return nil, errors.New("oidc: provider response did not include an id_token")
	}

	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("oidc: verify id token: %s", redaction.RedactValue(err.Error(), nil))
	}
	if idToken.Nonce != flow.Nonce {
		return nil, errors.New("oidc: id token nonce mismatch")
	}

	claims := &oidcClaims{}
	if err := idToken.Claims(claims); err != nil {
		return nil, fmt.Errorf("oidc: decode id token claims: %s", redaction.RedactValue(err.Error(), nil))
	}
	claims.Subject = idToken.Subject
	return claims, nil
}

// oidcClaims carries the identity claims upbrr uses to name the session.
type oidcClaims struct {
	Subject           string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	Email             string `json:"email"`
	Name              string `json:"name"`
}

// Username picks the most human-readable stable identifier available. Subject
// is the last resort because it is opaque, but it is always present.
func (c *oidcClaims) Username() string {
	for _, candidate := range []string{c.PreferredUsername, c.Email, c.Name, c.Subject} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (s *oidcService) storeFlow(state string, flow oidcFlow) {
	s.flowMu.Lock()
	defer s.flowMu.Unlock()

	now := time.Now().UTC()
	for key, existing := range s.flows {
		if now.After(existing.ExpiresAt) {
			delete(s.flows, key)
		}
	}
	// Abandoned flows (a user who opens the login page and never finishes)
	// would otherwise accumulate until their TTL expires.
	if len(s.flows) >= oidcFlowLimit {
		oldestKey := ""
		oldest := time.Time{}
		for key, existing := range s.flows {
			if oldestKey == "" || existing.ExpiresAt.Before(oldest) {
				oldestKey = key
				oldest = existing.ExpiresAt
			}
		}
		delete(s.flows, oldestKey)
	}
	s.flows[state] = flow
}

// consumeFlow returns the flow for state and removes it, so an authorization
// code cannot be replayed against the same state twice.
func (s *oidcService) consumeFlow(state string) (oidcFlow, bool) {
	trimmed := strings.TrimSpace(state)
	if trimmed == "" {
		return oidcFlow{}, false
	}

	s.flowMu.Lock()
	defer s.flowMu.Unlock()

	flow, ok := s.flows[trimmed]
	if !ok {
		return oidcFlow{}, false
	}
	delete(s.flows, trimmed)
	if time.Now().UTC().After(flow.ExpiresAt) {
		return oidcFlow{}, false
	}
	return flow, true
}
