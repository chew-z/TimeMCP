package main

import (
	"strings"
	"testing"
	"time"
)

func TestFormatDescribeTimeSingle(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc)

	cases := []struct {
		name  string
		input string
		want  []string // substrings that must appear in output
	}{
		{
			name:  "bare time in future",
			input: "22:08",
			want:  []string{`input:`, `"22:08"`, `machine:`, `2026-07-09T22:08:00-04:00`, `human:`, `today at 10:08 PM`, `tense:`, `future`, `perspective:`},
		},
		{
			name:  "historical date",
			input: "July 4th, 1776",
			want:  []string{`machine:`, `1776-07-04T00:00:00`, `human:`, `Thursday, July 4th, 1776`, `tense:`, `past`, `perspective:`, `250 years ago`},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			candidates, _, err := parseFlexibleDate(c.input, now)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			out := formatDescribeTime(c.input, candidates, "", now)
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("output missing %q\nGot:\n%s", w, out)
				}
			}
			// Verify machine value round-trips.
			if !strings.Contains(out, "ambiguous") {
				lines := strings.SplitSeq(out, "\n")
				for line := range lines {
					if after, ok := strings.CutPrefix(line, "machine:"); ok {
						machineVal := strings.TrimSpace(after)
						_, err := time.Parse(time.RFC3339, machineVal)
						if err != nil {
							t.Errorf("machine value %q does not parse as RFC3339: %v", machineVal, err)
						}
					}
				}
			}
		})
	}
}

func TestFormatDescribeTimeUsesRequestedZone(t *testing.T) {
	warsaw, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name          string
		input         string
		now           time.Time
		machine       string
		humanContains []string
	}{
		{
			name:          "RFC3339 Z input in CEST",
			input:         "2026-10-22T14:33:57Z",
			now:           time.Date(2026, 10, 23, 2, 44, 0, 0, warsaw),
			machine:       "2026-10-22T16:33:57+02:00",
			humanContains: []string{"yesterday", "4:33 PM"},
		},
		{
			name:          "RFC3339 Z input in CET",
			input:         "2026-01-09T23:00:00Z",
			now:           time.Date(2026, 1, 10, 10, 0, 0, 0, warsaw),
			machine:       "2026-01-10T00:00:00+01:00",
			humanContains: []string{"today", "12:00 AM"},
		},
		{
			name:          "UTC and Warsaw dates differ near midnight",
			input:         "2026-10-22T22:30:00Z",
			now:           time.Date(2026, 10, 23, 10, 0, 0, 0, warsaw),
			machine:       "2026-10-23T00:30:00+02:00",
			humanContains: []string{"today", "12:30 AM"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidateTime, err := time.Parse(time.RFC3339, tc.input)
			if err != nil {
				t.Fatalf("parse input time: %v", err)
			}
			out := formatDescribeTime(tc.input, []timeCandidate{{Time: candidateTime, HasTime: true}}, "", tc.now)
			if !strings.Contains(out, "machine:     "+tc.machine) {
				t.Errorf("machine line missing %q\nGot:\n%s", tc.machine, out)
			}
			for _, want := range tc.humanContains {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\nGot:\n%s", want, out)
				}
			}
			if tc.name == "UTC and Warsaw dates differ near midnight" && strings.Contains(out, "yesterday") {
				t.Errorf("human line should say today, not yesterday\nGot:\n%s", out)
			}
		})
	}
}

func TestFormatDescribeTimeMultiUsesRequestedZone(t *testing.T) {
	warsaw, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 23, 10, 0, 0, 0, warsaw)
	inputTime, err := time.Parse(time.RFC3339, "2026-10-22T22:30:00Z")
	if err != nil {
		t.Fatal(err)
	}
	candidates := []timeCandidate{
		{Label: "first", Time: inputTime, HasTime: true},
		{Label: "second", Time: inputTime.Add(time.Hour), HasTime: true},
	}

	out := formatDescribeTime("ambiguous", candidates, "ambiguous", now)
	for _, machine := range []string{"2026-10-23T00:30:00+02:00", "2026-10-23T01:30:00+02:00"} {
		if !strings.Contains(out, machine) {
			t.Errorf("output missing machine timestamp %q\nGot:\n%s", machine, out)
		}
	}
}

func TestFormatDescribeTimeAmbiguous(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc)

	// Weekday mismatch.
	candidates, ambNote, err := parseFlexibleDate("Thursday, July 20th", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) < 2 {
		t.Fatal("expected >=2 candidates")
	}
	out := formatDescribeTime("Thursday, July 20th", candidates, ambNote, now)

	required := []string{
		"input:", "Thursday, July 20th",
		"status:", "ambiguous",
		"candidate 1", "as written",
		"candidate 2", "if you meant",
		"machine:", "human:", "tense:", "perspective:",
	}
	for _, w := range required {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\nGot:\n%s", w, out)
		}
	}

	// Verify machine values round-trip.
	lines := strings.SplitSeq(out, "\n")
	for line := range lines {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "machine:"); ok {
			machineVal := strings.TrimSpace(after)
			_, err := time.Parse(time.RFC3339, machineVal)
			if err != nil {
				t.Errorf("machine value %q does not parse as RFC3339: %v", machineVal, err)
			}
		}
	}
}

func TestPerspectiveBuckets(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc)

	cases := []struct {
		name    string
		t       time.Time
		hasTime bool
		want    string
	}{
		{"right now", now, true, "right now"},
		{"<1 min future", now.Add(30 * time.Second), true, "right now"},
		{"<45 min future", now.Add(20 * time.Minute), true, "in about 20 minutes"},
		{"45-90 min past", now.Add(-65 * time.Minute), true, "about an hour ago"},
		{"same day evening (composite)", time.Date(2026, 7, 9, 22, 8, 0, 0, loc), true, "tonight, about an hour from now"},
		{"same day afternoon", time.Date(2026, 7, 9, 14, 0, 0, 0, loc), true, "earlier afternoon"},
		{"today (day-level, midnight)", time.Date(2026, 7, 9, 0, 0, 0, 0, loc), false, "today"},
		{"tomorrow", time.Date(2026, 7, 10, 0, 0, 0, 0, loc), false, "tomorrow"},
		{"tomorrow evening", time.Date(2026, 7, 10, 19, 0, 0, 0, loc), true, "tomorrow evening"},
		{"yesterday", time.Date(2026, 7, 8, 0, 0, 0, 0, loc), false, "yesterday"},
		{"3 days future (this coming)", time.Date(2026, 7, 12, 0, 0, 0, 0, loc), false, "this coming Sunday"},
		{"3 days past (last)", time.Date(2026, 7, 6, 0, 0, 0, 0, loc), false, "last Monday"},
		{"next week (8 days)", time.Date(2026, 7, 17, 0, 0, 0, 0, loc), false, "next week"},
		{"last week (8 days ago)", time.Date(2026, 7, 1, 0, 0, 0, 0, loc), false, "last week"},
		{"a few weeks ago", time.Date(2026, 6, 18, 0, 0, 0, 0, loc), false, "a few weeks ago"},
		{"in a couple of weeks", time.Date(2026, 7, 25, 0, 0, 0, 0, loc), false, "in a couple of weeks"},
		{"29 days future (about a month)", time.Date(2026, 8, 7, 0, 0, 0, 0, loc), false, "in about a month"},
		{"29 days past (about a month)", time.Date(2026, 6, 10, 0, 0, 0, 0, loc), false, "about a month ago"},
		{"30 days future (about a month)", time.Date(2026, 8, 8, 12, 0, 0, 0, loc), true, "in about a month"},
		{"in early Sept", time.Date(2026, 9, 3, 0, 0, 0, 0, loc), false, "in early September"},
		{"last November", time.Date(2025, 11, 15, 0, 0, 0, 0, loc), false, "last November (mid)"},
		{"in early May", time.Date(2027, 5, 10, 0, 0, 0, 0, loc), false, "in early May"},
		{"250 years ago", time.Date(1776, 7, 4, 0, 0, 0, 0, loc), false, "250 years ago"},
		{"in 50 years", time.Date(2076, 7, 9, 0, 0, 0, 0, loc), false, "in 50 years"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := perspectivePhrase(c.t, now, c.hasTime)
			if got != c.want {
				t.Errorf("perspectivePhrase(%s, now) = %q, want %q",
					c.t.Format("2006-01-02 15:04"), got, c.want)
			}
		})
	}
}

func TestFormatHuman(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc)

	cases := []struct {
		name    string
		t       time.Time
		hasTime bool
		want    string
	}{
		{"today no time", time.Date(2026, 7, 9, 0, 0, 0, 0, loc), false, "today"},
		{"today with time", time.Date(2026, 7, 9, 22, 8, 0, 0, loc), true, "today at 10:08 PM"},
		{"tomorrow no time", time.Date(2026, 7, 10, 0, 0, 0, 0, loc), false, "tomorrow"},
		{"tomorrow with time", time.Date(2026, 7, 10, 17, 0, 0, 0, loc), true, "tomorrow at 5:00 PM"},
		{"yesterday", time.Date(2026, 7, 8, 0, 0, 0, 0, loc), false, "yesterday"},
		{"next week no time", time.Date(2026, 7, 14, 0, 0, 0, 0, loc), false, "Tuesday, July 14th"},
		{"next week with time", time.Date(2026, 7, 14, 17, 0, 0, 0, loc), true, "Tuesday, July 14th at 5:00 PM"},
		{"different year", time.Date(1776, 7, 4, 0, 0, 0, 0, loc), false, "Thursday, July 4th, 1776"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatHuman(c.t, now, c.hasTime)
			if got != c.want {
				t.Errorf("formatHuman = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDaysBetweenDatesDST(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")

	// Spring-forward day (2026-03-08) is 23h long; the count must not truncate.
	now := time.Date(2026, 3, 8, 21, 0, 0, 0, loc)
	tomorrow := time.Date(2026, 3, 9, 0, 0, 0, 0, loc)
	if got := daysBetweenDates(now, tomorrow); got != 1 {
		t.Errorf("daysBetweenDates across spring-forward = %d, want 1", got)
	}
	if got := formatHuman(tomorrow, now, false); got != "tomorrow" {
		t.Errorf("formatHuman across spring-forward = %q, want %q", got, "tomorrow")
	}
	if got := perspectivePhrase(tomorrow, now, false); got != "tomorrow" {
		t.Errorf("perspectivePhrase across spring-forward = %q, want %q", got, "tomorrow")
	}

	// Fall-back day (2026-11-01) is 25h long; must not over-count.
	now = time.Date(2026, 10, 31, 21, 0, 0, 0, loc)
	nov2 := time.Date(2026, 11, 2, 0, 0, 0, 0, loc)
	if got := daysBetweenDates(now, nov2); got != 2 {
		t.Errorf("daysBetweenDates across fall-back = %d, want 2", got)
	}
}

func TestPartOfDay(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	cases := []struct {
		hour int
		want string
	}{
		{3, "overnight"},
		{7, "morning"},
		{10, "late morning"},
		{14, "afternoon"},
		{19, "evening"},
		{23, "tonight"},
	}
	for _, c := range cases {
		tm := time.Date(2026, 7, 9, c.hour, 0, 0, 0, loc)
		got := partOfDay(tm)
		if got != c.want {
			t.Errorf("partOfDay(%d:00) = %q, want %q", c.hour, got, c.want)
		}
	}
}

func TestMidnightDefaultForDayLevelPhrases(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc)

	tests := []struct {
		input string
	}{
		{"tomorrow"},
		{"next tuesday"},
		{"friday"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			candidates, _, err := parseFlexibleDate(tt.input, now)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := candidates[0].Time
			if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 {
				t.Errorf("%q: expected midnight, got %s", tt.input, got.Format("15:04:05"))
			}
			// Verify human format doesn't show clock time for midnight-default inputs.
			out := formatDescribeTime(tt.input, candidates, "", now)
			lines := strings.SplitSeq(out, "\n")
			for line := range lines {
				if strings.HasPrefix(strings.TrimSpace(line), "human:") &&
					strings.Contains(line, " at ") {
					t.Errorf("%q: human line shows clock time for midnight-default input: %s", tt.input, line)
				}
			}
		})
	}
}
