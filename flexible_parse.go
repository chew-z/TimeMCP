package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/araddon/dateparse"
)

// weekdayGroup is the shared alternation matching any weekday name or abbrev.
const weekdayGroup = `mon(?:day)?|tue(?:s|sday)?|wed(?:nesday)?|thu(?:r|rs|rsday)?|fri(?:day)?|sat(?:urday)?|sun(?:day)?`

// durationUnits is the shared alternation matching a duration unit (singular or plural).
const durationUnits = `minute|minutes|hour|hours|day|days|week|weeks|month|months|year|years`

var (
	weekdayPrefixRe = regexp.MustCompile(`(?i)^(` + weekdayGroup + `)\.?,?\s+`)
	ordinalSuffixRe = regexp.MustCompile(`(?i)\b(\d{1,2})(st|nd|rd|th)\b`)
	bareTimeRe      = regexp.MustCompile(`(?i)^\d{1,2}:\d{2}(:\d{2})?\s*(am|pm)?$`)
	numericDateRe   = regexp.MustCompile(`^\d{1,2}[/.]\d{1,2}(?:[/.]\d{2,4})?(?:[ T]\d{1,2}:\d{2}(?::\d{2})?\s*(?:[aApP][mM])?)?$`)
	looksRelativeRe = regexp.MustCompile(`(?i)\b(?:next|last|ago)\b|\bin\s+\d+`)

	// Relative-grammar matchers, compiled once from the shared alternations above.
	nextWeekdayRe = regexp.MustCompile(`^next\s+(` + weekdayGroup + `)$`)
	lastWeekdayRe = regexp.MustCompile(`^last\s+(` + weekdayGroup + `)$`)
	thisWeekdayRe = regexp.MustCompile(`^this\s+(` + weekdayGroup + `)$`)
	bareWeekdayRe = regexp.MustCompile(`^(` + weekdayGroup + `)$`)
	nextUnitRe    = regexp.MustCompile(`^next\s+(week|month|year)$`)
	lastUnitRe    = regexp.MustCompile(`^last\s+(week|month|year)$`)
	inDurationRe  = regexp.MustCompile(`^in\s+(\d+)\s+(` + durationUnits + `)$`)
	durationAgoRe = regexp.MustCompile(`^(\d+)\s+(` + durationUnits + `)\s+ago$`)

	weekdayAbbrevs = map[string]time.Weekday{
		"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday,
		"wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
	}
)

type timeCandidate struct {
	Time    time.Time
	Label   string // empty for unambiguous single result
	HasTime bool   // true when the input supplied a clock time (even explicit midnight)
}

// parseFlexibleDate parses a date/time string and returns one or more candidates.
// ambiguityNote is non-empty only when there are multiple candidates.
func parseFlexibleDate(input string, now time.Time) ([]timeCandidate, string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, "", fmt.Errorf("date string is empty")
	}

	// 1. Try relative grammar first
	t, hasTime, matched, err := parseRelativeDate(trimmed, now)
	if err != nil {
		return nil, "", err
	}
	if matched {
		return []timeCandidate{{Time: t, HasTime: hasTime}}, "", nil
	}

	// 2. Absolute parsing
	return parseAbsoluteDate(trimmed, now)
}

// ---------------------------------------------------------------------------
// Relative grammar
// ---------------------------------------------------------------------------

// parseRelativeDate attempts to match the input against the relative-date grammar.
// Returns (time, hasTime, true, nil) on match, (zero, false, false, nil) when no
// pattern matched, and (zero, false, false, error) when the input looks relative
// but didn't parse. hasTime is true when a clock time is implied (an explicit
// "at <time>" suffix, or a moment-level phrase like "now" / "in 2 hours").
func parseRelativeDate(input string, now time.Time) (time.Time, bool, bool, error) {
	loc := now.Location()
	lower := strings.ToLower(strings.TrimSpace(input))

	base, timeStr, hasTime := splitAtTime(lower)

	// t is the matched date; dayLevel means the phrase names a day (default midnight).
	t, dayLevel, ok := matchRelativePhrase(base, now, loc)

	if !ok {
		if looksRelative(lower) {
			return time.Time{}, false, false, fmt.Errorf(
				"%q looks like a relative date but could not be parsed. "+
					"Supported phrases: now, today, tomorrow, yesterday, "+
					"next/last/this <weekday>, bare <weekday>, "+
					"in N minutes/hours/days/weeks/months/years, N ... ago, "+
					"next/last week/month/year, "+
					"optionally suffixed with 'at <time>'",
				input,
			)
		}
		return time.Time{}, false, false, nil
	}

	// Apply time component.
	if hasTime {
		parsedTime, err := parseTimeComponent(timeStr, now)
		if err != nil {
			return time.Time{}, false, false, fmt.Errorf("cannot parse time in %q: %w", input, err)
		}
		t = time.Date(t.Year(), t.Month(), t.Day(),
			parsedTime.Hour(), parsedTime.Minute(), parsedTime.Second(), 0, loc)
	} else if dayLevel {
		t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	}

	// A moment-level phrase (not day-level, e.g. "now", "in 2 hours") also carries
	// a clock time even without an explicit "at" suffix.
	return t, hasTime || !dayLevel, true, nil
}

// midnightIn returns midnight for the given reference time's date in loc.
func midnightIn(ref time.Time, loc *time.Location) time.Time {
	return time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, loc)
}

// relativeMatcher attempts to match base against one grammar rule.
type relativeMatcher func(base string, now time.Time) (t time.Time, ok, dayLevel bool)

// relativeMatchers lists the grammar rules in order from most to least specific.
var relativeMatchers = []relativeMatcher{
	func(b string, n time.Time) (time.Time, bool, bool) { return matchNextLastWeekday(b, n, "next") },
	func(b string, n time.Time) (time.Time, bool, bool) { return matchNextLastWeekday(b, n, "last") },
	matchThisWeekday,
	func(b string, n time.Time) (time.Time, bool, bool) { return matchNextLastUnit(b, n, "next") },
	func(b string, n time.Time) (time.Time, bool, bool) { return matchNextLastUnit(b, n, "last") },
	matchInDuration,
	matchDurationAgo,
	matchBareWeekday,
}

// matchRelativePhrase resolves base against the keyword phrases and the ordered
// matcher chain. Returns (time, dayLevel, ok).
func matchRelativePhrase(base string, now time.Time, loc *time.Location) (time.Time, bool, bool) {
	switch base {
	case "now":
		return now, false, true
	case "today":
		return midnightIn(now, loc), true, true
	case "tomorrow":
		return midnightIn(now, loc).AddDate(0, 0, 1), true, true
	case "yesterday":
		return midnightIn(now, loc).AddDate(0, 0, -1), true, true
	}
	for _, match := range relativeMatchers {
		if t, ok, dayLevel := match(base, now); ok {
			return t, dayLevel, true
		}
	}
	return time.Time{}, false, false
}

// splitAtTime splits "base at time" into ("base", "time", true) when the
// suffix looks like a clock time, otherwise (input, "", false).
func splitAtTime(input string) (base, timeStr string, hasTime bool) {
	idx := strings.LastIndex(input, " at ")
	if idx < 0 {
		return input, "", false
	}
	candidate := strings.TrimSpace(input[idx+4:])
	if clockRe.MatchString(candidate) {
		return strings.TrimSpace(input[:idx]), candidate, true
	}
	return input, "", false
}

// clockRe matches times like "5pm", "5:00pm", "17:00", "5:00 PM", "17:00:00".
var clockRe = regexp.MustCompile(`(?i)^\d{1,2}(?::\d{2})?(?::\d{2})?\s*(am|pm)?$`)

// normTimeRe matches bare times like "5pm" or "5:30am" for normalization.
var normTimeRe = regexp.MustCompile(`(?i)^(\d{1,2})(?::(\d{2}))?\s*(am|pm)$`)

// parseTimeComponent parses a clock-time string.
func parseTimeComponent(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	// Normalize "5pm" → "5:00pm" — dateparse misparses bare "5pm" as seconds.
	if normTimeRe.MatchString(s) {
		m := normTimeRe.FindStringSubmatch(s)
		if m[2] == "" {
			s = m[1] + ":00" + m[3]
		}
	}
	today := now.Format("2006-01-02")
	t, err := dateparse.ParseIn(today+" "+s, now.Location())
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// matchNextLastWeekday matches "next monday" / "last friday".
func matchNextLastWeekday(base string, now time.Time, prefix string) (time.Time, bool, bool) {
	re := nextWeekdayRe
	if prefix == "last" {
		re = lastWeekdayRe
	}
	m := re.FindStringSubmatch(base)
	if m == nil {
		return time.Time{}, false, false
	}
	wd, _ := parseWeekdayAbbrev(m[1]) // m[1] matched the weekday regex group; always valid
	if prefix == "next" {
		return nextWeekday(now, wd, true), true, true
	}
	return lastWeekday(now, wd, true), true, true
}

// matchThisWeekday matches "this monday".
func matchThisWeekday(base string, now time.Time) (time.Time, bool, bool) {
	m := thisWeekdayRe.FindStringSubmatch(base)
	if m == nil {
		return time.Time{}, false, false
	}
	wd, _ := parseWeekdayAbbrev(m[1]) // regex-validated weekday
	days := int(wd) - int(now.Weekday())
	t := midnightIn(now, now.Location()).AddDate(0, 0, days)
	return t, true, true
}

// matchBareWeekday matches a bare weekday name ("monday", "mon").
func matchBareWeekday(base string, now time.Time) (time.Time, bool, bool) {
	m := bareWeekdayRe.FindStringSubmatch(base)
	if m == nil {
		return time.Time{}, false, false
	}
	wd, _ := parseWeekdayAbbrev(m[1]) // regex-validated weekday
	return nextWeekday(now, wd, false), true, true
}

// nextWeekday returns the next occurrence of wd. If strict is true, "next Monday"
// on Monday returns next week's Monday; otherwise bare "Monday" on Monday returns today.
func nextWeekday(now time.Time, wd time.Weekday, strict bool) time.Time {
	days := (int(wd) - int(now.Weekday()) + 7) % 7
	if days == 0 && strict {
		days = 7
	}
	return midnightIn(now, now.Location()).AddDate(0, 0, days)
}

// lastWeekday returns the most recent past occurrence of wd.
func lastWeekday(now time.Time, wd time.Weekday, strict bool) time.Time {
	days := (int(now.Weekday()) - int(wd) + 7) % 7
	if days == 0 && strict {
		days = 7
	}
	return midnightIn(now, now.Location()).AddDate(0, 0, -days)
}

// matchNextLastUnit matches "next week", "last month", "next year".
func matchNextLastUnit(base string, now time.Time, prefix string) (time.Time, bool, bool) {
	re := nextUnitRe
	dir := 1
	if prefix == "last" {
		re, dir = lastUnitRe, -1
	}
	m := re.FindStringSubmatch(base)
	if m == nil {
		return time.Time{}, false, false
	}
	mid := midnightIn(now, now.Location())
	switch m[1] {
	case "week":
		return mid.AddDate(0, 0, 7*dir), true, true
	case "month":
		return addMonthsClamped(mid, dir), true, true
	case "year":
		return addMonthsClamped(mid, 12*dir), true, true
	}
	return time.Time{}, false, false
}

// matchInDuration matches "in N minutes|hours|days|weeks|months|years".
func matchInDuration(base string, now time.Time) (time.Time, bool, bool) {
	m := inDurationRe.FindStringSubmatch(base)
	if m == nil {
		return time.Time{}, false, false
	}
	return applyDurationUnit(m[1], m[2], 1, now)
}

// matchDurationAgo matches "N minutes|hours|days|weeks|months|years ago".
func matchDurationAgo(base string, now time.Time) (time.Time, bool, bool) {
	m := durationAgoRe.FindStringSubmatch(base)
	if m == nil {
		return time.Time{}, false, false
	}
	return applyDurationUnit(m[1], m[2], -1, now)
}

// applyDurationUnit applies a signed count of a duration unit to now. sign is +1
// for future ("in N ...") and -1 for past ("N ... ago"). The bool result is
// dayLevel: false for clock-time units (minute/hour), true for calendar units.
func applyDurationUnit(count, unit string, sign int, now time.Time) (time.Time, bool, bool) {
	n, err := strconv.Atoi(count)
	if err != nil {
		return time.Time{}, false, false
	}
	n *= sign
	switch unit {
	case "minute", "minutes":
		return now.Add(time.Duration(n) * time.Minute), true, false
	case "hour", "hours":
		return now.Add(time.Duration(n) * time.Hour), true, false
	case "day", "days":
		return now.AddDate(0, 0, n), true, true
	case "week", "weeks":
		return now.AddDate(0, 0, n*7), true, true
	case "month", "months":
		return addMonthsClamped(now, n), true, true
	case "year", "years":
		return addMonthsClamped(now, n*12), true, true
	}
	return time.Time{}, false, false
}

// looksRelative returns true when the input contains tokens that suggest a
// relative-date phrase (even if we couldn't parse it).
func looksRelative(s string) bool {
	return looksRelativeRe.MatchString(s)
}

// parseWeekdayAbbrev converts a weekday name or abbreviation to a time.Weekday.
// The bool is false when s is not a recognized weekday, so callers passing
// unvalidated input can't silently receive a wrong day.
func parseWeekdayAbbrev(s string) (time.Weekday, bool) {
	s = strings.ToLower(s)
	if len(s) >= 3 {
		s = s[:3]
	}
	wd, ok := weekdayAbbrevs[s]
	return wd, ok
}

// ---------------------------------------------------------------------------
// Helpers for calendar arithmetic
// ---------------------------------------------------------------------------

// addMonthsClamped adds n months to t, clamping the day to the last valid day
// of the target month (so Jan 31 + 1 month → Feb 28/29, never Mar 3).
func addMonthsClamped(t time.Time, n int) time.Time {
	totalMonths := t.Year()*12 + int(t.Month()) - 1 + n
	newYear := totalMonths / 12
	newMonth := time.Month(totalMonths%12 + 1)
	newDay := t.Day()
	lastDay := daysInMonth(newYear, newMonth)
	if newDay > lastDay {
		newDay = lastDay
	}
	return time.Date(newYear, newMonth, newDay,
		t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// ---------------------------------------------------------------------------
// Absolute parsing
// ---------------------------------------------------------------------------

// parseAbsoluteDate parses an absolute date string, generating candidates for
// ambiguous inputs (weekday mismatch, mm/dd ambiguity, year-less far past).
func parseAbsoluteDate(input string, now time.Time) ([]timeCandidate, string, error) {
	loc := now.Location()

	// Normalize: strip weekday prefix and ordinal suffixes.
	weekdayMatch := weekdayPrefixRe.FindString(input)
	normalized := weekdayPrefixRe.ReplaceAllString(input, "")
	normalized = ordinalSuffixRe.ReplaceAllString(normalized, "$1")
	normalized = strings.TrimSpace(normalized)

	if normalized == "" {
		return nil, "", fmt.Errorf("date string is empty after normalization")
	}

	// Detect whether the original input carried a year and/or a clock time.
	hasYear := yearPresent(input)
	hasTime := clockRePresent.MatchString(input)

	// Bare time → prefix with today's date.
	useInput := normalized
	if bareTimeRe.MatchString(normalized) {
		today := now.Format("2006-01-02")
		useInput = today + " " + normalized
	}

	// Numeric slash/dot dates → dual reading. A year is present only when the
	// input has a third numeric component ("13/04/2026" vs "13/04").
	if numericDateRe.MatchString(normalized) {
		numericHasYear := strings.Count(normalized, "/")+strings.Count(normalized, ".") == 2
		return parseNumericDate(useInput, normalized, weekdayMatch, numericHasYear, hasTime, now, loc)
	}

	// Standard parse via dateparse.
	t, err := dateparse.ParseIn(useInput, loc)
	if err != nil {
		return nil, "", fmt.Errorf("could not parse %q as a date/time", input)
	}

	// Fill in year if dateparse left it as zero.
	if t.Year() == 0 {
		t = time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
		hasYear = false
	}

	// Check weekday mismatch.
	candidates, ambNote := checkWeekdayMismatch(t, weekdayMatch)

	// Check year-less far past.
	if len(candidates) == 1 && !hasYear {
		candidates, ambNote = checkYearlessFarPast(candidates, now)
	}

	setHasTime(candidates, hasTime)
	return candidates, ambNote, nil
}

// setHasTime marks every candidate with whether the input supplied a clock time.
func setHasTime(candidates []timeCandidate, hasTime bool) {
	for i := range candidates {
		candidates[i].HasTime = hasTime
	}
}

// yearRe matches a 4-digit year. Standalone 2-digit numbers are day-of-month,
// not years ("January 15" carries no year).
var yearRe = regexp.MustCompile(`\b\d{4}\b`)

// clockRePresent matches a clock time anywhere in the input (colon form like
// "22:08" or "5:30 PM", or a bare am/pm like "5pm"). Used to distinguish an
// explicitly supplied time — including explicit midnight — from a date-only input.
var clockRePresent = regexp.MustCompile(`(?i)\d{1,2}:\d{2}|\b\d{1,2}\s*[ap]m\b`)

// yearPresent returns true if the input contains a token that looks like a year.
func yearPresent(input string) bool {
	return yearRe.MatchString(input)
}

// parseNumericDate handles slash/dot-separated dates with dual mm/dd vs dd/mm reading.
func parseNumericDate(useInput, normalized, weekdayMatch string, hasYear, hasTime bool,
	now time.Time, loc *time.Location) ([]timeCandidate, string, error) {
	mf, errMF := dateparse.ParseIn(useInput, loc, dateparse.PreferMonthFirst(true))
	df, errDF := dateparse.ParseIn(useInput, loc, dateparse.PreferMonthFirst(false))

	if errMF != nil && errDF != nil {
		return nil, "", fmt.Errorf("could not parse %q as a date/time", normalized)
	}

	var candidates []timeCandidate
	switch {
	case errMF != nil:
		// Only day-first succeeded.
		candidates = []timeCandidate{{Time: fillYear(df, now, loc), Label: "day/month"}}
	case errDF != nil:
		// Only month-first succeeded.
		candidates = []timeCandidate{{Time: fillYear(mf, now, loc), Label: "month/day"}}
	case mf.Equal(df):
		// Both succeeded identically.
		candidates = []timeCandidate{{Time: fillYear(mf, now, loc)}}
	default:
		// Both succeeded differently → ambiguous.
		candidates = []timeCandidate{
			{Time: fillYear(mf, now, loc), Label: "month/day"},
			{Time: fillYear(df, now, loc), Label: "day/month"},
		}
	}

	if len(candidates) == 2 {
		setHasTime(candidates, hasTime)
		return candidates, fmt.Sprintf("ambiguous — %q could be month/day or day/month", normalized), nil
	}

	// Single candidate — check weekday mismatch, then year-less past.
	if wdCands, wdAmb := checkWeekdayMismatch(candidates[0].Time, weekdayMatch); len(wdCands) == 2 {
		setHasTime(wdCands, hasTime)
		return wdCands, wdAmb, nil
	}
	if !hasYear {
		newCands, amb := checkYearlessFarPast(candidates, now)
		setHasTime(newCands, hasTime)
		return newCands, amb, nil
	}

	setHasTime(candidates, hasTime)
	return candidates, "", nil
}

// fillYear fills in the current year if dateparse left year at zero.
func fillYear(t time.Time, now time.Time, loc *time.Location) time.Time {
	if t.Year() == 0 {
		return time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
	}
	return t
}

// checkWeekdayMismatch detects weekday-vs-date mismatches and generates two
// candidates when found: as-written date and the nearest occurrence of the
// stated weekday.
func checkWeekdayMismatch(t time.Time, weekdayMatch string) ([]timeCandidate, string) {
	if weekdayMatch == "" {
		return []timeCandidate{{Time: t}}, ""
	}

	want, ok := parseWeekdayAbbrev(strings.TrimSpace(strings.Trim(weekdayMatch, ", ")))
	if !ok || want == t.Weekday() {
		return []timeCandidate{{Time: t}}, ""
	}

	// Mismatch: two candidates.
	dateLabel := fmt.Sprintf("as written, %s", t.Format("January")+" "+strconv.Itoa(t.Day())+ordinal(t.Day()))

	// Find nearest occurrence of the stated weekday.
	daysAfter := (int(want) - int(t.Weekday()) + 7) % 7
	daysBefore := (int(t.Weekday()) - int(want) + 7) % 7

	var nearest time.Time
	var nearLabel string
	if daysAfter <= daysBefore {
		nearest = t.AddDate(0, 0, daysAfter)
		nearLabel = fmt.Sprintf("if you meant %s", nearest.Format("Monday"))
	} else {
		nearest = t.AddDate(0, 0, -daysBefore)
		nearLabel = fmt.Sprintf("if you meant %s", nearest.Format("Monday"))
	}

	ambNote := fmt.Sprintf("ambiguous — %s falls on a %s, not a %s",
		t.Format("January")+" "+strconv.Itoa(t.Day())+ordinal(t.Day())+" "+strconv.Itoa(t.Year()), t.Weekday(), want)

	return []timeCandidate{
		{Time: t, Label: dateLabel},
		{Time: nearest, Label: nearLabel},
	}, ambNote
}

// checkYearlessFarPast adds a next-year candidate when a year-less date is more
// than 30 days in the past (e.g., "January 5" said in July probably means next year).
func checkYearlessFarPast(candidates []timeCandidate, now time.Time) ([]timeCandidate, string) {
	// A single candidate may carry a reading label (e.g. "day/month" for "13/04");
	// that's still eligible for the next-year ambiguity.
	if len(candidates) != 1 {
		return candidates, ""
	}
	t := candidates[0].Time
	// Only relevant for year-less dates that are in the past.
	if t.Year() != now.Year() {
		return candidates, ""
	}

	days := daysBetweenDates(now, t)
	if days > -30 {
		return candidates, ""
	}

	// Far in the past — add next-year candidate.
	nextYear := time.Date(now.Year()+1, t.Month(), t.Day(),
		t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())

	ambNote := fmt.Sprintf("ambiguous — %s is in the past; did you mean next year?",
		t.Format("January")+" "+strconv.Itoa(t.Day())+ordinal(t.Day()))

	return []timeCandidate{
		{Time: t, Label: fmt.Sprintf("%s (this year)", t.Format("January")+" "+strconv.Itoa(t.Day())+ordinal(t.Day()))},
		{Time: nextYear, Label: fmt.Sprintf("%s (next year)", nextYear.Format("January")+" "+strconv.Itoa(nextYear.Day())+ordinal(nextYear.Day()))},
	}, ambNote
}
