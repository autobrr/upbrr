// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"net/http"
	"os"

	"github.com/autobrr/upbrr/internal/authmaterial"
)

type webBrowsePolicy struct {
	Roots             []string
	AllowUnrestricted bool
}

func (s *Server) webBrowsePolicy() (webBrowsePolicy, error) {
	if s == nil || s.auth == nil {
		return webBrowsePolicy{}, nil
	}
	record, err := s.auth.Load()
	if err != nil {
		if os.IsNotExist(err) {
			return webBrowsePolicy{}, nil
		}
		return webBrowsePolicy{}, err
	}
	if record.AllowUnrestrictedBrowse {
		return webBrowsePolicy{AllowUnrestricted: true}, nil
	}
	roots, err := normalizeBrowsePolicyRoots(splitBrowsePolicyRoots(record.BrowseRoot))
	if err != nil {
		return webBrowsePolicy{}, err
	}
	return webBrowsePolicy{Roots: roots}, nil
}

func (s *Server) isDesktopAPIRequest(r *http.Request, token apiTokenStatus) bool {
	return s.isLocalWebUIRequest(r) && token.Purpose == authmaterial.APITokenPurposeDesktop
}
