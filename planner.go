package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/klippa-app/go-pdfium/requests"
)

var months = map[string]int{
	"Jan": 1, "Feb": 2, "Mar": 3, "Apr": 4, "May": 5, "Jun": 6,
	"Jul": 7, "Aug": 8, "Sep": 9, "Oct": 10, "Nov": 11, "Dec": 12,
}

var weekdays = map[string]bool{
	"Sunday": true, "Monday": true, "Tuesday": true, "Wednesday": true,
	"Thursday": true, "Friday": true, "Saturday": true,
}

// IndexPlannerDates scans the pages of a reMarkable planner template PDF and
// indexes dates matching "Month Day Weekday Notes/Day" into a nested mapping:
// map[YYYY-MM-DD]map[day|notes]pageIdx.
//
// Accepts *Document, string (file path), []byte, or io.Reader.
// For each matching date:
// - A page containing "Notes" in its header represents the "day" view (pointing to its notes companion).
// - A page containing "Day" in its header represents the "notes" view (pointing back to its day companion).
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
		textResp, err := doc.instance.GetPageText(&requests.GetPageText{
			Page: requests.Page{
				ByIndex: &requests.PageByIndex{
					Document: doc.handle.Document,
					Index:    idx,
				},
			},
		})
		if err != nil || textResp == nil {
			continue
		}
		text := textResp.Text

		tokens := strings.Fields(text)
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

		if hasNotes {
			if _, ok := indexed[dateKey]; !ok {
				indexed[dateKey] = make(map[string]int)
			}
			indexed[dateKey]["day"] = idx
		} else if hasDay {
			if _, ok := indexed[dateKey]; !ok {
				indexed[dateKey] = make(map[string]int)
			}
			indexed[dateKey]["notes"] = idx
		}
	}

	return indexed, nil
}
