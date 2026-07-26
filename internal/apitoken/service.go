// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package apitoken owns persistent bearer-token generation, validation, and authentication.
package apitoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
)

const (
	minimumTokenLength = 24
	maximumNameLength  = 100
	maximumOwnerLength = 100
	tokenIDPrefix      = "tok_"
)

// Scope is one independently grantable public API permission.
type Scope string

const (
	// ScopeWorkflowRead permits immutable workflow and operation reads.
	ScopeWorkflowRead Scope = "workflow:read"
	// ScopeWorkflowWrite permits preparation and non-execution commands.
	ScopeWorkflowWrite Scope = "workflow:write"
	// ScopeWorkflowExecute permits consuming reviewed upload plans.
	ScopeWorkflowExecute Scope = "workflow:execute"
)

var allScopes = []Scope{
	ScopeWorkflowRead,
	ScopeWorkflowWrite,
	ScopeWorkflowExecute,
}

var (
	// ErrInvalid indicates invalid operator-supplied token metadata.
	ErrInvalid = errors.New("invalid API token")
	// ErrNotFound indicates that an active token record does not exist.
	ErrNotFound = errors.New("api token not found")
	// ErrRepositoryRequired indicates incomplete service composition.
	ErrRepositoryRequired = errors.New("api token repository is required")
)

// Record is safe API-token metadata. It never contains the token or its hash.
type Record struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	OwnerID   string     `json:"ownerId"`
	Scopes    []Scope    `json:"scopes"`
	CreatedAt time.Time  `json:"createdAt"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}

// StoredRecord is the persistence representation. TokenHash must never be serialized or logged.
type StoredRecord struct {
	Record
	TokenHash [sha256.Size]byte
}

// CreateInput contains operator-selected metadata for one new token.
type CreateInput struct {
	Name    string
	OwnerID string
	Scopes  []Scope
}

// Created contains safe metadata plus the one-time plaintext token.
type Created struct {
	Record Record `json:"record"`
	Token  string `json:"token"`
}

// Principal is the owner and permission set established by authentication.
type Principal struct {
	OwnerID string
	Scopes  []Scope
}

// Repository persists token hashes and safe metadata.
type Repository interface {
	CreateAPIToken(context.Context, StoredRecord) error
	ListAPITokens(context.Context) ([]Record, error)
	FindActiveAPITokenByHash(context.Context, [sha256.Size]byte) (Record, error)
	RevokeAPIToken(context.Context, string, time.Time) (bool, error)
}

// Service owns token generation and authentication policy.
type Service struct {
	repository Repository
}

type persistenceError struct {
	cause error
}

func (e *persistenceError) Error() string {
	return "persist API credential metadata failed"
}

func (e *persistenceError) Unwrap() error {
	return e.cause
}

// NewService constructs a persistent token service.
func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, ErrRepositoryRequired
	}
	return &Service{repository: repository}, nil
}

// AllScopes returns the supported scopes in stable display order.
func AllScopes() []Scope {
	return append([]Scope(nil), allScopes...)
}

// NormalizeScopes validates, deduplicates, and canonically orders scopes.
// An empty selection grants all currently supported scopes.
func NormalizeScopes(scopes []Scope) ([]Scope, error) {
	if len(scopes) == 0 {
		return AllScopes(), nil
	}
	wanted := make(map[Scope]struct{}, len(scopes))
	for _, scope := range scopes {
		scope = Scope(strings.TrimSpace(string(scope)))
		if !slices.Contains(allScopes, scope) {
			return nil, fmt.Errorf("unsupported API token scope %q", scope)
		}
		wanted[scope] = struct{}{}
	}
	normalized := make([]Scope, 0, len(wanted))
	for _, scope := range allScopes {
		if _, ok := wanted[scope]; ok {
			normalized = append(normalized, scope)
		}
	}
	return normalized, nil
}

// Create generates and persists one token. Plaintext is returned only from this call.
func (s *Service) Create(ctx context.Context, input CreateInput) (Created, error) {
	if s == nil || s.repository == nil {
		return Created{}, ErrRepositoryRequired
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Created{}, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if len(name) > maximumNameLength {
		return Created{}, fmt.Errorf("%w: name exceeds %d characters", ErrInvalid, maximumNameLength)
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return Created{}, fmt.Errorf("%w: name contains control characters", ErrInvalid)
	}
	ownerID := strings.TrimSpace(input.OwnerID)
	if ownerID == "" {
		ownerID = "default"
	}
	if len(ownerID) > maximumOwnerLength {
		return Created{}, fmt.Errorf("%w: owner exceeds %d characters", ErrInvalid, maximumOwnerLength)
	}
	if strings.IndexFunc(ownerID, unicode.IsControl) >= 0 {
		return Created{}, fmt.Errorf("%w: owner contains control characters", ErrInvalid)
	}
	scopes, err := NormalizeScopes(input.Scopes)
	if err != nil {
		return Created{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	idSuffix, err := randomValue(12)
	if err != nil {
		return Created{}, fmt.Errorf("generate API token id: %w", err)
	}
	id := tokenIDPrefix + idSuffix
	secret, err := randomValue(32)
	if err != nil {
		return Created{}, fmt.Errorf("generate API token: %w", err)
	}
	token := "upbrr_api_" + secret
	record := Record{
		ID:        id,
		Name:      name,
		OwnerID:   ownerID,
		Scopes:    scopes,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.repository.CreateAPIToken(ctx, StoredRecord{
		Record:    record,
		TokenHash: sha256.Sum256([]byte(token)),
	}); err != nil {
		return Created{}, &persistenceError{cause: err}
	}
	return Created{Record: record, Token: token}, nil
}

// List returns token metadata, including revoked records, newest first.
func (s *Service) List(ctx context.Context) ([]Record, error) {
	if s == nil || s.repository == nil {
		return nil, ErrRepositoryRequired
	}
	records, err := s.repository.ListAPITokens(ctx)
	if err != nil {
		return nil, fmt.Errorf("list API tokens: %w", err)
	}
	if records == nil {
		return []Record{}, nil
	}
	return records, nil
}

// Revoke permanently disables one active token without deleting its audit metadata.
func (s *Service) Revoke(ctx context.Context, id string) error {
	if s == nil || s.repository == nil {
		return ErrRepositoryRequired
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: id is required", ErrInvalid)
	}
	revoked, err := s.repository.RevokeAPIToken(ctx, id, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("revoke API token: %w", err)
	}
	if !revoked {
		return ErrNotFound
	}
	return nil
}

// Authenticate validates a plaintext token against active persisted hashes.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, bool, error) {
	if s == nil || s.repository == nil {
		return Principal{}, false, ErrRepositoryRequired
	}
	token = strings.TrimSpace(token)
	if len(token) < minimumTokenLength {
		return Principal{}, false, nil
	}
	record, err := s.repository.FindActiveAPITokenByHash(ctx, sha256.Sum256([]byte(token)))
	if errors.Is(err, ErrNotFound) {
		return Principal{}, false, nil
	}
	if err != nil {
		return Principal{}, false, fmt.Errorf("authenticate API token: %w", err)
	}
	scopes, err := NormalizeScopes(record.Scopes)
	if err != nil {
		return Principal{}, false, fmt.Errorf("authenticate API token metadata: %w", err)
	}
	return Principal{OwnerID: "api:" + record.OwnerID, Scopes: scopes}, true, nil
}

func randomValue(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("read secure random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
