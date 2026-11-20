// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nav

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// VideoPES is one original MPEG-2 video PES clock and borrowed payload.
// Data aliases the input and must be copied before that input is reused.
type VideoPES struct {
	StreamID uint8
	PTS      uint64
	HasPTS   bool
	Data     []byte
}

// ParseVideoPES reads bounded original PS packets without reconstructing clocks.
// A partial final packet is permitted only when earlier complete packets exist.
func ParseVideoPES(data []byte) ([]VideoPES, error) {
	if len(data) > 8<<20 {
		return nil, fmt.Errorf("%w: video PES probe limit", ErrInvalidNAV)
	}
	var packets []VideoPES
	for offset := 0; offset+6 <= len(data); {
		index := bytes.Index(data[offset:], []byte{0, 0, 1})
		if index < 0 {
			break
		}
		offset += index
		if offset+6 > len(data) {
			break
		}
		stream := data[offset+3]
		if stream < 0xe0 || stream > 0xef {
			if stream == 0xba {
				end, err := packEnd(data, offset)
				if err != nil {
					return nil, err
				}
				offset = end
				continue
			}
			if stream == 0xb9 {
				offset += 4
				continue
			}
			if stream < 0xbb {
				return nil, ErrInvalidNAV
			}
			length := int(binary.BigEndian.Uint16(data[offset+4:]))
			end := offset + 6 + length
			if end > len(data) {
				if len(packets) > 0 {
					break
				}
				return nil, ErrInvalidNAV
			}
			offset = end
			continue
		}
		length := int(binary.BigEndian.Uint16(data[offset+4:]))
		end := offset + 6 + length
		if end > len(data) {
			if len(packets) > 0 {
				break
			}
			return nil, ErrInvalidNAV
		}
		if length < 3 || data[offset+6]&0xc0 != 0x80 {
			return nil, ErrInvalidNAV
		}
		headerLength := int(data[offset+8])
		payload := offset + 9 + headerLength
		if payload > end {
			return nil, ErrInvalidNAV
		}
		packet := VideoPES{StreamID: stream, Data: data[payload:end]}
		flags := data[offset+7] >> 6
		if flags == 1 {
			return nil, ErrInvalidNAV
		}
		if flags == 2 || flags == 3 {
			if headerLength < 5 || flags == 3 && headerLength < 10 {
				return nil, ErrInvalidNAV
			}
			stamp := data[offset+9 : offset+14]
			if stamp[0]>>4 != flags || stamp[0]&1 == 0 || stamp[2]&1 == 0 || stamp[4]&1 == 0 {
				return nil, ErrInvalidNAV
			}
			packet.PTS = decodePTS(stamp)
			packet.HasPTS = true
		}
		packets = append(packets, packet)
		if len(packets) > 4096 {
			return nil, ErrInvalidNAV
		}
		offset = end
	}
	return packets, nil
}
