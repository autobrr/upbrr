// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package clientdiscovery

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/pkg/api"
)

// WithLiveTestPolicy keeps injected client services behind process-owned guards.
// Read-only discovery remains available; force-recheck is a client mutation.
func WithLiveTestPolicy(client api.ClientService, policy *api.LiveTestPolicy) api.ClientService {
	if policy == nil {
		return client
	}
	return liveTestClientService{ClientService: client, policy: policy}
}

type liveTestClientService struct {
	api.ClientService
	policy *api.LiveTestPolicy
}

func (s liveTestClientService) Inject(context.Context, api.ClientSubject, api.TorrentResult) error {
	return fmt.Errorf("live-test injection: %w", s.policy.RejectMutation(api.OperationKindClientInjection))
}

func (s liveTestClientService) SearchPathedTorrents(ctx context.Context, subject api.ClientSubject) (api.ClientSearchResult, error) {
	if subject.ClientOverrides.ForceRecheck != nil && *subject.ClientOverrides.ForceRecheck {
		return api.ClientSearchResult{}, fmt.Errorf("live-test recheck: %w", s.policy.RejectMutation(api.OperationKindClientInjection))
	}
	result, err := s.ClientService.SearchPathedTorrents(ctx, subject)
	if err != nil {
		return result, fmt.Errorf("live-test client discovery: %w", err)
	}
	return result, nil
}
