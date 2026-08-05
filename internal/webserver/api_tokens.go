// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/apitoken"
)

// APITokenScope is one independently grantable third-party API permission.
type APITokenScope = apitoken.Scope

const (
	// APITokenScopeWorkflowRead permits immutable workflow and operation reads.
	APITokenScopeWorkflowRead = apitoken.ScopeWorkflowRead
	// APITokenScopeWorkflowWrite permits preparation and non-execution commands.
	APITokenScopeWorkflowWrite = apitoken.ScopeWorkflowWrite
	// APITokenScopeWorkflowExecute permits consuming reviewed upload plans.
	APITokenScopeWorkflowExecute = apitoken.ScopeWorkflowExecute
)

// APITokenCredential configures one runtime-only bearer credential. Token is
// hashed during server construction and is never retained or serialized.
type APITokenCredential struct {
	Token   string
	OwnerID string
	Scopes  []APITokenScope
}

type apiTokenRecord struct {
	hash    [sha256.Size]byte
	ownerID string
	scopes  []APITokenScope
}

type apiTokenStore struct {
	records    []apiTokenRecord
	persistent *apitoken.Service
}

type apiPrincipal struct {
	OwnerID string
	Scopes  []APITokenScope
}

func newAPITokenStore(credentials []APITokenCredential, repositories ...apitoken.Repository) (*apiTokenStore, error) {
	if len(repositories) > 1 {
		return nil, errors.New("webserver: only one API token repository is supported")
	}
	store := &apiTokenStore{records: make([]apiTokenRecord, 0, len(credentials))}
	if len(repositories) == 1 && repositories[0] != nil {
		service, err := apitoken.NewService(repositories[0])
		if err != nil {
			return nil, fmt.Errorf("webserver: API tokens: %w", err)
		}
		store.persistent = service
	}
	seen := make(map[[sha256.Size]byte]struct{}, len(credentials))
	for index, credential := range credentials {
		token := strings.TrimSpace(credential.Token)
		ownerID := strings.TrimSpace(credential.OwnerID)
		if len(token) < 24 {
			return nil, fmt.Errorf("webserver: API token %d must contain at least 24 characters", index+1)
		}
		if ownerID == "" {
			return nil, fmt.Errorf("webserver: API token %d owner is required", index+1)
		}
		hash := sha256.Sum256([]byte(token))
		if _, ok := seen[hash]; ok {
			return nil, errors.New("webserver: duplicate API token")
		}
		seen[hash] = struct{}{}
		scopes, err := normalizeAPITokenScopes(credential.Scopes)
		if err != nil {
			return nil, fmt.Errorf("webserver: API token %d: %w", index+1, err)
		}
		store.records = append(store.records, apiTokenRecord{
			hash:    hash,
			ownerID: "api:" + ownerID,
			scopes:  scopes,
		})
	}
	return store, nil
}

func normalizeAPITokenScopes(scopes []APITokenScope) ([]APITokenScope, error) {
	normalized, err := apitoken.NormalizeScopes(scopes)
	if err != nil {
		return nil, fmt.Errorf("normalize API token scopes: %w", err)
	}
	return normalized, nil
}

func (s *apiTokenStore) authenticate(token string) (apiPrincipal, bool) {
	if s == nil || strings.TrimSpace(token) == "" {
		return apiPrincipal{}, false
	}
	candidate := sha256.Sum256([]byte(strings.TrimSpace(token)))
	for _, record := range s.records {
		if subtle.ConstantTimeCompare(candidate[:], record.hash[:]) == 1 {
			return apiPrincipal{OwnerID: record.ownerID, Scopes: append([]APITokenScope(nil), record.scopes...)}, true
		}
	}
	return apiPrincipal{}, false
}

func (s *apiTokenStore) authenticateContext(ctx context.Context, token string) (apiPrincipal, bool, error) {
	if principal, ok := s.authenticate(token); ok {
		return principal, true, nil
	}
	if s == nil || s.persistent == nil {
		return apiPrincipal{}, false, nil
	}
	principal, ok, err := s.persistent.Authenticate(ctx, token)
	if err != nil {
		return apiPrincipal{}, false, fmt.Errorf("authenticate persistent API credential: %w", err)
	}
	return apiPrincipal{OwnerID: principal.OwnerID, Scopes: principal.Scopes}, ok, nil
}

func (s *apiTokenStore) create(ctx context.Context, input apitoken.CreateInput) (apitoken.Created, error) {
	if s == nil || s.persistent == nil {
		return apitoken.Created{}, apitoken.ErrRepositoryRequired
	}
	created, err := s.persistent.Create(ctx, input)
	if err != nil {
		return apitoken.Created{}, fmt.Errorf("create persistent API credential: %w", err)
	}
	return created, nil
}

func (s *apiTokenStore) list(ctx context.Context) ([]apitoken.Record, error) {
	if s == nil || s.persistent == nil {
		return nil, apitoken.ErrRepositoryRequired
	}
	records, err := s.persistent.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list persistent API credentials: %w", err)
	}
	return records, nil
}

func (s *apiTokenStore) revoke(ctx context.Context, id string) error {
	if s == nil || s.persistent == nil {
		return apitoken.ErrRepositoryRequired
	}
	if err := s.persistent.Revoke(ctx, id); err != nil {
		return fmt.Errorf("revoke persistent API credential: %w", err)
	}
	return nil
}

func bearerToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func (s *Server) authenticateAPIRequest(
	w http.ResponseWriter,
	r *http.Request,
	scope APITokenScope,
) (apiPrincipal, bool) {
	if !s.allowGeneralRequest(r) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return apiPrincipal{}, false
	}
	principal, ok, err := s.apiTokens.authenticateContext(r.Context(), bearerToken(r))
	if err != nil {
		if s.backend != nil {
			s.backend.logWarnf("web: API token authentication unavailable: %v", err)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "API token authentication unavailable"})
		return apiPrincipal{}, false
	}
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "API token required"})
		return apiPrincipal{}, false
	}
	if !slices.Contains(principal.Scopes, scope) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "API token scope denied"})
		return apiPrincipal{}, false
	}
	return principal, true
}

// ParseAPITokenEnvironment constructs one runtime credential from serve-mode
// environment values without retaining the plaintext outside server startup.
func ParseAPITokenEnvironment(token string, ownerID string, rawScopes string) ([]APITokenCredential, error) {
	if strings.TrimSpace(token) == "" {
		return nil, nil
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		ownerID = "default"
	}
	var scopes []APITokenScope
	for value := range strings.SplitSeq(rawScopes, ",") {
		if value = strings.TrimSpace(value); value != "" {
			scopes = append(scopes, APITokenScope(value))
		}
	}
	if _, err := normalizeAPITokenScopes(scopes); err != nil {
		return nil, err
	}
	return []APITokenCredential{{
		Token:   token,
		OwnerID: ownerID,
		Scopes:  scopes,
	}}, nil
}
