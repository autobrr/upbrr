// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"fmt"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/azfamily"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/ant"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/ar"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/asc"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/bhd"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/bhdtv"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/bjs"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/bt"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/btn"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/czt"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/dc"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/ff"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/fl"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/gpw"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/hdb"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/hds"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/hdt"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/is"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/mtv"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/nbl"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/ptp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/pts"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/rtf"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/spd"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/thr"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/tl"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/tvc"
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
	for _, definition := range builtInDefinitions() {
		if err := registry.Register(definition); err != nil {
			return nil, fmt.Errorf("trackers: %w", err)
		}
	}
	registry.SetPriorityOrder([]string{"aither", "ulcx", "lst", "blu", "oe", "btn", "bhd", "hdb", "ant", "rf", "otw", "yus", "dp", "sp", "ptp"})
	return registry, nil
}

// MustNewRegistry returns the built-in tracker registry or panics when the
// compiled manifest violates registry invariants.
func MustNewRegistry() *trackers.Registry {
	registry, err := NewRegistry()
	if err != nil {
		panic(fmt.Sprintf("trackers: invalid built-in registry: %v", err))
	}
	return registry
}

func unit3DDefinitions() []trackers.Definition {
	profiles := []unit3d.Profile{
		a4k.Profile(), acm.Profile(), aither.Profile(), blu.Profile(), cbr.Profile(), dp.Profile(), emuw.Profile(), friki.Profile(), hhd.Profile(),
		ihd.Profile(), itt.Profile(), lcd.Profile(), ldu.Profile(), lst.Profile(), lt.Profile(), lume.Profile(), mns.Profile(), oe.Profile(), otw.Profile(),
		pt.Profile(), ptt.Profile(), r4e.Profile(), ras.Profile(), rf.Profile(), rhd.Profile(), sam.Profile(), shri.Profile(), sp.Profile(), stc.Profile(),
		tik.Profile(), tlz.Profile(), tos.Profile(), ttr.Profile(), ulcx.Profile(), utp.Profile(), yus.Profile(), znth.Profile(),
	}
	definitions := make([]trackers.Definition, 0, len(profiles))
	for _, profile := range profiles {
		definitions = append(definitions, unit3d.NewWithProfile(profile))
	}
	return definitions
}

func azFamilyDefinitions() []trackers.Definition {
	return []trackers.Definition{azfamily.New("AZ"), azfamily.New("CZ"), azfamily.New("PHD")}
}

func standaloneDefinitions() []trackers.Definition {
	return []trackers.Definition{
		hdb.New(), mtv.New(), ant.New(), ar.New(), asc.New(), bhd.New(), bhdtv.New(), bjs.New(), btn.New(), bt.New(), czt.New(), dc.New(), ff.New(),
		fl.New(), gpw.New(), hds.New(), hdt.New(), is.New(), nbl.New(), ptp.New(), pts.New(), rtf.New(), spd.New(), thr.New(), tl.New(), tvc.New(),
	}
}

func builtInDefinitions() []trackers.Definition {
	definitions := unit3DDefinitions()
	definitions = append(definitions, azFamilyDefinitions()...)
	definitions = append(definitions, standaloneDefinitions()...)
	return definitions
}
