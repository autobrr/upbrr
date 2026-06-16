// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/autobrr/upbrr/internal/services/db"
)

type BannedGroupChecker struct {
	basePath string
	mu       sync.Mutex
	cache    map[string]map[string]struct{}
}

type bannedGroupsFile struct {
	BannedGroups string `json:"banned_groups"`
}

func NewBannedGroupChecker(dbPath string) *BannedGroupChecker {
	basePath, err := db.Subdir(dbPath, "cache")
	if err != nil {
		return nil
	}
	basePath = filepath.Join(basePath, "banned")
	return &BannedGroupChecker{basePath: basePath, cache: make(map[string]map[string]struct{})}
}

func (c *BannedGroupChecker) IsBanned(tracker, group string) (bool, error) {
	if c == nil {
		return false, nil
	}
	tracker = strings.ToUpper(strings.TrimSpace(tracker))
	group = strings.ToLower(strings.TrimSpace(group))
	if tracker == "" || group == "" {
		return false, nil
	}

	groups, err := c.load(tracker)
	if err != nil {
		return false, err
	}
	_, found := groups[group]
	return found, nil
}

func (c *BannedGroupChecker) load(tracker string) (map[string]struct{}, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cached, ok := c.cache[tracker]; ok {
		return cached, nil
	}

	groups := map[string]struct{}{}
	if builtin := builtinBannedGroups[tracker]; len(builtin) > 0 {
		for _, value := range builtin {
			cleaned := strings.ToLower(strings.TrimSpace(value))
			if cleaned != "" {
				groups[cleaned] = struct{}{}
			}
		}
		c.cache[tracker] = groups
		return groups, nil
	}

	filePath := filepath.Join(c.basePath, tracker+"_banned_groups.json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.cache[tracker] = groups
			return groups, nil
		}
		return nil, fmt.Errorf("trackers: read banned groups: %w", err)
	}

	var payload bannedGroupsFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("trackers: unmarshal banned groups: %w", err)
	}

	for value := range strings.SplitSeq(payload.BannedGroups, ",") {
		cleaned := strings.ToLower(strings.TrimSpace(value))
		if cleaned == "" {
			continue
		}
		groups[cleaned] = struct{}{}
	}

	c.cache[tracker] = groups
	return groups, nil
}

var builtinBannedGroups = map[string][]string{
	"BHD": {
		"Sicario",
		"TOMMY",
		"x0r",
		"nikt0",
		"FGT",
		"d3g",
		"MeGusta",
		"YIFY",
		"tigole",
		"TEKNO3D",
		"C4K",
		"RARBG",
		"4K4U",
		"EASports",
		"ReaLHD",
		"Telly",
		"AOC",
		"WKS",
		"SasukeducK",
		"CRUCiBLE",
		"iFT",
		"ProRes",
		"MezRips",
		"Flights",
		"BiTOR",
		"iVy",
		"QxR",
		"SyncUP",
		"OFT",
		"TGS",
	},
	"DP": {
		"ARCADE",
		"aXXo",
		"BANDOLEROS",
		"BONE",
		"BRrip",
		"CM8",
		"CrEwSaDe",
		"CTFOH",
		"dAV1nci",
		"DNL",
		"eranger2",
		"FaNGDiNG0",
		"FGT",
		"FiSTER",
		"flower",
		"GalaxyTV",
		"HD2DVD",
		"HDTime",
		"HorribleSubs",
		"iHYTECH",
		"ION10",
		"iPlanet",
		"KiNGDOM",
		"LAMA",
		"MeGusta",
		"mHD",
		"mSD",
		"NaNi",
		"NhaNc3",
		"nHD",
		"nikt0",
		"nSD",
		"OFT",
		"PiTBULL",
		"PRODJi",
		"PSA",
		"RARBG",
		"Rifftrax",
		"ROCKETRACCOON",
		"SANTi",
		"SasukeducK",
		"SEEDSTER",
		"ShAaNiG",
		"Sicario",
		"STUTTERSHIT",
		"Subsplease",
		"SyncUp",
		"TAoE",
		"TGALAXY",
		"TGx",
		"TORRENTGALAXY",
		"ToVaR",
		"Trix",
		"TSP",
		"TSPxL",
		"ViSION",
		"VXT",
		"WAF",
		"WKS",
		"X0r",
		"YIFY",
		"YTS",
	},
	"RHD": {
		"1XBET",
		"MEGA",
		"MTZ",
		"Whistler",
		"WOTT",
		"Taylor.D",
		"HELD",
		"FSX",
		"FuN",
		"MagicX",
		"w00t",
		"PaTroL",
		"BB",
		"266ers",
		"GTF",
		"JellyfinPlex",
		"2BA",
		"FritzBox",
		"FUNXDTV",
	},
	"TOS": {
		"FL3ER",
		"SUNS3T",
		"WoLFHD",
		"EXTREME",
		"Slay3R",
		"3T3AM",
		"BARBiE",
	},
}
