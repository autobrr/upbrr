// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package jobs

import (
	"errors"
	"fmt"
	"sync"

	"github.com/autobrr/upbrr/internal/logging"
)

// resourceSet closes named per-job resources exactly once and retains the combined result.
type resourceSet struct {
	once      sync.Once
	resources Resources
	err       error
}

func (r *resourceSet) close() error {
	if r == nil {
		return nil
	}
	r.once.Do(func() {
		if r.resources.Core != nil {
			r.err = errors.Join(r.err, closeResource("core", r.resources.Core))
		}
		if r.resources.Logger != nil {
			r.err = errors.Join(r.err, closeResource("logger", r.resources.Logger))
		}
		r.resources = Resources{}
	})
	return r.err
}

// closeResource converts close errors and panics into sanitized job diagnostics.
func closeResource(name string, resource Closer) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%s close panicked: %s", name, sanitizeMessage(fmt.Sprint(recovered)))
		}
	}()
	if err := resource.Close(); err != nil {
		return fmt.Errorf("%s close failed: %s", name, sanitizeMessage(err.Error()))
	}
	return nil
}

// sanitize removes secret and local-path detail before text enters a frontend-visible snapshot.
func sanitizeMessage(message string) string {
	message = logging.SanitizeMessage(message)
	if message == "" {
		return "operation failed"
	}
	return message
}
