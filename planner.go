package render

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var months = map[string]int{
	"Jan": 1, "Feb": 2, "Mar": 3, "Apr": 4, "May": 5, "Jun": 6,
	"Jul": 7, "Aug": 8, "Sep": 9, "Oct": 10, "Nov": 11, "Dec": 12,
}

var weekdays = map[string]bool{
	"Sunday": true, "Monday": true, "Tuesday": true, "Wednesday": true,
	"Thursday": true, "Friday": true, "Saturday": true,
}

// plannerHeader accepts a single-line header or the template's two-line
// navigation header. Only pairs of abbreviated weekdays and day numbers may
// precede the second line; arbitrary body text cannot extend the header.
func plannerHeader(text string) []string {
	first, rest, _ := strings.Cut(strings.TrimSpace(text), "\n")
	tokens := strings.Fields(first)
	if len(tokens) < 4 || len(tokens)%2 != 0 {
		return tokens
	}
	for i := 2; i < len(tokens); i += 2 {
		switch tokens[i] {
		case "Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat":
		default:
			return tokens
		}
		day, err := strconv.Atoi(tokens[i+1])
		if err != nil || day < 1 || day > 31 {
			return tokens
		}
	}
	second, _, _ := strings.Cut(rest, "\n")
	continuation := strings.Fields(second)
	if len(continuation) < 2 || !weekdays[continuation[0]] {
		return tokens
	}
	return append(tokens, continuation...)
}

// IndexPlannerDates scans the pages of a reMarkable planner template PDF and
// indexes dates matching "Month Day Weekday Notes/Day" into a nested mapping:
// map[YYYY-MM-DD]map[day|notes]pageIdx.
//
// Accepts *Document, string (file path), []byte, or io.Reader.
// For each matching date:
// - A page containing "Notes" in its header represents the "day" view (pointing to its notes companion).
// - A page containing "Day" in its header represents the "notes" view (pointing back to its day companion).
//
// The header is the first nonempty text line. When that line starts with a
// month and day followed only by abbreviated weekday/day navigation pairs,
// the next line also belongs to the header if it starts with a full weekday.
// Dates must be valid in year; printed years and weekday/date agreement are
// not checked. Notes takes precedence over Day, including holiday names.
// Duplicate views return an error naming both page indexes and no index.
// A page whose text carries no valid header is skipped. A failed PDFium text
// request for any page, including one that returns no response or one made on
// a released *Document, returns an error naming the 0-based page index and no
// index.
func IndexPlannerDates(pdfDocOrPath any, year int) (map[string]map[string]int, error) {
	if year <= 0 {
		return nil, ErrInvalidYear
	}

	doc, cleanup, err := resolveDocument(pdfDocOrPath)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	indexed := make(map[string]map[string]int)
	numPages := doc.NumPage()

	for idx := 0; idx < numPages; idx++ {
		text, err := doc.Text(idx)
		if err != nil {
			return nil, err
		}

		tokens := plannerHeader(text)
		if len(tokens) < 3 {
			continue
		}

		monthIdx, ok := months[tokens[0]]
		if !ok {
			continue
		}

		dayNum, err := strconv.Atoi(tokens[1])
		if err != nil || dayNum < 1 || dayNum > 31 {
			continue
		}
		date := time.Date(year, time.Month(monthIdx), dayNum, 0, 0, 0, 0, time.UTC)
		if date.Month() != time.Month(monthIdx) || date.Day() != dayNum {
			continue
		}

		hasWeekday := false
		hasNotes := false
		hasDay := false

		for _, t := range tokens {
			if weekdays[t] {
				hasWeekday = true
			}
			if t == "Notes" {
				hasNotes = true
			}
			if t == "Day" {
				hasDay = true
			}
		}

		if !hasWeekday {
			continue
		}

		dateKey := fmt.Sprintf("%04d-%02d-%02d", year, monthIdx, dayNum)

		kind := "day"
		if !hasNotes {
			if !hasDay {
				continue
			}
			kind = "notes"
		}
		if previous, exists := indexed[dateKey][kind]; exists {
			return nil, fmt.Errorf("duplicate %s view for %s on pages %d and %d", kind, dateKey, previous, idx)
		}
		if indexed[dateKey] == nil {
			indexed[dateKey] = make(map[string]int)
		}
		indexed[dateKey][kind] = idx
	}

	return indexed, nil
}
