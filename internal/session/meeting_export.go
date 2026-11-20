package session

import (
	"errors"
	"fmt"
	"strings"
)

// ExportMeeting exports the enhanced notebook when available, falling back to
// the user's original notes. Transcript and subtitle exports remain separate.
func ExportMeeting(v Session, format string) (string, error) {
	return exportMeetingNotes(v, format, false)
}

func ExportOriginalMeetingNotes(v Session, format string) (string, error) {
	return exportMeetingNotes(v, format, true)
}

func exportMeetingNotes(v Session, format string, original bool) (string, error) {
	if format != "txt" && format != "md" {
		return "", errors.New("meeting notes export supports TXT or Markdown")
	}
	var body string
	if v.Meeting != nil {
		body = v.Meeting.Notes
		if !original && strings.TrimSpace(v.Meeting.Summary) != "" {
			body = v.Meeting.Summary
		}
	}
	if strings.TrimSpace(body) == "" {
		return "", errors.New("save notes or enhance your notes before exporting")
	}
	var b strings.Builder
	heading := func(level int, title string) {
		if format == "md" {
			b.WriteString(strings.Repeat("#", level) + " ")
		}
		b.WriteString(title + "\n\n")
	}
	heading(1, strings.Join(strings.Fields(v.Title), " "))
	if !v.Created.IsZero() {
		fmt.Fprintf(&b, "%s\n\n", v.Created.Format("January 2, 2006, 3:04 PM MST"))
	}
	if format == "txt" {
		b.WriteString(strings.TrimSpace(body))
		return strings.TrimSpace(b.String()) + "\n", nil
	}
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	return b.String(), nil
}
