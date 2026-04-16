// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"sort"
	"strings"
	"sync"

	"github.com/autobrr/upbrr/internal/pathutil"
	"github.com/autobrr/upbrr/pkg/api"
)

var (
	serviceInitOnce sync.Once
	serviceMap      map[string]string
	serviceKeys     []string
	serviceNeedles  map[string]string
	serviceLong     map[string]string

	commonReplacer = strings.NewReplacer(".", " ", "(", " ", ")", " ")
	aliasReplacer  = strings.NewReplacer(".", " ", "(", " ", ")", " ", ":", " ")
)

func initServiceData() {
	serviceInitOnce.Do(func() {
		serviceMap = serviceCodeMap()
		serviceKeys = make([]string, 0, len(serviceMap))
		serviceNeedles = make(map[string]string, len(serviceMap))
		serviceLong = make(map[string]string)

		for k, v := range serviceMap {
			serviceKeys = append(serviceKeys, k)
			serviceNeedles[k] = " " + strings.ToLower(strings.TrimSpace(k)) + " "

			if current, ok := serviceLong[v]; !ok || len(k) > len(current) {
				serviceLong[v] = k
			}
		}

		sort.Slice(serviceKeys, func(i, j int) bool {
			return len(serviceKeys[i]) > len(serviceKeys[j])
		})
	})
}

func resolveService(meta api.PreparedMetadata) (string, string, string) {
	initServiceData()

	filename := strings.TrimSpace(meta.Filename)
	if filename == "" {
		filename = pathutil.Base(meta.SourcePath)
	}
	cleaned := commonReplacer.Replace(filename)
	if tag := strings.TrimSpace(meta.Tag); tag != "" {
		cleaned = strings.ReplaceAll(cleaned, tag, "")
	}
	if audio := strings.TrimSpace(meta.Audio); strings.Contains(audio, "DTS-HD MA") {
		cleaned = strings.ReplaceAll(cleaned, "DTS-HD.MA.", "")
		cleaned = strings.ReplaceAll(cleaned, "DTS-HD MA ", "")
	}
	if meta.ExternalMetadata.TVDB != nil {
		for _, alias := range meta.ExternalMetadata.TVDB.Aliases {
			cleanAlias := aliasReplacer.Replace(alias)
			words := strings.Fields(cleanAlias)
			cleanAlias = strings.Join(words, " ")
			cleaned = strings.ReplaceAll(cleaned, cleanAlias, "")
		}
	}
	if meta.Release.Title != "" {
		cleaned = strings.ReplaceAll(cleaned, meta.Release.Title, "")
	}

	cleanedLower := " " + strings.ToLower(cleaned) + " "

	service := strings.TrimSpace(meta.Service)
	if service == "" {
		for _, key := range serviceKeys {
			needle := serviceNeedles[key]
			if needle != "  " && strings.Contains(cleanedLower, needle) {
				service = serviceMap[key]
				break
			}
		}
	}

	longName := ""
	if service != "" {
		if name, ok := serviceLong[service]; ok {
			longName = name
		} else {
			longName = service
		}
	}

	return service, longName, filename
}
