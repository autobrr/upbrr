// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"github.com/autobrr/upbrr/internal/trackerdata"
	"github.com/autobrr/upbrr/internal/trackers"
)

func buildHandlers(deps handlerDeps) map[string]searchHandler {
	handlers := map[string]searchHandler{}

	unit := unit3dHandler{cfg: deps.cfg, tracker: deps.tracker}
	for _, tracker := range trackers.Unit3DTrackers() {
		handlers[tracker] = unit
	}

	// Functional non-Unit3D handlers (first pass parity)
	handlers["ANT"] = antHandler{cfg: deps.cfg, http: deps.http, logger: deps.logger}
	handlers["BHD"] = bhdHandler{cfg: deps.cfg, http: deps.http, logger: deps.logger}
	handlers["BTN"] = btnHandler{cfg: deps.cfg, http: deps.http}
	handlers["HDB"] = hdbHandler{cfg: deps.cfg, http: deps.http, logger: deps.logger}
	handlers["PTP"] = ptpHandler{cfg: deps.cfg, http: deps.http, logger: deps.logger}
	handlers["BHDTV"] = bhdtvHandler{}
	handlers["DC"] = dcHandler{cfg: deps.cfg, http: deps.http}
	handlers["GPW"] = gpwHandler{cfg: deps.cfg, http: deps.http}
	handlers["MTV"] = mtvHandler{cfg: deps.cfg, http: deps.http}
	handlers["NBL"] = nblHandler{cfg: deps.cfg, http: deps.http}
	handlers["AR"] = arHandler{cfg: deps.cfg, http: deps.http, logger: deps.logger}
	handlers["RTF"] = rtfHandler{cfg: deps.cfg, http: deps.http, logger: deps.logger}
	handlers["SPD"] = spdHandler{cfg: deps.cfg, http: deps.http}
	handlers["TL"] = tlHandler{cfg: deps.cfg, http: deps.http}
	handlers["TVC"] = tvcHandler{}

	// Explicit per-tracker stubs
	handlers["ASC"] = ascHandler{cfg: deps.cfg, http: deps.http, logger: deps.logger}
	handlers["AZ"] = azHandler{cfg: deps.cfg, http: deps.http, logger: deps.logger}
	handlers["BJS"] = bjsHandler{cfg: deps.cfg, http: deps.http}
	handlers["BT"] = btHandler{cfg: deps.cfg, http: deps.http, logger: deps.logger}
	handlers["CZ"] = czHandler{cfg: deps.cfg, http: deps.http, logger: deps.logger}
	handlers["CZT"] = cztHandler{cfg: deps.cfg, http: deps.http, logger: deps.logger}
	handlers["FF"] = ffHandler{cfg: deps.cfg, http: deps.http}
	handlers["FL"] = flHandler{cfg: deps.cfg, http: deps.http}
	handlers["HDS"] = hdsHandler{cfg: deps.cfg, http: deps.http}
	handlers["HDT"] = hdtHandler{cfg: deps.cfg, http: deps.http}
	handlers["IS"] = isHandler{cfg: deps.cfg, http: deps.http}
	handlers["PHD"] = phdHandler{cfg: deps.cfg, http: deps.http, logger: deps.logger}
	handlers["PTS"] = ptsHandler{cfg: deps.cfg, http: deps.http}
	handlers["THR"] = thrHandler{cfg: deps.cfg, http: deps.http}

	// Config-aware Unit3D fallback for unknown/custom Unit3D trackers.
	for name := range deps.cfg.Trackers.Trackers {
		normalized := normalizeTracker(name)
		if normalized == "" {
			continue
		}
		if _, exists := handlers[normalized]; exists {
			continue
		}
		if trackerdata.IsUnit3DTrackerWithConfig(deps.cfg, normalized) {
			handlers[normalized] = unit
		}
	}

	return handlers
}
