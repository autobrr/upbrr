// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediainfo

import "testing"

func TestDurationMinutesAcceptedFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want int
	}{
		{
name: "unit tokens",
 text: "Duration : 1 h 31 min 30 s",
 want: 92,
},
		{
name: "BJS mn token",
 text: "Duration/String3 : 1 h 31 mn",
 want: 91,
},
		{
name: "colon",
 text: "Duration : 01:31:30.000",
 want: 92,
},
		{
name: "milliseconds",
 text: "Duration : 5490000",
 want: 92,
},
		{
name: "ISO",
 text: "Duration : PT1H31M30S",
 want: 92,
},
		{
name: "first parseable line",
 text: "Duration : invalid\nDuration/String1 : 45 min",
 want: 45,
},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := DurationMinutes(test.text); got != test.want {
				t.Fatalf("DurationMinutes() = %d, want %d", got, test.want)
			}
		})
	}
}
