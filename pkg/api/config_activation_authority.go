// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "context"

// ConfigActivationAuthority binds work admitted by one runtime bundle to the
// durable effective configuration generation that built that bundle.
type ConfigActivationAuthority struct {
	Generation  uint64
	Fingerprint WorkflowFingerprint
}

type configActivationAuthorityKey struct{}

// WithConfigActivationAuthority installs host-owned runtime generation
// authority. It is never decoded from a client request.
func WithConfigActivationAuthority(ctx context.Context, authority ConfigActivationAuthority) context.Context {
	return context.WithValue(ctx, configActivationAuthorityKey{}, authority)
}

// ConfigActivationAuthorityFromContext returns host-owned runtime generation
// authority when work was admitted through an activated runtime bundle.
func ConfigActivationAuthorityFromContext(ctx context.Context) (ConfigActivationAuthority, bool) {
	authority, ok := ctx.Value(configActivationAuthorityKey{}).(ConfigActivationAuthority)
	return authority, ok
}
