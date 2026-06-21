// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/ant"
	"github.com/autobrr/upbrr/internal/trackers/impl/ar"
	"github.com/autobrr/upbrr/internal/trackers/impl/asc"
	"github.com/autobrr/upbrr/internal/trackers/impl/azfamily"
	"github.com/autobrr/upbrr/internal/trackers/impl/bhd"
	"github.com/autobrr/upbrr/internal/trackers/impl/bhdtv"
	"github.com/autobrr/upbrr/internal/trackers/impl/bjs"
	"github.com/autobrr/upbrr/internal/trackers/impl/bt"
	"github.com/autobrr/upbrr/internal/trackers/impl/btn"
	"github.com/autobrr/upbrr/internal/trackers/impl/czt"
	"github.com/autobrr/upbrr/internal/trackers/impl/dc"
	"github.com/autobrr/upbrr/internal/trackers/impl/ff"
	"github.com/autobrr/upbrr/internal/trackers/impl/fl"
	"github.com/autobrr/upbrr/internal/trackers/impl/gpw"
	"github.com/autobrr/upbrr/internal/trackers/impl/hdb"
	"github.com/autobrr/upbrr/internal/trackers/impl/hds"
	"github.com/autobrr/upbrr/internal/trackers/impl/hdt"
	"github.com/autobrr/upbrr/internal/trackers/impl/is"
	"github.com/autobrr/upbrr/internal/trackers/impl/mtv"
	"github.com/autobrr/upbrr/internal/trackers/impl/nbl"
	"github.com/autobrr/upbrr/internal/trackers/impl/ptp"
	"github.com/autobrr/upbrr/internal/trackers/impl/pts"
	"github.com/autobrr/upbrr/internal/trackers/impl/rtf"
	"github.com/autobrr/upbrr/internal/trackers/impl/spd"
	"github.com/autobrr/upbrr/internal/trackers/impl/thr"
	"github.com/autobrr/upbrr/internal/trackers/impl/tl"
	"github.com/autobrr/upbrr/internal/trackers/impl/tvc"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
)

func NewRegistry() (*trackers.Registry, error) {
	registry := trackers.NewRegistry()
	if err := unit3d.Register(registry, unit3d.DefaultTrackers()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(hdb.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(mtv.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(ant.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(ar.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(asc.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(bhd.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(bhdtv.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(bjs.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(btn.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(bt.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(czt.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(dc.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(ff.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(fl.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(gpw.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(hds.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(hdt.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(is.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(nbl.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(ptp.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(pts.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(rtf.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(spd.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(thr.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(tl.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	if err := registry.Register(tvc.New()); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	for _, name := range []string{"AZ", "CZ", "PHD"} {
		if err := registry.Register(azfamily.New(name)); err != nil {
			return nil, fmt.Errorf("trackers: %w", err)
		}
	}
	return registry, nil
}
