package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Calendar arithmetic
// ---------------------------------------------------------------------------

// monthsElapsed returns whole calendar months between two times.
func monthsElapsed(later, earlier time.Time) int {
	months := (later.Year()-earlier.Year())*12 + int(later.Month()) - int(earlier.Month())
	if later.Day() < earlier.Day() {
		months--
	}
	if months < 0 {
		months = 0
	}
	return months
}

// daysBetweenDates returns the signed number of calendar days from a to b,
// ignoring time-of-day. Dates are re-anchored in UTC so DST transitions
// (23h/25h local days) can't truncate the count.
func daysBetweenDates(a, b time.Time) int {
	ad := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	bd := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	return int(bd.Sub(ad).Hours() / 24)
}

func pluralize(n int, unit string) string {
	if n == 1 {
		return unit
	}
	return unit + "s"
}

func ordinal(n int) string {
	if n%100 >= 11 && n%100 <= 13 {
		return "th"
	}
	switch n % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	default:
		return "th"
	}
}

// ---------------------------------------------------------------------------
// Block renderer
// ---------------------------------------------------------------------------

func formatDescribeTime(input string, candidates []timeCandidate, ambNote string, now time.Time) string {
	// A single candidate is unambiguous — render the normal top-level shape even
	// if it carries an annotation label (e.g. a numeric date with one valid reading).
	if len(candidates) == 1 {
		return formatSingle(input, candidates[0], now)
	}
	return formatMulti(input, candidates, ambNote, now)
}

func formatSingle(input string, c timeCandidate, now time.Time) string {
	// Inputs can carry their own offset (for example RFC 3339 `Z`), so render in the requested zone.
	c.Time = c.Time.In(now.Location())
	var b strings.Builder
	fmt.Fprintf(&b, "input:       %q\n", input)
	fmt.Fprintf(&b, "machine:     %s\n", c.Time.Format(time.RFC3339))
	fmt.Fprintf(&b, "human:       %s\n", formatHuman(c.Time, now, c.HasTime))
	fmt.Fprintf(&b, "tense:       %s\n", tenseLabel(c.Time, now))
	fmt.Fprintf(&b, "perspective: %s", perspectivePhrase(c.Time, now, c.HasTime))
	return b.String()
}

func formatMulti(input string, candidates []timeCandidate, ambNote string, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "input:       %q\n", input)
	fmt.Fprintf(&b, "status:      %s\n", ambNote)
	for i, c := range candidates {
		// Inputs can carry their own offset (for example RFC 3339 `Z`), so render in the requested zone.
		c.Time = c.Time.In(now.Location())
		b.WriteByte('\n')
		if c.Label != "" {
			fmt.Fprintf(&b, "candidate %d (%s):\n", i+1, c.Label)
		} else {
			fmt.Fprintf(&b, "candidate %d:\n", i+1)
		}
		fmt.Fprintf(&b, "  machine:     %s\n", c.Time.Format(time.RFC3339))
		fmt.Fprintf(&b, "  human:       %s\n", formatHuman(c.Time, now, c.HasTime))
		fmt.Fprintf(&b, "  tense:       %s\n", tenseLabel(c.Time, now))
		fmt.Fprintf(&b, "  perspective: %s\n", perspectivePhrase(c.Time, now, c.HasTime))
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---------------------------------------------------------------------------
// Human formatter
// ---------------------------------------------------------------------------

func formatHuman(t time.Time, now time.Time, hasTime bool) string {
	days := daysBetweenDates(now, t)

	var datePrefix string
	switch days {
	case 0:
		datePrefix = "today"
	case 1:
		datePrefix = "tomorrow"
	case -1:
		datePrefix = "yesterday"
	default:
		datePrefix = fmt.Sprintf("%s, %s %d%s",
			t.Format("Monday"), t.Format("January"), t.Day(), ordinal(t.Day()))
		if t.Year() != now.Year() {
			datePrefix += fmt.Sprintf(", %d", t.Year())
		}
	}

	if hasTime {
		timeStr := t.Format("3:04 PM")
		return datePrefix + " at " + timeStr
	}
	return datePrefix
}

// ---------------------------------------------------------------------------
// Tense
// ---------------------------------------------------------------------------

func tenseLabel(t, now time.Time) string {
	switch {
	case t.Equal(now):
		return "present"
	case t.After(now):
		return "future"
	default:
		return "past"
	}
}

// ---------------------------------------------------------------------------
// Perspective phrase
// ---------------------------------------------------------------------------

// dir selects the future or past phrasing.
func dir(future bool, futureStr, pastStr string) string {
	if future {
		return futureStr
	}
	return pastStr
}

func perspectivePhrase(t, now time.Time, hasTime bool) string {
	if t.Equal(now) {
		return "right now"
	}

	future := t.After(now)
	earlier, later := t, now
	if future {
		earlier, later = now, t
	}
	dur := later.Sub(earlier)

	if s, ok := intradayPhrase(t, now, dur, future, hasTime); ok {
		return s
	}
	return calendarPhrase(t, now, later, earlier, future, hasTime)
}

// intradayPhrase handles sub-90-minute spans and any same-day distance,
// returning ok=false to fall through to calendar-scale phrasing.
func intradayPhrase(t, now time.Time, dur time.Duration, future, hasTime bool) (string, bool) {
	switch {
	case dur < time.Minute:
		return "right now", true

	case dur < 45*time.Minute: // round to nearest 5 minutes
		mins := int(math.Round(float64(dur) / float64(time.Minute)))
		mins = max((mins+2)/5*5, 1)
		return dir(future,
			fmt.Sprintf("in about %d %s", mins, pluralize(mins, "minute")),
			fmt.Sprintf("about %d %s ago", mins, pluralize(mins, "minute"))), true

	case dur < 90*time.Minute && future && sameCalendarDay(t, now):
		return partOfDay(t) + ", about an hour from now", true

	case dur < 90*time.Minute:
		return dir(future, "about an hour from now", "about an hour ago"), true

	// A day-level phrase (e.g. "today", bare weekday matching today) names
	// the whole day, not a specific moment — "today" reads better than
	// "earlier overnight" for a candidate that's really just midnight.
	case sameCalendarDay(t, now) && !hasTime:
		return "today", true

	case sameCalendarDay(t, now):
		return dir(future, partOfDay(t), "earlier "+partOfDay(t)), true
	}
	return "", false
}

// calendarPhrase handles day-scale distances, delegating month/year spans.
func calendarPhrase(t, now, later, earlier time.Time, future, hasTime bool) string {
	abs := daysBetweenDates(now, t)
	if abs < 0 {
		abs = -abs
	}

	switch {
	case abs == 1:
		if hasTime {
			return dir(future, "tomorrow "+partOfDay(t), "yesterday "+partOfDay(t))
		}
		return dir(future, "tomorrow", "yesterday")
	case abs <= 6:
		return dir(future, "this coming "+t.Format("Monday"), "last "+t.Format("Monday"))
	case abs <= 13:
		return dir(future, "next week", "last week")
	case abs <= 28:
		return dir(future, "in a couple of weeks", "a few weeks ago")
	}
	return monthYearPhrase(t, later, earlier, future)
}

// monthYearPhrase handles spans of a month or more.
func monthYearPhrase(t, later, earlier time.Time, future bool) string {
	months := monthsElapsed(later, earlier)

	switch {
	case months < 1: // 29–30 days: past the weeks bucket but not a whole calendar month
		return dir(future, "in about a month", "about a month ago")

	case months <= 11:
		prefix := earlyMidLate(t.Day())
		return dir(future,
			fmt.Sprintf("in %s %s", prefix, t.Format("January")),
			fmt.Sprintf("last %s (%s)", t.Format("January"), prefix))

	case months <= 23:
		return dir(future,
			"next year ("+t.Format("January")+")",
			"last year ("+t.Format("January")+")")
	}

	years := months / 12
	return dir(future,
		fmt.Sprintf("in %d %s", years, pluralize(years, "year")),
		fmt.Sprintf("%d %s ago", years, pluralize(years, "year")))
}

func sameCalendarDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

func partOfDay(t time.Time) string {
	h := t.Hour()
	switch {
	case h < 6:
		return "overnight"
	case h < 9:
		return "morning"
	case h < 12:
		return "late morning"
	case h < 17:
		return "afternoon"
	case h < 21:
		return "evening"
	default:
		return "tonight"
	}
}

func earlyMidLate(day int) string {
	switch {
	case day <= 10:
		return "early"
	case day <= 20:
		return "mid"
	default:
		return "late"
	}
}
