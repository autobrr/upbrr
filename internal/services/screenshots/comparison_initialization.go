// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package screenshots

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/asticode/go-astits"
	"github.com/bluenviron/mediacommon/v2/pkg/bits"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"

	"github.com/autobrr/upbrr/internal/dvdvideo/nav"
	"github.com/autobrr/upbrr/internal/pathing"
)

// validateComparisonInitialization proves independently initializing original
// codec data and primary-PID clocks within the finite encoded window. Parsing
// uses the already-installed transport/Annex-B readers, never ffprobe.
func validateComparisonInitialization(ctx context.Context, path string, window *comparisonDiscWindow) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("comparison initialization: %w", err)
	}
	defer file.Close()
	limit := int64(8 << 20)
	if window.Format == "mpegts" {
		limit = (16 << 20) - window.ProbeBytes
		if limit < 192 {
			return comparisonDiscFailure("packet_probe_limit")
		}
	}
	probe, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return fmt.Errorf("comparison initialization: %w", err)
	}
	if window.Format == "mpegts" {
		window.ProbeBytes += int64(len(probe))
	}
	if window.Format == "mpeg" {
		packets, err := nav.ParseVideoPES(probe)
		if err != nil {
			return comparisonDiscFailure("dvd_pes_initialization")
		}
		var payload []byte
		var firstPTS int64
		hasPTS := false
		for _, packet := range packets {
			if packet.StreamID != 0xe0 {
				return comparisonDiscFailure("dvd_video_stream")
			}
			if !hasPTS && packet.HasPTS {
				if packet.PTS >= 1<<33 {
					return comparisonDiscFailure("dvd_pes_clock")
				}
				firstPTS = int64(packet.PTS)
				hasPTS = true
			}
			payload = append(payload, packet.Data...)
			if len(payload) > 1<<20 {
				break
			}
		}
		firstPTS = comparisonDiscClockNear(firstPTS, window.Start90)
		if !hasPTS || firstPTS > window.Start90 || window.Start90-firstPTS > 10*90000 || !comparisonMPEG2Initialization(payload) {
			return comparisonDiscFailure("dvd_random_access_initialization")
		}
		digest := sha256.Sum256(payload)
		return retainComparisonInitialization(window, hex.EncodeToString(digest[:]))
	}
	if len(probe) < 192 || len(probe)%192 != 0 {
		probe = probe[:len(probe)/192*192]
	}
	var transport bytes.Buffer
	for offset := 0; offset < len(probe); offset += 192 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("comparison initialization: %w", err)
		}
		packet := probe[offset+4 : offset+192]
		if packet[0] != 0x47 || packet[1]&0x80 != 0 || packet[3]&0xc0 != 0 || packet[3]&0x30 == 0 {
			return comparisonDiscFailure("m2ts_packet_framing")
		}
		_, _ = transport.Write(packet)
	}
	pid, err := strconv.ParseUint(window.StreamID, 0, 16)
	if err != nil {
		return comparisonDiscFailure("primary_pid")
	}
	demuxer := astits.NewDemuxer(ctx, bytes.NewReader(transport.Bytes()), astits.DemuxerOptPacketSize(188))
	primary := false
	codec := byte(0)
	for {
		data, err := demuxer.NextData()
		if err != nil {
			return comparisonDiscFailure("transport_initialization")
		}
		if data.PMT != nil {
			found := 0
			for _, stream := range data.PMT.ElementaryStreams {
				if uint64(stream.ElementaryPID) == pid {
					found++
					if found > 1 || primary && codec != byte(stream.StreamType) {
						return comparisonDiscFailure("ambiguous_primary_pid")
					}
					primary = true
					codec = byte(stream.StreamType)
					if window.Codec != 0 && window.Codec != codec {
						return comparisonDiscFailure("primary_codec_changed")
					}
				}
			}
			if found != 1 {
				return comparisonDiscFailure("primary_pid_mapping")
			}
			if window.TransportTablePID != 0 && window.TransportTablePID != data.PID {
				return comparisonDiscFailure("transport_initialization_changed")
			}
			window.TransportTablePID = data.PID
		}
		if data.PES == nil || uint64(data.PID) != pid {
			continue
		}
		optional := data.PES.Header.OptionalHeader
		if !primary || optional == nil || optional.PTS == nil {
			return comparisonDiscFailure("original_pes_clock")
		}
		pts := comparisonDiscClockNear(optional.PTS.Base, window.RandomAccess90)
		if pts < window.RandomAccess90 {
			continue
		}
		if pts-window.RandomAccess90 > 512 || pts > window.Start90 || window.Start90-pts > 10*90000 {
			return comparisonDiscFailure("cpi_pes_clock")
		}
		if !comparisonCodecInitialization(data.PES.Data, codec) {
			return comparisonDiscFailure("independent_codec_initialization")
		}
		digest := sha256.Sum256(data.PES.Data)
		return retainComparisonInitialization(window, hex.EncodeToString(digest[:]))
	}
}

// The option terminator separates subfile options from the literal nested file
// path. Colons, commas and spaces in local paths cannot become protocol options.
func comparisonSubfileInput(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil || strings.ContainsAny(abs, "\x00\r\n") {
		return "", comparisonDiscFailure("local_input_path")
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 2<<30 {
		return "", comparisonDiscFailure("encoded_input_extent")
	}
	return fmt.Sprintf("subfile,,start,0,end,%d,,:file:%s", info.Size(), abs), nil
}

// Keep only original transport tables from a disjoint initialization prefix.
// Codec initialization is independently present at the selected random-access
// unit, so preceding movie/audio payloads cannot supply hidden decoder state.
func comparisonTransportTablesOnly(ctx context.Context, source ComparisonSource, path string, window *comparisonDiscWindow) error {
	if window.Format != "mpegts" || len(window.Spans) < 2 {
		return nil
	}
	prefixLength := window.Spans[0].End - window.Spans[0].Start
	if prefixLength <= 0 || prefixLength > 16<<20 || prefixLength%192 != 0 {
		return comparisonDiscFailure("transport_prefix")
	}
	file, err := pathing.OpenFileWithinRoot(source.ScratchDir, path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("comparison initialization: %w", err)
	}
	defer file.Close()
	prefix := make([]byte, prefixLength)
	if _, err := file.ReadAt(prefix, 0); err != nil {
		return fmt.Errorf("comparison initialization: %w", err)
	}
	window.ProbeBytes += prefixLength
	// Read only PSI here: a disjoint prefix can end inside a movie/audio PES.
	// Such a partial predecessor PES must never participate in initialization.
	var transport bytes.Buffer
	for offset := 0; offset < len(prefix); offset += 192 {
		packet := prefix[offset+4 : offset+192]
		if packet[0] != 0x47 || packet[1]&0x80 != 0 || packet[3]&0xc0 != 0 || packet[3]&0x30 == 0 {
			return comparisonDiscFailure("m2ts_packet_framing")
		}
		_, _ = transport.Write(packet)
	}
	selectedPID, err := strconv.ParseUint(window.StreamID, 0, 16)
	if err != nil {
		return comparisonDiscFailure("primary_pid")
	}
	allowed := map[uint16]bool{0: true}
	demuxer := astits.NewDemuxer(
		ctx,
		bytes.NewReader(transport.Bytes()),
		astits.DemuxerOptPacketSize(188),
		astits.DemuxerOptPacketSkipper(func(packet *astits.Packet) bool { return !allowed[packet.Header.PID] }),
	)
	found := false
	for !found {
		data, err := demuxer.NextData()
		if err != nil {
			return comparisonDiscFailure("transport_tables")
		}
		if data.PAT != nil {
			for _, programme := range data.PAT.Programs {
				if programme.ProgramNumber != 0 {
					allowed[programme.ProgramMapID] = true
				}
			}
		}
		if data.PMT != nil {
			for _, stream := range data.PMT.ElementaryStreams {
				if uint64(stream.ElementaryPID) == selectedPID {
					if window.TransportTablePID != 0 && window.TransportTablePID != data.PID {
						return comparisonDiscFailure("transport_initialization_changed")
					}
					window.TransportTablePID, found = data.PID, true
				}
			}
		}
	}
	kept := make([]byte, 0, len(prefix))
	for offset := 0; offset < len(prefix); offset += 192 {
		pid := uint16(prefix[offset+5]&31)<<8 | uint16(prefix[offset+6])
		if pid == 0 || pid == window.TransportTablePID {
			kept = append(kept, prefix[offset:offset+192]...)
		}
	}
	if len(kept) == 0 {
		return comparisonDiscFailure("transport_tables")
	}
	if _, err := file.WriteAt(kept, 0); err != nil {
		return fmt.Errorf("comparison initialization: %w", err)
	}
	readOffset, writeOffset := prefixLength, int64(len(kept))
	buffer := make([]byte, 64<<10)
	for {
		n, readErr := file.ReadAt(buffer, readOffset)
		if n > 0 {
			if _, err := file.WriteAt(buffer[:n], writeOffset); err != nil {
				return fmt.Errorf("compact comparison transport: %w", err)
			}
			readOffset += int64(n)
			writeOffset += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read comparison transport: %w", readErr)
		}
	}
	if err := file.Truncate(writeOffset); err != nil {
		return fmt.Errorf("truncate comparison transport: %w", err)
	}
	return nil
}

func retainComparisonInitialization(window *comparisonDiscWindow, digest string) error {
	if window.InitializationSHA256 != "" && window.InitializationSHA256 != digest {
		return comparisonDiscFailure("initialization_changed")
	}
	window.InitializationSHA256 = digest
	return nil
}

func comparisonCodecInitialization(payload []byte, codec byte) bool {
	if codec == 2 {
		return comparisonMPEG2Initialization(payload)
	}
	var units h264.AnnexB
	if err := units.Unmarshal(payload); err != nil {
		return false
	}
	vps, sps, pps := false, false, false
	if codec == 0x1b && !h264.IsRandomAccess(units) {
		return false
	}
	for _, unit := range units {
		if len(unit) == 0 {
			return false
		}
		switch codec {
		case 0x1b:
			switch unit[0] & 31 {
			case 7:
				sps = true
			case 8:
				pps = true
			case 5:
				return sps && pps
			case 1:
				if !sps || !pps || !comparisonExactAVCRecovery(units) {
					return false
				}
				position := 0
				payload := h264.EmulationPreventionRemove(unit[1:])
				if _, err := bits.ReadGolombUnsigned(payload, &position); err != nil {
					return false
				}
				kind, err := bits.ReadGolombUnsigned(payload, &position)
				return err == nil && kind%5 == 2
			case 2, 3, 4:
				return false
			}
		case 0x24:
			if len(unit) < 2 {
				return false
			}
			kind := (unit[0] >> 1) & 63
			switch kind {
			case 32:
				vps = true
			case 33:
				sps = true
			case 34:
				pps = true
			// CRA (21) starts remain unsupported: this window path neither validates
			// leading-picture dependencies nor discards RASL pictures before replay.
			case 16, 17, 18, 19, 20:
				return vps && sps && pps
			default:
				if kind < 32 {
					return false
				}
			}
		default:
			return false
		}
	}
	return false
}

func comparisonExactAVCRecovery(units h264.AnnexB) bool {
	for _, unit := range units {
		if len(unit) < 2 || unit[0]&31 != 6 {
			continue
		}
		payload := h264.EmulationPreventionRemove(unit[1:])
		for position := 0; position < len(payload)-1; {
			readValue := func() (int, bool) {
				value := 0
				for position < len(payload) {
					b := int(payload[position])
					position++
					if value > 1<<20-b {
						return 0, false
					}
					value += b
					if b != 255 {
						return value, true
					}
				}
				return 0, false
			}
			kind, ok := readValue()
			if !ok {
				return false
			}
			size, ok := readValue()
			if !ok || size > len(payload)-position {
				return false
			}
			// For recovery_frame_cnt=0, exact_match_flag=1, changing_slice_group_idc=0
			// and valid alignment bits, these are the two broken_link_flag values.
			if kind == 6 && size == 1 && (payload[position] == 0xc4 || payload[position] == 0xe4) {
				return true
			}
			position += size
		}
	}
	return false
}

func comparisonMPEG2Initialization(payload []byte) bool {
	sequence := bytes.Index(payload, []byte{0, 0, 1, 0xb3})
	picture := bytes.Index(payload, []byte{0, 0, 1, 0})
	return sequence >= 0 && picture > sequence && picture+6 <= len(payload) && payload[picture+5]>>3&7 == 1
}
