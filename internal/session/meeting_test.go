package session

import (
	"strings"
	"testing"
)

func TestMeetingNotesSummaryAndCorrectionsSurviveReopen(t *testing.T) {
	s, v := sample(t)
	seg, err := s.Append(v.ID, Segment{Text: "ship Tuesday", Start: 1, End: 2, Source: "Microphone"})
	if err != nil {
		t.Fatal(err)
	}
	meeting, err := s.SaveMeetingNotes(v.ID, "Discuss launch · pakisend")
	if err != nil || meeting.Revision != 1 {
		t.Fatalf("save notes: %+v %v", meeting, err)
	}
	v, _ = s.Get(v.ID)
	fingerprint := MeetingFingerprint(v)
	meeting, err = s.SaveMeetingSummary(v.ID, "## Decisions\n\n- Ship Tuesday", "local", fingerprint)
	if err != nil || meeting.SummaryStale || meeting.Revision != 2 {
		t.Fatalf("save summary: %+v %v", meeting, err)
	}
	correction := "ship Wednesday"
	if err := s.Correct(v.ID, seg.ID, &correction); err != nil {
		t.Fatal(err)
	}
	reopened := &Store{Dir: s.Dir}
	got, err := reopened.Get(v.ID)
	if err != nil || got.Meeting == nil || !got.Meeting.SummaryStale || got.Meeting.Revision != 3 || !strings.Contains(got.Meeting.Notes, "pakisend") {
		t.Fatalf("meeting data lost: %+v %v", got.Meeting, err)
	}
	if err := s.Correct(v.ID, seg.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = reopened.Get(v.ID)
	if got.Meeting.SummaryStale || got.Meeting.Revision != 4 {
		t.Fatalf("restoring evidence did not restore freshness: %+v", got.Meeting)
	}
}

func TestMeetingSummaryMergesConcurrentNotesAndPreservesJournal(t *testing.T) {
	s, v := sample(t)
	_, _ = s.Append(v.ID, Segment{Text: "original evidence", Start: 1, End: 2})
	_, _ = s.SaveMeetingNotes(v.ID, "first notes")
	v, _ = s.Get(v.ID)
	fingerprint := MeetingFingerprint(v)
	_, _ = s.SaveMeetingNotes(v.ID, "edited during generation")
	_, _ = s.Append(v.ID, Segment{Text: "later evidence", Start: 2, End: 3})
	meeting, err := s.SaveMeetingSummary(v.ID, "Generated from first notes", "local", fingerprint)
	if err != nil || meeting.Notes != "edited during generation" || !meeting.SummaryStale || meeting.Revision != 3 {
		t.Fatalf("concurrent notes overwritten: %+v %v", meeting, err)
	}
	got, _ := s.Get(v.ID)
	if len(got.Segments) != 2 {
		t.Fatalf("journal changed: %+v", got.Segments)
	}
}

func TestMeetingAcceptedReviewUpdatesFreshnessAndRevision(t *testing.T) {
	s, v := sample(t)
	seg, _ := s.Append(v.ID, Segment{Text: "ship Tuesday", Start: 1, End: 2})
	_, _ = s.SaveMeetingNotes(v.ID, "launch")
	v, _ = s.Get(v.ID)
	_, _ = s.SaveMeetingSummary(v.ID, "summary", "local", MeetingFingerprint(v))
	_, _ = s.Suggest(v.ID, seg.ID, "ship Wednesday")
	if _, err := s.ResolveSuggestion(v.ID, seg.ID, true); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Get(v.ID)
	if !v.Meeting.SummaryStale || v.Meeting.Revision != 3 {
		t.Fatalf("accepted review did not invalidate summary: %+v", v.Meeting)
	}
}

func TestEnhancedNotesEditsPreserveOriginalNotesAndGenerationProvenance(t *testing.T) {
	s, v := sample(t)
	const original = "## Budget\n- Not approved yet.\n\n## Launch\n- Ask about the date."
	_, _ = s.SaveMeetingNotes(v.ID, original)
	_, _ = s.Append(v.ID, Segment{Text: "Launch is Wednesday.", Start: 1, End: 2})
	v, _ = s.Get(v.ID)
	generated, err := s.SaveMeetingSummary(v.ID, "generated", "local-qwen", MeetingFingerprint(v))
	if err != nil {
		t.Fatal(err)
	}
	edited, err := s.SaveEnhancedNotes(v.ID, "## Budget\n- Not approved yet.\n\n## Launch\n- Wednesday; ask Mira.")
	if err != nil || edited.Notes != original || edited.Summary == generated.Summary || edited.Revision != generated.Revision+1 {
		t.Fatalf("enhanced edit changed original or lost revision: %+v %v", edited, err)
	}
	if edited.SummaryModel != generated.SummaryModel || edited.SummaryFingerprint != generated.SummaryFingerprint || !edited.SummaryUpdated.Equal(generated.SummaryUpdated) || edited.SummaryStale {
		t.Fatalf("editing changed generation provenance: before=%+v after=%+v", generated, edited)
	}
	reopened := &Store{Dir: s.Dir}
	got, _ := reopened.Get(v.ID)
	if got.Meeting.Notes != original || got.Meeting.Summary != edited.Summary {
		t.Fatal("separate source and enhanced versions were not persisted")
	}
	_, _ = s.SaveMeetingNotes(v.ID, original+"\n- New question")
	edited, err = s.SaveEnhancedNotes(v.ID, edited.Summary+"\n- Follow up.")
	if err != nil || !edited.SummaryStale || edited.SummaryFingerprint != generated.SummaryFingerprint {
		t.Fatalf("editing incorrectly refreshed stale source evidence: %+v %v", edited, err)
	}
}
