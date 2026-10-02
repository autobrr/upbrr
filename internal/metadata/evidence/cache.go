// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package evidence retains source-bound remote query outcomes independently
// from the corrections and local scoring that consume them.
package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

// ErrSuppressed reports a previously completed failed query. Its original
// error is deliberately not persisted because provider errors can contain secrets.
var ErrSuppressed = errors.New("previous metadata lookup failed; remove the release from History to retry")

type contextKey struct{}

type queryState struct {
	gate chan struct{}
	seen bool
}

// Scope owns one collection pass. Store failures are retained so a best-effort
// provider cannot accidentally hide a failed persistence operation.
type Scope struct {
	store       api.MetadataEvidenceRepository
	source      string
	fingerprint string
	freshness   api.ExternalFreshness
	logger      api.Logger
	mu          sync.Mutex
	queries     map[string]*queryState
	err         error
}

// WithScope binds query reuse to the verified source, never to a correction
// revision. A missing store leaves direct/test clients' behavior unchanged.
func WithScope(
	ctx context.Context,
	store api.MetadataEvidenceRepository,
	source, fingerprint string,
	freshness api.ExternalFreshness,
	logger api.Logger,
) (context.Context, *Scope) {
	scope := &Scope{
		store:       store,
		source:      source,
		fingerprint: fingerprint,
		freshness:   freshness,
		logger:      logger,
		queries:     make(map[string]*queryState),
	}
	return context.WithValue(ctx, contextKey{}, scope), scope
}

// Err returns the first persistence failure observed by this collection pass.
func (s *Scope) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Scope) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

// Lookup replays a detached typed result for the same actual query. Empty
// results are eligible again on a fresh load; completed failures remain
// suppressed until source history is removed. Cancellation is never retained.
// query is hashed before persistence and must contain every lookup-affecting input.
func Lookup[T any](ctx context.Context, domain string, query any, fetch func() (T, error), empty func(T) bool) (T, error) {
	return lookup(ctx, domain, query, fetch, empty, false)
}

// Attempt retries a missing local artifact after a previously successful fetch,
// while retaining completed failures. Callers check artifact existence first.
func Attempt(ctx context.Context, domain string, query any, fetch func() error) error {
	_, err := lookup(ctx, domain, query, func() (bool, error) { err := fetch(); return err == nil, err }, nil, true)
	return err
}

// Enabled reports whether this collection can retain source-bound evidence.
func Enabled(ctx context.Context) bool {
	scope, _ := ctx.Value(contextKey{}).(*Scope)
	return scope != nil && scope.store != nil
}

// Reusable reports whether the exact query has an outcome this collection may
// replay. Callers use it before accepting legacy aggregate snapshots that do
// not carry endpoint/configuration provenance. Load failures remain fatal to
// the enclosing collection through Scope.Err.
func Reusable(ctx context.Context, domain string, query any) bool {
	scope, _ := ctx.Value(contextKey{}).(*Scope)
	if scope == nil || scope.store == nil || query == nil {
		return false
	}
	key, err := queryKey(query)
	if err != nil {
		scope.fail(err)
		return false
	}
	record, err := scope.store.LoadMetadataEvidence(ctx, scope.source, scope.fingerprint, domain, key)
	if err != nil {
		if !errors.Is(err, internalerrors.ErrNotFound) {
			scope.fail(err)
		}
		return false
	}
	if record.Outcome == api.MetadataEvidenceFailed {
		return true
	}
	if scope.freshness.RequiresRefresh() && refreshesProviderDomain(domain) {
		return false
	}
	return scope.freshness != api.ExternalFreshnessLoad || record.Outcome != api.MetadataEvidenceEmpty
}

func lookup[T any](ctx context.Context, domain string, query any, fetch func() (T, error), empty func(T) bool, retrySuccess bool) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, fmt.Errorf("metadata evidence canceled: %w", err)
	}
	scope, _ := ctx.Value(contextKey{}).(*Scope)
	if scope == nil || scope.store == nil {
		return fetch()
	}
	key, err := queryKey(query)
	if err != nil {
		return zero, err
	}
	scope.mu.Lock()
	call := scope.queries[domain+key]
	if call == nil {
		call = &queryState{gate: make(chan struct{}, 1)}
		call.gate <- struct{}{}
		scope.queries[domain+key] = call
	}
	scope.mu.Unlock()
	select {
	case <-ctx.Done():
		return zero, fmt.Errorf("metadata evidence canceled: %w", ctx.Err())
	case <-call.gate:
	}
	defer func() { call.gate <- struct{}{} }()
	seen := call.seen
	call.seen = true
	record, err := scope.store.LoadMetadataEvidence(ctx, scope.source, scope.fingerprint, domain, key)
	if err != nil && !errors.Is(err, internalerrors.ErrNotFound) {
		scope.fail(err)
		return zero, fmt.Errorf("load metadata evidence: %w", err)
	}
	if err == nil {
		refresh := retrySuccess ||
			!seen &&
				(scope.freshness.RequiresRefresh() && refreshesProviderDomain(domain) || (scope.freshness == api.ExternalFreshnessLoad && record.Outcome == api.MetadataEvidenceEmpty))
		if record.Outcome == api.MetadataEvidenceFailed {
			if scope.logger != nil {
				scope.logger.Debugf("metadata: evidence domain=%s decision=retain_failed", domain)
			}
			if len(record.Payload) > 0 {
				var partial T
				if err := json.Unmarshal(record.Payload, &partial); err != nil {
					scope.fail(err)
					return zero, errors.New("metadata evidence partial result cannot be decoded")
				}
				return partial, ErrSuppressed
			}
			return zero, ErrSuppressed
		}
		if !refresh {
			var result T
			if err := json.Unmarshal(record.Payload, &result); err != nil {
				scope.fail(err)
				return zero, fmt.Errorf("decode metadata evidence: %w", err)
			}
			if scope.logger != nil {
				scope.logger.Debugf("metadata: evidence domain=%s decision=reuse", domain)
			}
			return result, nil
		}
	}
	if scope.logger != nil {
		scope.logger.Debugf("metadata: evidence domain=%s decision=collect", domain)
	}
	result, fetchErr := fetch()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, fmt.Errorf("metadata evidence canceled: %w", ctxErr)
	}
	if errors.Is(fetchErr, context.Canceled) || errors.Is(fetchErr, context.DeadlineExceeded) {
		return result, fetchErr
	}
	record = api.MetadataEvidence{
		SourcePath:        scope.source,
		SourceFingerprint: scope.fingerprint,
		Domain:            domain,
		Key:               key,
		Outcome:           api.MetadataEvidenceSuccess,
	}
	if fetchErr != nil {
		record.Outcome = api.MetadataEvidenceFailed
		if !reflect.DeepEqual(result, zero) {
			record.Payload, err = json.Marshal(result)
			if err != nil {
				scope.fail(err)
				return zero, fmt.Errorf("encode partial metadata evidence: %w", err)
			}
		}
	} else {
		if empty != nil && empty(result) {
			record.Outcome = api.MetadataEvidenceEmpty
		}
		record.Payload, err = json.Marshal(result)
		if err != nil {
			scope.fail(err)
			return zero, fmt.Errorf("encode metadata evidence: %w", err)
		}
	}
	if err := scope.store.SaveMetadataEvidence(ctx, record); err != nil {
		scope.fail(err)
		return zero, fmt.Errorf("save metadata evidence: %w", err)
	}
	return result, fetchErr
}

func queryKey(query any) (string, error) {
	encoded, err := json.Marshal(query)
	if err != nil {
		return "", fmt.Errorf("metadata evidence query: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// RetainAlias binds a successful search's resolved identity to the same outcome.
// It never replaces an existing direct-query record, and preserves failed
// partial outcomes so learning an ID cannot silently retry its missing assets.
func RetainAlias(ctx context.Context, domain string, query, alias any) error {
	scope, _ := ctx.Value(contextKey{}).(*Scope)
	if scope == nil || scope.store == nil {
		return nil
	}
	key, err := queryKey(query)
	if err != nil {
		return err
	}
	aliasKey, err := queryKey(alias)
	if err != nil {
		return err
	}
	if key == aliasKey {
		return nil
	}
	if _, err := scope.store.LoadMetadataEvidence(ctx, scope.source, scope.fingerprint, domain, aliasKey); err == nil {
		return nil
	} else if !errors.Is(err, internalerrors.ErrNotFound) {
		scope.fail(err)
		return fmt.Errorf("load evidence alias: %w", err)
	}
	record, err := scope.store.LoadMetadataEvidence(ctx, scope.source, scope.fingerprint, domain, key)
	if err != nil {
		scope.fail(err)
		return fmt.Errorf("load evidence source: %w", err)
	}
	record.Key = aliasKey
	if err := scope.store.SaveMetadataEvidence(ctx, record); err != nil {
		scope.fail(err)
		return fmt.Errorf("save evidence alias: %w", err)
	}
	return nil
}

// A title-provider refresh does not invalidate independent source-tracker,
// scene, or disc-catalogue evidence. New queries and fresh-load misses still
// collect through their own dependency keys.
func refreshesProviderDomain(domain string) bool {
	return !strings.HasPrefix(domain, "tracker.") && !strings.HasPrefix(domain, "scene.") && !strings.HasPrefix(domain, "bluray.")
}
