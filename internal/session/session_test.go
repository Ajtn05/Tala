package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func sample(t *testing.T) (*Store, Session) {
	t.Helper()
	s := &Store{Dir: t.TempDir()}
	v, e := s.Create("file", "Lecture", "/original/lecture.mp4")
	if e != nil {
		t.Fatal(e)
	}
	return s, v
}
func TestOrderingCorrectionAndExportPersistence(t *testing.T) {
	s, v := sample(t)
	later, e := s.Append(v.ID, Segment{Text: " second ", Start: 61.25, End: 63.5, Source: "System Audio"})
	if e != nil {
		t.Fatal(e)
	}
	_, _ = s.Append(v.ID, Segment{Text: "first", Start: 2, End: 4, Source: "Microphone"})
	correction := "pangalawa"
	if e = s.Correct(v.ID, later.ID, &correction); e != nil {
		t.Fatal(e)
	}
	reopened := &Store{Dir: s.Dir}
	got, e := reopened.Get(v.ID)
	if e != nil {
		t.Fatal(e)
	}
	if got.Segments[0].Text != "first" || got.Segments[1].Text != "second" {
		t.Fatalf("ordering or original text lost: %+v", got.Segments)
	}
	for _, tc := range []struct{ format, mode, want string }{{"txt", "clean", "first\n\npangalawa"}, {"txt", "timestamps", "[01:01] pangalawa"}, {"md", "sources", "[System Audio · 01:01]\npangalawa"}, {"srt", "clean", "00:01:01,250 --> 00:01:03,500"}, {"vtt", "clean", "WEBVTT\n\n1\n00:00:02.000 --> 00:00:04.000"}} {
		t.Run(tc.format+tc.mode, func(t *testing.T) {
			text, e := Export(got, tc.format, tc.mode)
			if e != nil || !strings.Contains(text, tc.want) {
				t.Fatalf("export: %q %v", text, e)
			}
		})
	}
	if e = s.Correct(v.ID, later.ID, nil); e != nil {
		t.Fatal(e)
	}
	got, _ = s.Get(v.ID)
	if Text(got, got.Segments[1]) != "second" {
		t.Fatal("restore original failed")
	}
}
func TestContinuousTextUsesCorrectionsWithoutLineBreaks(t *testing.T) {
	v := Session{Title: "Lecture", Corrections: map[string]string{"2": " corrected\r\ntext\t here "}, Segments: []Segment{
		{ID: "1", Text: " Hello\nworld. ", Start: 0, End: 1},
		{ID: "2", Text: "original", Start: 1, End: 2},
		{ID: "3", Text: " \n\t ", Start: 2, End: 3},
		{ID: "4", Text: "Salamat!", Untimed: true},
	}}
	text, err := Export(v, "txt", "continuous")
	if err != nil || text != "Hello world. corrected text here Salamat!" {
		t.Fatalf("continuous: %q %v", text, err)
	}
	text, err = Export(v, "txt", "clean")
	if err != nil || !strings.Contains(text, "\n\n") {
		t.Fatal("paragraph layout was lost")
	}
	v.Segments = v.Segments[:2]
	text, err = Export(v, "srt", "continuous")
	if err != nil || !strings.Contains(text, "00:00:01,000 --> 00:00:02,000") {
		t.Fatalf("subtitle timing changed: %q %v", text, err)
	}
}
func TestConcurrentAppendsDoNotOverwriteCorrections(t *testing.T) {
	s, v := sample(t)
	seg, _ := s.Append(v.ID, Segment{Text: "original", Start: 1, End: 2})
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if _, e := s.Append(v.ID, Segment{Text: fmt.Sprint(n), Start: float64(n + 2), End: float64(n + 3)}); e != nil {
				t.Error(e)
			}
		}(i)
	}
	corrected := "edited"
	if e := s.Correct(v.ID, seg.ID, &corrected); e != nil {
		t.Fatal(e)
	}
	wg.Wait()
	got, e := s.Get(v.ID)
	if e != nil || len(got.Segments) != 31 || got.Corrections[seg.ID] != "edited" {
		t.Fatalf("lost update: %d %v", len(got.Segments), e)
	}
}
func TestLegacyImportIsIdempotentAndOriginalSurvivesDelete(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	p := filepath.Join(s.Dir, "transcript_2026-01-01.txt")
	text := "[Microphone] [00:04 → 00:08] Hello\n\nplain text without timing\n"
	_ = os.WriteFile(p, []byte(text), 0600)
	for i := 0; i < 2; i++ {
		if e := s.ImportLegacy(); e != nil {
			t.Fatal(e)
		}
	}
	items, _ := s.List()
	if len(items) != 1 {
		t.Fatalf("duplicate import: %d", len(items))
	}
	v, _ := s.Get(items[0].ID)
	if len(v.Segments) != 2 || v.Segments[0].Untimed && v.Segments[1].Untimed {
		t.Fatal("legacy timing lost")
	}
	if _, e := Export(v, "srt", "clean"); e == nil {
		t.Fatal("invented timing for legacy text")
	}
	if e := s.Delete(v.ID); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(p)
	if e != nil || string(b) != text {
		t.Fatal("original legacy file removed")
	}
	if e = s.ImportLegacy(); e != nil {
		t.Fatal(e)
	}
	items, _ = s.List()
	if len(items) != 0 {
		t.Fatal("deleted legacy session reappeared")
	}
}
func TestInvalidIDsCannotEscapeStorage(t *testing.T) {
	s, _ := sample(t)
	if _, e := s.Get("../../other"); e == nil {
		t.Fatal("accepted traversal")
	}
	if e := s.Delete("../other"); e == nil {
		t.Fatal("accepted deletion outside store")
	}
}
func TestClockRoundingAndHours(t *testing.T) {
	if Clock(3661.9996, true) != "01:01:02.000" {
		t.Fatal(Clock(3661.9996, true))
	}
	if Clock(3661, false) != "01:01:01" {
		t.Fatal("hour formatting")
	}
}
func TestTruncatedJournalRetainsCompleteSegments(t *testing.T) {
	s, v := sample(t)
	_, _ = s.Append(v.ID, Segment{Text: "safe", Start: 1, End: 2})
	p, _ := s.path(v.ID, ".jsonl")
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = f.WriteString(`{"incomplete":`)
	f.Close()
	got, e := s.Get(v.ID)
	if e != nil || len(got.Segments) != 1 {
		t.Fatal("complete segment lost")
	}
	_, e = s.Append(v.ID, Segment{Text: "next", Start: 3, End: 4})
	if e != nil {
		t.Fatal(e)
	}
	got, _ = s.Get(v.ID)
	if len(got.Segments) != 2 {
		t.Fatal("a damaged tail swallowed the next segment")
	}
}
