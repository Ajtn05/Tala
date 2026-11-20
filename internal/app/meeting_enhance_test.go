package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"tala/internal/session"
)

func TestEnhancementKeepsUserOutlineWordingOrderAndNegation(t *testing.T) {
	const notes = "## Budget\n- No budget approval yet.\n\n## Launch\n- Need the date."
	v := session.Session{Meeting: &session.Meeting{Notes: notes}, Segments: []session.Segment{
		{Text: "We decided to launch Project Cedar on Wednesday. Budget is not approved. Mira will send the checklist by Friday.", Start: 1, End: 8},
	}}
	enhanced, err := enhanceMeeting(context.Background(), v, func(_ context.Context, prompt string) (noteEnhancement, error) {
		block := enhancementPromptBlock(t, prompt)
		result := noteEnhancement{}
		for _, line := range block.Lines {
			switch line.Text {
			case "- No budget approval yet.":
				result.Additions = append(result.Additions, noteAddition{AfterLine: line.Line, Text: "Budget remains unapproved.", Evidence: "Budget is not approved."})
			case "- Need the date.":
				result.Additions = append(result.Additions, noteAddition{AfterLine: line.Line, Text: "Project Cedar launches Wednesday.", Evidence: "We decided to launch Project Cedar on Wednesday."})
			}
		}
		result.Extras = []extraNoteFact{{Text: "Mira will send the checklist by Friday.", Evidence: "Mira will send the checklist by Friday."}}
		return result, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	last := -1
	for _, line := range strings.Split(notes, "\n") {
		if line == "" {
			continue
		}
		at := strings.Index(enhanced[last+1:], line)
		if at < 0 {
			t.Fatalf("original line lost or reordered: %q\n%s", line, enhanced)
		}
		last += at + 1
	}
	if v.Meeting.Notes != notes || strings.Contains(enhanced, "## Overview") || strings.Contains(enhanced, "## Key points") || !strings.Contains(enhanced, "  - Budget remains unapproved.") || !strings.Contains(enhanced, "Project Cedar launches Wednesday.") || strings.Count(enhanced, "Mira will send the checklist by Friday.") != 1 {
		t.Fatalf("enhancement did not follow the user's notes: %s", enhanced)
	}
}

func enhancementPromptBlock(t *testing.T, prompt string) sourceNoteBlock {
	t.Helper()
	const prefix = "Original note lines (keep their wording and structure):\n"
	end := strings.Index(prompt, "\n\nTranscript evidence:")
	var block sourceNoteBlock
	if !strings.HasPrefix(prompt, prefix) || end < 0 {
		t.Fatalf("missing source notes in enhancement request: %q", prompt)
	}
	if err := json.Unmarshal([]byte(prompt[len(prefix):end]), &block); err != nil {
		t.Fatal(err)
	}
	return block
}

func TestEnhancementCarriesNotesFocusThroughEveryTranscriptChunk(t *testing.T) {
	const notes = "## My priority\n- Keep the customer concern, not just launch dates."
	evidence := strings.Repeat("Background discussion. ", 900) + "TAIL customer concern remains unresolved."
	v := session.Session{Meeting: &session.Meeting{Notes: notes}, Segments: []session.Segment{{Text: evidence, End: 60}}}
	calls, sawTail := 0, false
	enhanced, err := enhanceMeeting(context.Background(), v, func(_ context.Context, prompt string) (noteEnhancement, error) {
		calls++
		if !strings.Contains(prompt, "Keep the customer concern, not just launch dates.") || !strings.Contains(prompt, "My priority") {
			t.Error("source notes focus absent from a transcript chunk")
		}
		if strings.Contains(prompt, "TAIL customer concern remains unresolved.") {
			sawTail = true
			return noteEnhancement{Additions: []noteAddition{{AfterLine: 1, Text: "Customer concern remains unresolved.", Evidence: "TAIL customer concern remains unresolved."}}}, nil
		}
		return noteEnhancement{}, nil
	})
	if err != nil || calls < 3 || !sawTail || !strings.Contains(enhanced, "Customer concern remains unresolved.") {
		t.Fatalf("long transcript evidence or notes focus lost: calls=%d tail=%v text=%q err=%v", calls, sawTail, enhanced, err)
	}
}

func TestLongNoteBlocksPreserveEveryByteAndLineIdentifier(t *testing.T) {
	notes := "## Priorities\n" + strings.Repeat("日本語 < & ", 1000) + "\n## Next\nLast note.\n"
	lines := strings.Split(notes, "\n")
	reassembled := make([]string, len(lines))
	blocks := sourceNoteBlocks(notes)
	for _, block := range blocks {
		encoded, _ := json.Marshal(block)
		if len(encoded) > 4000 {
			t.Fatalf("unbounded note context: %d", len(encoded))
		}
		for _, line := range block.Lines {
			if !utf8.ValidString(line.Text) {
				t.Fatal("split a Unicode character in user notes")
			}
			reassembled[line.Line] += line.Text
		}
	}
	if strings.Join(reassembled, "\n") != notes || len(blocks) < 3 {
		t.Fatal("long notes were truncated, reordered or changed")
	}
}

func TestEnhancementUsesEveryNoteBlockWithEveryTranscriptChunk(t *testing.T) {
	notes := "## First priority\n" + strings.Repeat("Keep this detailed concern.\n", 200) + "## Final priority\nDo not forget this question."
	v := session.Session{Meeting: &session.Meeting{Notes: notes}, Segments: []session.Segment{{Text: strings.Repeat("Spoken context. ", 1000) + "TAIL", End: 100}}}
	transcript := v
	transcript.Meeting = nil
	chunks := splitMeetingEvidence(meetingEvidence(transcript), meetingChunkBytes)
	blocks := sourceNoteBlocks(notes)
	seen := map[string]bool{}
	result, err := enhanceMeeting(context.Background(), v, func(_ context.Context, prompt string) (noteEnhancement, error) {
		seen[prompt] = true
		return noteEnhancement{}, nil
	})
	if err != nil || result != notes {
		t.Fatalf("source notes changed: %v", err)
	}
	for _, block := range blocks {
		encoded, _ := json.Marshal(block)
		for _, chunk := range chunks {
			prompt := "Original note lines (keep their wording and structure):\n" + string(encoded) + "\n\nTranscript evidence:\n" + chunk
			if !seen[prompt] {
				t.Fatal("a source note block did not receive a transcript chunk")
			}
		}
	}
}

func TestEnhancementRetainsDistinctFactsSharingTheSameEvidence(t *testing.T) {
	const quote = "We launch Wednesday and Mira sends the checklist Friday."
	text := renderEnhancedNotes("## Launch\nNeed date.", []noteAddition{{AfterLine: 1, Text: "Launch Wednesday.", Evidence: quote}}, []extraNoteFact{{Text: "Mira sends the checklist Friday.", Evidence: quote}})
	if !strings.Contains(text, "Launch Wednesday.") || !strings.Contains(text, "Mira sends the checklist Friday.") {
		t.Fatalf("different fact dropped because evidence was shared: %s", text)
	}
}

func TestEnhancementLeavesMarkdownListsAndGeneratedTextIntact(t *testing.T) {
	notes := "12. Review the **budget**\n13. Agree on next steps"
	result := renderEnhancedNotes(notes, []noteAddition{{AfterLine: 0, Text: "1. Use the ~~draft~~ label verbatim."}}, nil)
	want := "12. Review the **budget**\n    - 1\\. Use the \\~\\~draft\\~\\~ label verbatim.\n13. Agree on next steps"
	if result != want {
		t.Fatalf("addition broke the numbered outline or became Markdown syntax: %q", result)
	}
}

func TestEnhancementKeepsLargeGroundedResponses(t *testing.T) {
	var quotes []string
	response := noteEnhancement{Extras: []extraNoteFact{}}
	for week := 1; week <= 100; week++ {
		quote := fmt.Sprintf("Week %d: Mira will send the checklist.", week)
		quotes = append(quotes, quote)
		response.Additions = append(response.Additions, noteAddition{
			AfterLine: 0, Text: fmt.Sprintf("Mira committed to sending the release checklist for week %d.", week), Evidence: quote,
		})
	}
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	complete := meetingCompletion(func(context.Context, string, string, string, map[string]any) (string, error) {
		return string(body), nil
	})
	v := session.Session{Meeting: &session.Meeting{Notes: "Release checklists"}, Segments: []session.Segment{{Text: strings.Join(quotes, " ")}}}
	result, err := enhanceMeeting(context.Background(), v, func(ctx context.Context, source string) (noteEnhancement, error) {
		return complete.enhanceNotes(ctx, "List every checklist commitment.", source)
	})
	if err != nil || strings.Count(result, "\n- Mira committed") != len(quotes) || !strings.HasSuffix(result, "week 100.") {
		t.Fatalf("large enhancement lost additions: %d bytes, %v", len(result), err)
	}
}

func TestEnhancementCanClarifyAnUncertainNoteWithoutReplacingIt(t *testing.T) {
	const notes = "## Budget\n- budget approved?"
	v := session.Session{Meeting: &session.Meeting{Notes: notes}, Segments: []session.Segment{{Text: "The budget was not approved.", End: 1}}}
	text, err := enhanceMeeting(context.Background(), v, func(_ context.Context, prompt string) (noteEnhancement, error) {
		if !strings.Contains(prompt, "budget approved?") {
			t.Error("the original uncertain note was not provided")
		}
		return noteEnhancement{Additions: []noteAddition{{AfterLine: 1, Text: "Clarification: the budget was not approved.", Evidence: "The budget was not approved."}}}, nil
	})
	if err != nil || !strings.Contains(text, "- budget approved?\n  - Clarification: the budget was not approved.") || v.Meeting.Notes != notes {
		t.Fatalf("clarification replaced or changed source uncertainty: %q %v", text, err)
	}
}

func TestEnhancementRejectsUnsupportedFactsAndWrongNoteIdentifiers(t *testing.T) {
	v := session.Session{Meeting: &session.Meeting{Notes: "Need date"}, Segments: []session.Segment{{Text: "Launch Wednesday.", End: 1}}}
	for _, addition := range []noteAddition{
		{AfterLine: 3, Text: "Launch Wednesday.", Evidence: "Launch Wednesday."},
		{AfterLine: 0, Text: "Launch Friday.", Evidence: "Launch Friday."},
	} {
		if _, err := enhanceMeeting(context.Background(), v, func(context.Context, string) (noteEnhancement, error) {
			return noteEnhancement{Additions: []noteAddition{addition}}, nil
		}); err == nil {
			t.Fatalf("accepted unsupported addition: %+v", addition)
		}
	}
}

func TestEnhancementWithoutTranscriptKeepsNotesExactly(t *testing.T) {
	const notes = "## Budget\nNo approval yet.\n\nKeep this wording.\n"
	called := false
	result, err := enhanceMeeting(context.Background(), session.Session{Meeting: &session.Meeting{Notes: notes}}, func(context.Context, string) (noteEnhancement, error) {
		called = true
		return noteEnhancement{}, errors.New("must not invent details")
	})
	if err != nil || called || result != notes {
		t.Fatalf("notes without evidence changed: %q %v", result, err)
	}
}

func TestEnhancedNotesCannotBeEditedDuringGeneration(t *testing.T) {
	c, v := meetingController(t)
	_, _ = c.SaveMeetingNotes(v.ID, "Original notes")
	v, _ = c.Store.Get(v.ID)
	_, _ = c.Store.SaveMeetingSummary(v.ID, "Enhanced notes", "local", session.MeetingFingerprint(v))
	c.summaryID = v.ID
	if _, err := c.SaveEnhancedNotes(v.ID, "Manual edit"); err == nil {
		t.Fatal("manual enhanced edit raced generation")
	}
	c.summaryID = ""
	if edited, err := c.SaveEnhancedNotes(v.ID, "Manual edit"); err != nil || edited.Summary != "Manual edit" || edited.Notes != "Original notes" {
		t.Fatalf("enhanced version was not independently editable: %+v %v", edited, err)
	}
}
