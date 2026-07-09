package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseRelativeGrammar(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc) // Thursday

	cases := []struct {
		name     string
		input    string
		wantDate string // YYYY-MM-DD
		wantTime string // HH:MM (empty for midnight)
		wantErr  bool
	}{
		{"now", "now", "2026-07-09", "21:08", false},
		{"today", "today", "2026-07-09", "", false},
		{"tomorrow", "tomorrow", "2026-07-10", "", false},
		{"yesterday", "yesterday", "2026-07-08", "", false},
		{"next monday", "next monday", "2026-07-13", "", false},
		{"next mon", "next mon", "2026-07-13", "", false},
		{"next Tuesday (from Thurs)", "next tuesday", "2026-07-14", "", false},
		{"next Thursday (strict: next week)", "next thursday", "2026-07-16", "", false},
		{"last Monday", "last monday", "2026-07-06", "", false},
		{"last Friday (from Thurs)", "last friday", "2026-07-03", "", false},
		{"this Monday (past this week)", "this monday", "2026-07-06", "", false},
		{"this Friday (future this week)", "this friday", "2026-07-10", "", false},
		{"bare Monday", "monday", "2026-07-13", "", false},
		{"bare Friday (tomorrow)", "friday", "2026-07-10", "", false},
		{"bare Thursday (today)", "thursday", "2026-07-09", "", false},
		{"in 2 hours", "in 2 hours", "2026-07-09", "23:08", false},
		{"in 30 minutes", "in 30 minutes", "2026-07-09", "21:38", false},
		{"in 3 days", "in 3 days", "2026-07-12", "", false},
		{"in 1 week", "in 1 week", "2026-07-16", "", false},
		{"in 1 month", "in 1 month", "2026-08-09", "", false},
		{"in 2 years", "in 2 years", "2028-07-09", "", false},
		{"5 minutes ago", "5 minutes ago", "2026-07-09", "21:03", false},
		{"2 hours ago", "2 hours ago", "2026-07-09", "19:08", false},
		{"3 days ago", "3 days ago", "2026-07-06", "", false},
		{"1 month ago", "1 month ago", "2026-06-09", "", false},
		{"next week", "next week", "2026-07-16", "", false},
		{"last week", "last week", "2026-07-02", "", false},
		{"next month", "next month", "2026-08-09", "", false},
		{"last year", "last year", "2025-07-09", "", false},
		// With "at" suffix
		{"tomorrow at 17:00", "tomorrow at 17:00", "2026-07-10", "17:00", false},
		{"next tuesday at 5pm", "next tuesday at 5pm", "2026-07-14", "17:00", false},
		{"bare friday at noon", "friday at 12:00", "2026-07-10", "12:00", false},
		// Case insensitivity
		{"TOMORROW", "TOMORROW", "2026-07-10", "", false},
		{"Next Monday", "Next Monday", "2026-07-13", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			candidates, ambNote, err := parseFlexibleDate(c.input, now)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got candidates=%+v", candidates)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ambNote != "" {
				t.Fatalf("unexpected ambiguity note: %s", ambNote)
			}
			if len(candidates) != 1 {
				t.Fatalf("expected 1 candidate, got %d", len(candidates))
			}
			got := candidates[0].Time
			want := parseWant(c.wantDate, c.wantTime, loc)
			if !got.Equal(want) {
				t.Errorf("input=%q: got %s, want %s", c.input,
					got.Format("2006-01-02 15:04 MST"),
					want.Format("2006-01-02 15:04 MST"))
			}
		})
	}
}

func TestParseRelativeRejection(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc)

	// These should fail with an error (look relative but don't parse).
	cases := []string{
		"next someday",
		"in a while",
		"3 centuries ago",
	}

	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			_, _, err := parseFlexibleDate(input, now)
			if err == nil {
				t.Errorf("expected error for %q", input)
			}
		})
	}

	// These should fall through to absolute parsing (don't look relative).
	noMatch := []string{
		"Austin",
		"July 4th, 1776",
	}
	for _, input := range noMatch {
		t.Run(input, func(t *testing.T) {
			_, ambNote, err := parseFlexibleDate(input, now)
			if err != nil {
				t.Logf("absolute parse failed for %q: %v (this may be ok)", input, err)
				return
			}
			t.Logf("%q -> %d candidates, amb=%q", input, len(ambNote), ambNote)
		})
	}
}

func TestParseAbsoluteWeekdayMismatch(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc) // Thursday

	// July 20, 2026 is a Monday, not a Thursday.
	candidates, ambNote, err := parseFlexibleDate("Thursday, July 20th", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if ambNote == "" {
		t.Fatal("expected ambiguity note")
	}
	if !strings.Contains(ambNote, "Monday") {
		t.Errorf("ambiguity note should mention Monday: %s", ambNote)
	}

	// Candidate 1: as-written = July 20 (Monday)
	if !candidates[0].Time.Equal(time.Date(2026, 7, 20, 0, 0, 0, 0, loc)) {
		t.Errorf("candidate 1: got %s", candidates[0].Time.Format("2006-01-02"))
	}
	// Candidate 2: if you meant Thursday = July 23 (nearest Thursday)
	if !candidates[1].Time.Equal(time.Date(2026, 7, 23, 0, 0, 0, 0, loc)) {
		t.Errorf("candidate 2: got %s", candidates[1].Time.Format("2006-01-02"))
	}
}

func TestParseNumericDualReading(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc)

	// "13/04/2014" — only day-first is valid (month 13 doesn't exist).
	candidates, ambNote, err := parseFlexibleDate("13/04/2014", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Label != "day/month" {
		t.Errorf("expected 'day/month' label, got %q", candidates[0].Label)
	}
	if ambNote != "" {
		t.Errorf("expected no ambiguity note, got %q", ambNote)
	}
	want := time.Date(2014, 4, 13, 0, 0, 0, 0, loc)
	if !candidates[0].Time.Equal(want) {
		t.Errorf("got %s, want %s", candidates[0].Time.Format("2006-01-02"), want.Format("2006-01-02"))
	}

	// "04/02/2014" — ambiguous (could be Apr 2 or Feb 4).
	candidates, ambNote, err = parseFlexibleDate("04/02/2014", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if ambNote == "" {
		t.Fatal("expected ambiguity note")
	}
	if candidates[0].Label != "month/day" || candidates[1].Label != "day/month" {
		t.Errorf("unexpected labels: %q / %q", candidates[0].Label, candidates[1].Label)
	}

	// "07/07" — identical readings → single result.
	candidates, ambNote, err = parseFlexibleDate("07/07", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Label != "" {
		t.Errorf("expected no label, got %q", candidates[0].Label)
	}
	if ambNote != "" {
		t.Errorf("expected no ambiguity note, got %q", ambNote)
	}

	// "99/99" — invalid → error.
	_, _, err = parseFlexibleDate("99/99", now)
	if err == nil {
		t.Fatal("expected error for 99/99")
	}

	// Finding #1: numeric date carrying a clock time is still dual-read.
	candidates, ambNote, err = parseFlexibleDate("04/02/2014 10:00", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates for numeric date+time, got %d", len(candidates))
	}
	if ambNote == "" {
		t.Error("expected ambiguity note for numeric date+time")
	}
	if candidates[0].Time.Hour() != 10 || candidates[1].Time.Hour() != 10 {
		t.Errorf("expected clock time preserved in both readings: %s / %s",
			candidates[0].Time.Format("15:04"), candidates[1].Time.Format("15:04"))
	}
}

func TestFormatSingleLabeledNumeric(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc)

	// Finding #2: "13/04/2014" has one valid reading (day-first) but carries a
	// label; it must render as an unambiguous top-level result, not via formatMulti.
	candidates, ambNote, err := parseFlexibleDate("13/04/2014", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	out := formatDescribeTime("13/04/2014", candidates, ambNote, now)
	if strings.Contains(out, "status:") {
		t.Errorf("single result must not emit a status: line\nGot:\n%s", out)
	}
	if strings.Contains(out, "candidate ") {
		t.Errorf("single result must not emit candidate blocks\nGot:\n%s", out)
	}
	if !strings.HasPrefix(out, "input:") || !strings.Contains(out, "\nmachine:") {
		t.Errorf("expected top-level machine: shape\nGot:\n%s", out)
	}
}

func TestParseNumericWeekdayMismatch(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc)

	// 2026-04-13 is a Monday, not a Thursday — the numeric path must flag it too.
	candidates, ambNote, err := parseFlexibleDate("Thursday, 13/04/2026", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if !strings.Contains(ambNote, "Monday") {
		t.Errorf("ambiguity note should mention Monday: %s", ambNote)
	}
}

func TestNumericYearlessFarPast(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc) // July 9

	// "13/04" is April 13 (only day-first is valid) — yearless and ~3 months past.
	// It must still offer a next-year candidate, even though it carries a reading label.
	candidates, ambNote, err := parseFlexibleDate("13/04", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates (this year + next year), got %d", len(candidates))
	}
	if ambNote == "" {
		t.Error("expected a next-year ambiguity note")
	}
	if candidates[0].Time.Year() != 2026 || candidates[1].Time.Year() != 2027 {
		t.Errorf("expected 2026 then 2027, got %d then %d",
			candidates[0].Time.Year(), candidates[1].Time.Year())
	}
}

func TestExplicitMidnightHasTime(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc)

	// Explicit midnight must be flagged as time-carrying and rendered with a clock.
	cases := []struct {
		input           string
		wantHasTime     bool
		wantInHuman     string // "" means "must not contain ' at '"
		wantPerspective string // exact perspective line
	}{
		{"tomorrow at 00:00", true, "12:00 AM", "tomorrow overnight"},
		{"2026-07-11 00:00", true, "12:00 AM", "this coming Saturday"},
		{"tomorrow", false, "", "tomorrow"},
		{"next tuesday", false, "", "this coming Tuesday"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			candidates, _, err := parseFlexibleDate(c.input, now)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if candidates[0].HasTime != c.wantHasTime {
				t.Errorf("HasTime = %v, want %v", candidates[0].HasTime, c.wantHasTime)
			}
			out := formatDescribeTime(c.input, candidates, "", now)
			if c.wantInHuman != "" && !strings.Contains(out, c.wantInHuman) {
				t.Errorf("output missing %q\nGot:\n%s", c.wantInHuman, out)
			}
			if c.wantInHuman == "" && strings.Contains(out, " at ") {
				t.Errorf("date-only input should not show a clock time\nGot:\n%s", out)
			}
			// The perspective line must also reflect the explicit time (P3 covers both lines).
			if !strings.Contains(out, "perspective: "+c.wantPerspective) {
				t.Errorf("perspective line = want %q\nGot:\n%s", c.wantPerspective, out)
			}
		})
	}
}

func TestYearPresent(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"July 4th, 1776", true},
		{"January 15", false}, // 2-digit day is not a year
		{"January 5", false},
		{"22:08", false},
	}
	for _, c := range cases {
		if got := yearPresent(c.input); got != c.want {
			t.Errorf("yearPresent(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestParseYearlessFarPast(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc)

	// "January 5" — in the past by > 30 days → next-year candidate.
	candidates, ambNote, err := parseFlexibleDate("January 5", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if ambNote == "" {
		t.Fatal("expected ambiguity note")
	}
	// Candidate 1: this year (past)
	if candidates[0].Time.Year() != 2026 {
		t.Errorf("candidate 1 year: got %d", candidates[0].Time.Year())
	}
	// Candidate 2: next year
	if candidates[1].Time.Year() != 2027 {
		t.Errorf("candidate 2 year: got %d", candidates[1].Time.Year())
	}

	// "July 20" — only 11 days in future → single candidate, no ambiguity.
	candidates, ambNote, err = parseFlexibleDate("July 20", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if ambNote != "" {
		t.Errorf("expected no ambiguity note, got %q", ambNote)
	}
}

func TestParseCenturySpan(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 7, 9, 21, 8, 0, 0, loc)

	// "July 4th, 1776" — historical date, no ambiguity.
	candidates, ambNote, err := parseFlexibleDate("July 4th, 1776", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if ambNote != "" {
		t.Errorf("expected no ambiguity note, got %q", ambNote)
	}
	want := time.Date(1776, 7, 4, 0, 0, 0, 0, loc)
	if !candidates[0].Time.Equal(want) {
		t.Errorf("got %s, want %s", candidates[0].Time.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

func TestMonthClamping(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")

	// Jan 31 + 1 month → Feb 28 (2026 is not a leap year).
	jan31 := time.Date(2026, 1, 31, 12, 0, 0, 0, loc)
	got := addMonthsClamped(jan31, 1)
	if got.Month() != 2 || got.Day() != 28 {
		t.Errorf("Jan 31 + 1 month: got %s, want Feb 28", got.Format("2006-01-02"))
	}

	// Feb 29 (leap year) + 1 year → Feb 28 (non-leap year).
	feb29 := time.Date(2024, 2, 29, 12, 0, 0, 0, loc)
	got = addMonthsClamped(feb29, 12)
	if got.Day() != 28 {
		t.Errorf("Feb 29 + 1 year: got %s, want Feb 28", got.Format("2006-01-02"))
	}

	// Verify via parse: "next month" on Jan 31.
	now := time.Date(2026, 1, 31, 12, 0, 0, 0, loc)
	candidates, _, err := parseFlexibleDate("next month", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got = candidates[0].Time
	if got.Month() != 2 || got.Day() != 28 {
		t.Errorf("next month from Jan 31: got %s, want Feb 28", got.Format("2006-01-02"))
	}

	// "in 1 year" on Feb 29 (leap year).
	now = time.Date(2024, 2, 29, 12, 0, 0, 0, loc)
	candidates, _, err = parseFlexibleDate("in 1 year", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got = candidates[0].Time
	if got.Day() != 28 {
		t.Errorf("in 1 year from Feb 29: got %s, want Feb 28", got.Format("2006-01-02"))
	}
}

func TestDSTCrossing(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	// Just before spring-forward: 2026-03-07 21:08 EST.
	// Spring-forward is 2026-03-08 02:00 EST → 03:00 EDT.
	now := time.Date(2026, 3, 7, 21, 8, 0, 0, loc)

	candidates, _, err := parseFlexibleDate("in 2 days", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := candidates[0].Time
	// Should be March 9 at midnight (local), keeping the same wall-clock date.
	if got.Hour() != 0 || got.Minute() != 0 {
		t.Errorf("DST crossing: expected midnight, got %s", got.Format("15:04"))
	}
	if got.Day() != 9 || got.Month() != 3 || got.Year() != 2026 {
		t.Errorf("DST crossing: expected 2026-03-09, got %s", got.Format("2006-01-02"))
	}
}

func parseWant(datePart, timePart string, loc *time.Location) time.Time {
	d, _ := time.ParseInLocation("2006-01-02", datePart, loc)
	if timePart == "" {
		return d
	}
	t, _ := time.ParseInLocation("15:04", timePart, loc)
	return time.Date(d.Year(), d.Month(), d.Day(), t.Hour(), t.Minute(), 0, 0, loc)
}
