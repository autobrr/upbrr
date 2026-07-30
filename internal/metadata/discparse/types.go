// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package discparse

type DiscType string

const (
	DiscBDMV  DiscType = "BDMV"
	DiscDVD   DiscType = "DVD"
	DiscHDDVD DiscType = "HDDVD"
)

type Disc struct {
	Path      string
	Name      string
	Type      DiscType
	Summary   string
	BDInfo    *BDInfo
	Playlists []PlaylistInfo
	VOBInfo   *DVDInfo
	HDDVD     *HDDVDInfo
}

type PlaylistInfo struct {
	File     string
	Duration float64
	Items    []PlaylistItem
	Edition  string
}

type PlaylistItem struct {
	File string
	Size int64
}

// BDInfo contains normalized fields parsed from a BDInfo quick summary. SizeGB
// is measured in binary GiB despite its historical field name; Path remains a
// host filesystem path supplied by the caller.
type BDInfo struct {
	Playlist  string
	SizeGB    float64
	Length    string
	Title     string
	Label     string
	Path      string
	Edition   string
	Video     []BDVideo
	Audio     []BDAudio
	Subtitles []string
	Files     []BDFile
}

type BDVideo struct {
	Codec       string
	Bitrate     string
	Resolution  string
	FPS         string
	AspectRatio string
	Profile     string
	BitDepth    string
	HDRDV       string
	Color       string
	ThreeD      string
}

type BDAudio struct {
	Language   string
	Codec      string
	Channels   string
	SampleRate string
	Bitrate    string
	BitDepth   string
	Atmos      string
}

type BDFile struct {
	File   string
	Length string
}

type DVDInfo struct {
	MainSet []string
	VOBMI   string
	IFOMI   string
	Size    string
	DiscGB  float64
}

type HDDVDInfo struct {
	EvoMI      string
	LargestEvo string
}
