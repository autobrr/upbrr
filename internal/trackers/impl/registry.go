// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
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
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/a4k"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/acm"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/aither"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/blu"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/cbr"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/dp"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/emuw"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/friki"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/hhd"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ihd"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/itt"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lcd"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ldu"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lst"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lt"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lume"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/mns"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/oe"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/otw"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/pt"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ptt"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/r4e"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ras"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/rf"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/rhd"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/sam"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/shri"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/sp"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/stc"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/tik"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/tlz"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/tos"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ttr"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ulcx"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/utp"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/yus"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/znth"
)

func NewRegistry() (*trackers.Registry, error) {
	registry := trackers.NewRegistry()
	profiles := []unit3d.Profile{
		a4k.Profile(),
		acm.Profile(),
		aither.Profile(),
		blu.Profile(),
		cbr.Profile(),
		dp.Profile(),
		emuw.Profile(),
		friki.Profile(),
		hhd.Profile(),
		ihd.Profile(),
		itt.Profile(),
		lcd.Profile(),
		ldu.Profile(),
		lt.Profile(),
		lume.Profile(),
		lst.Profile(),
		mns.Profile(),
		pt.Profile(),
		ptt.Profile(),
		r4e.Profile(),
		ras.Profile(),
		rf.Profile(),
		rhd.Profile(),
		sam.Profile(),
		oe.Profile(),
		otw.Profile(),
		shri.Profile(),
		sp.Profile(),
		stc.Profile(),
		tik.Profile(),
		tlz.Profile(),
		tos.Profile(),
		ttr.Profile(),
		ulcx.Profile(),
		znth.Profile(),
		utp.Profile(),
		yus.Profile(),
	}
	if err := unit3d.RegisterProfiles(registry, profiles); err != nil {
		return nil, fmt.Errorf("trackers: %w", err)
	}
	definitions := []trackers.Definition{
		hdb.New(), mtv.New(), ant.New(), ar.New(), asc.New(), bhd.New(), bhdtv.New(), bjs.New(), btn.New(), bt.New(), czt.New(), dc.New(), ff.New(),
		fl.New(), gpw.New(), hds.New(), hdt.New(), is.New(), nbl.New(), ptp.New(), pts.New(), rtf.New(), spd.New(), thr.New(), tl.New(), tvc.New(),
	}
	for _, definition := range definitions {
		if err := registry.Register(definition); err != nil {
			return nil, fmt.Errorf("trackers: %w", err)
		}
	}
	for _, name := range []string{"AZ", "CZ", "PHD"} {
		if err := registry.Register(azfamily.New(name)); err != nil {
			return nil, fmt.Errorf("trackers: %w", err)
		}
	}
	registry.SetPriorityOrder([]string{"aither", "ulcx", "lst", "blu", "oe", "btn", "bhd", "hdb", "ant", "rf", "otw", "yus", "dp", "sp", "ptp"})
	return registry, nil
}

// NewRegistryWithConfig composes built-in definitions and configured custom
// Unit3D trackers. Runtime config URLs remain authoritative in the Unit3D client.
func NewRegistryWithConfig(cfg config.Config) (*trackers.Registry, error) {
	registry, err := NewRegistry()
	if err != nil {
		return nil, err
	}
	for name := range cfg.Trackers.Trackers {
		normalized := strings.ToUpper(strings.TrimSpace(name))
		if normalized == "" {
			continue
		}
		if _, exists := registry.LookupDescriptor(normalized); exists {
			continue
		}
		if !unit3d.IsConfiguredTrackerWithRegistry(cfg, normalized, registry) {
			continue
		}
		if err := registry.Register(unit3d.New(normalized)); err != nil {
			return nil, fmt.Errorf("trackers: register custom unit3d %s: %w", normalized, err)
		}
	}
	return registry, nil
}
