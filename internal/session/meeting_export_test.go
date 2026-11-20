package session

import (
	"strings"
	"testing"
	"time"
)

func TestMeetingExportSelectsEnhancedOrOriginalNotesWithoutTranscript(t *testing.T) {
	v := Session{
		Title: "Planning\nmeeting", Created: time.Date(2026, 11, 15, 9, 0, 0, 0, time.FixedZone("PHT", 8*3600)),
		Meeting:  &Meeting{Notes: "Ask about the launch.", Summary: "## Decisions\n\n- Launch on Friday."},
		Segments: []Segment{{ID: "1", Text: "Raw transcript", Start: 0, End: 2}},
	}
	for _, format := range []string{"md", "txt"} {
		text, err := Export(v, format, "meeting")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Planning meeting", "November 15, 2026", v.Meeting.Summary} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s export missing %q: %s", format, want, text)
			}
		}
		if strings.Contains(text, "Raw transcript") || strings.Contains(text, v.Meeting.Notes) {
			t.Fatal("enhanced export duplicated original notes or included the transcript")
		}
		original, err := Export(v, format, "meeting-original")
		if err != nil || !strings.Contains(original, v.Meeting.Notes) || strings.Contains(original, v.Meeting.Summary) || strings.Contains(original, "Raw transcript") {
			t.Fatalf("original export mixed note versions: %q, %v", original, err)
		}
	}
	transcript, err := Export(v, "txt", "clean")
	if err != nil || strings.Contains(transcript, v.Meeting.Notes) || !strings.Contains(transcript, "Raw transcript") {
		t.Fatalf("transcript export changed: %q, %v", transcript, err)
	}
	for _, format := range []string{"srt", "vtt", "json", ""} {
		if _, err := Export(v, format, "meeting"); err == nil {
			t.Fatalf("accepted meeting export as %q", format)
		}
		if _, err := Export(v, format, "meeting-original"); err == nil {
			t.Fatalf("accepted original notes export as %q", format)
		}
	}
}

func TestMeetingExportAcceptsNotesOnlyAndRejectsEmpty(t *testing.T) {
	v := Session{Title: "Notes", Meeting: &Meeting{Notes: "Remember this"}}
	if text, err := ExportMeeting(v, "md"); err != nil || !strings.Contains(text, "Remember this") || strings.Contains(text, "AI summary") {
		t.Fatalf("notes-only export: %q, %v", text, err)
	}
	for _, meeting := range []*Meeting{nil, {}, {Notes: " \n", Summary: "\t"}} {
		v.Meeting = meeting
		if _, err := ExportMeeting(v, "md"); err == nil {
			t.Fatal("accepted empty meeting export")
		}
	}
	v.Meeting = &Meeting{Summary: "Enhanced only"}
	if _, err := ExportOriginalMeetingNotes(v, "md"); err == nil {
		t.Fatal("original export fell back to enhanced notes")
	}
}

func TestMarkdownMeetingExportKeepsIndentationAndTrailingWhitespace(t *testing.T) {
	const notes = "    indented code\n\nHard break  \n**Bold**\n\n"
	v := Session{Title: "Notebook", Meeting: &Meeting{Notes: notes}}
	text, err := ExportOriginalMeetingNotes(v, "md")
	if err != nil || text != "# Notebook\n\n"+notes {
		t.Fatalf("Markdown export changed the source: %q, %v", text, err)
	}
}
