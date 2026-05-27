// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"errors"
	"sync/atomic"
)

type appRuntimeContext struct {
	value atomic.Value
}

type runtimeContextValue struct {
	ctx context.Context
}

func newAppRuntimeContext(ctx context.Context) *appRuntimeContext {
	store := &appRuntimeContext{}
	store.Store(ctx)
	return store
}

func (s *appRuntimeContext) Store(ctx context.Context) {
	if s == nil || ctx == nil {
		return
	}
	s.value.Store(runtimeContextValue{ctx: ctx})
}

func (s *appRuntimeContext) Load() context.Context {
	if s == nil {
		return nil
	}
	value, _ := s.value.Load().(runtimeContextValue)
	return value.ctx
}

func (a *App) runtimeContext() context.Context {
	if a == nil {
		return context.Background()
	}
	if ctx := a.runtimeCtx.Load(); ctx != nil {
		return ctx
	}
	return context.Background()
}

func (a *App) readyRuntimeContext() (context.Context, error) {
	if a == nil {
		return nil, errors.New("app not initialized")
	}
	ctx := a.runtimeCtx.Load()
	if ctx == nil {
		return nil, errors.New("app context not ready")
	}
	return ctx, nil
}
