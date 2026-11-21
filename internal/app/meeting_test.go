package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"tala/internal/models"
	"tala/internal/session"
)

func meetingController(t *testing.T) (*Controller, session.Session) {
	t.Helper()
	c := controller(t)
	c.Events = make(chan Event, 100)
	c.Models.Exe = filepath.Join(t.TempDir(), "tala")
	runtime := filepath.Join(filepath.Dir(c.Models.Exe), "third_party", "build-llama", "bin", "llama-server")
	if err := os.MkdirAll(filepath.Dir(runtime), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtime, []byte("test runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	c.modelScanDone = true
	c.modelStates = map[string]models.Status{models.CleanupID: {ID: models.CleanupID, Path: "/verified/model", Managed: true}}
	v, err := c.Store.Create("meeting", "Launch meeting", "Microphone + System Audio")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Store.Update(v.ID, func(v *session.Session) error { v.Status = "completed"; return nil }); err != nil {
		t.Fatal(err)
	}
	return c, v
}

func TestMeetingSummaryUsesCorrectionsAndPreservesConcurrentNotes(t *testing.T) {
	c, v := meetingController(t)
	seg, _ := c.Store.Append(v.ID, session.Segment{Text: "ship Tuesday", Start: 1, End: 2})
	corrected := "ship Wednesday"
	_ = c.Store.Correct(v.ID, seg.ID, &corrected)
	_, _ = c.SaveMeetingNotes(v.ID, "Prepare launch")
	started, release := make(chan struct{}), make(chan struct{})
	c.summaryGenerate = func(ctx context.Context, input session.Session) (string, error) {
		evidence := meetingEvidence(input)
		if !strings.Contains(evidence, corrected) || strings.Contains(evidence, "ship Tuesday") || !strings.Contains(evidence, "Prepare launch") {
			t.Errorf("wrong summary evidence: %q", evidence)
		}
		close(started)
		<-release
		return "## Decisions\n\n- Ship Wednesday", nil
	}
	result := make(chan error, 1)
	go func() { _, err := c.GenerateMeetingSummary(MeetingSummaryRequest{ID: v.ID}); result <- err }()
	<-started
	_, err := c.SaveMeetingNotes(v.ID, "New notes while generating")
	if err != nil {
		t.Fatal(err)
	}
	if c.Start() == nil || c.RemoveModel(models.CleanupID) == nil || c.Delete(v.ID) == nil {
		t.Fatal("generation did not reserve runtime resources or protect target session")
	}
	if _, err := c.GenerateMeetingSummary(MeetingSummaryRequest{ID: v.ID}); err == nil {
		t.Fatal("accepted concurrent generation")
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	got, _ := c.Store.Get(v.ID)
	if got.Meeting.Notes != "New notes while generating" || !got.Meeting.SummaryStale || got.Meeting.SummaryModel != models.CleanupID {
		t.Fatalf("summary overwrote new evidence: %+v", got.Meeting)
	}
	c.mu.Lock()
	state := c.meetingSummaryLocked()
	c.mu.Unlock()
	if state.Busy || !state.Ready || !state.Available {
		t.Fatalf("wrong summary state: %+v", state)
	}
}

func TestMeetingSummaryCancellationKeepsPreviousSummary(t *testing.T) {
	c, v := meetingController(t)
	_, _ = c.SaveMeetingNotes(v.ID, "source notes")
	v, _ = c.Store.Get(v.ID)
	_, _ = c.Store.SaveMeetingSummary(v.ID, "previous summary", "old-model", session.MeetingFingerprint(v))
	started := make(chan struct{})
	c.summaryGenerate = func(ctx context.Context, _ session.Session) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	result := make(chan error, 1)
	go func() { _, err := c.GenerateMeetingSummary(MeetingSummaryRequest{ID: v.ID}); result <- err }()
	<-started
	if err := c.CancelMeetingSummary(v.ID); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	got, _ := c.Store.Get(v.ID)
	if got.Meeting.Summary != "previous summary" {
		t.Fatal("canceled generation changed summary")
	}
	if err := c.Delete(v.ID); err != nil {
		t.Fatalf("resource reservation not released: %v", err)
	}
}

func TestMeetingSummaryRejectsEmptyAndUnfinishedEvidence(t *testing.T) {
	c, v := meetingController(t)
	if _, err := c.GenerateMeetingSummary(MeetingSummaryRequest{ID: v.ID}); err == nil {
		t.Fatal("accepted empty evidence")
	}
	_, _ = c.SaveMeetingNotes(v.ID, "Evidence")
	_ = c.Store.Update(v.ID, func(v *session.Session) error { v.Status = "processing"; return nil })
	if _, err := c.GenerateMeetingSummary(MeetingSummaryRequest{ID: v.ID}); err == nil {
		t.Fatal("summarized an unfinished transcription")
	}
}

func TestMeetingChunksKeepBoundaryFactsAndValidUTF8(t *testing.T) {
	const fact = "Mira will send the launch checklist on Friday."
	evidence := strings.Repeat("Background discussion. ", 260) + fact + "\n" + strings.Repeat("Pagsubok 日本語. ", 800) + "TAIL_MARKER"
	chunks := splitMeetingEvidence(evidence, meetingChunkBytes)
	found := false
	for _, chunk := range chunks {
		if len(chunk) > meetingChunkBytes || !utf8.ValidString(chunk) {
			t.Fatalf("invalid chunk (%d bytes)", len(chunk))
		}
		found = found || strings.Contains(chunk, fact)
	}
	if !found || !strings.Contains(chunks[len(chunks)-1], "TAIL_MARKER") {
		t.Fatal("a boundary fact or the end of the meeting was lost")
	}
}

func TestMeetingReductionKeepsEveryDecisionAndAction(t *testing.T) {
	evidence := strings.Repeat("Meeting evidence. ", 900) + "TAIL_MARKER"
	maps, reductions := 0, 0
	text, err := summarizeMeeting(context.Background(), evidence, meetingChunkBytes, meetingCombineContextBytes-len(meetingInstructions), func(_ context.Context, input string, _ func(meetingDigest)) (meetingDigest, error) {
		if strings.HasPrefix(input, "Combine") {
			reductions++
			return meetingDigest{Overview: "Consolidated notes"}, nil
		}
		maps++
		digest := meetingDigest{Overview: "Part"}
		for n := 0; n < 7; n++ {
			digest.Decisions = append(digest.Decisions, fmt.Sprintf("Decision %d.%d", maps, n))
			digest.Actions = append(digest.Actions, meetingAction{Task: fmt.Sprintf("Task %d.%d", maps, n), Owner: "Mira"})
		}
		return digest, nil
	}, nil)
	if err != nil || maps < 2 || reductions != maps-1 {
		t.Fatalf("map/reduce did not process every part: maps=%d reductions=%d err=%v", maps, reductions, err)
	}
	for part := 1; part <= maps; part++ {
		for n := 0; n < 7; n++ {
			if !strings.Contains(text, fmt.Sprintf("Task %d.%d", part, n)) || !strings.Contains(text, fmt.Sprintf("Decision %d.%d", part, n)) {
				t.Fatalf("commitment omitted after reduction: %d.%d", part, n)
			}
		}
	}
}

func TestMeetingKeepsLargePartialSummariesWhenTheyCannotBeCombined(t *testing.T) {
	evidence := strings.Repeat("Meeting evidence. ", 900)
	var overviews []string
	result, err := summarizeMeeting(context.Background(), evidence, meetingChunkBytes, meetingCombineContextBytes-len(meetingInstructions), func(_ context.Context, input string, _ func(meetingDigest)) (meetingDigest, error) {
		if strings.HasPrefix(input, "Combine") {
			t.Fatal("sent a combination request larger than the input budget")
		}
		part := len(overviews) + 1
		overview := strings.Repeat("Meeting overview and discussion. ", 170) + fmt.Sprintf("End of part %d.", part)
		overviews = append(overviews, overview)
		return meetingDigest{
			Overview: overview, KeyPoints: []string{fmt.Sprintf("Key point %d", part)},
			Decisions: []string{fmt.Sprintf("Decision %d", part)}, Actions: []meetingAction{{Task: fmt.Sprintf("Task %d", part)}},
		}, nil
	}, nil)
	if err != nil || len(overviews) < 2 || !strings.HasPrefix(result, "## Overview\n\n"+strings.Join(overviews, "\n\n")) {
		t.Fatalf("long overviews were lost, truncated or reordered: %d parts, %v", len(overviews), err)
	}
	for i := range overviews {
		for _, prefix := range []string{"Key point", "Decision", "Task"} {
			if want := fmt.Sprintf("- %s %d\n", prefix, i+1); !strings.Contains(result+"\n", want) {
				t.Errorf("meeting notes omitted %q", want)
			}
		}
	}
}

func TestMeetingOverviewKeepsMarkdownHeadingsAndParagraphs(t *testing.T) {
	const overview = "\\# Craft of Programming\r\n\r\nThe first paragraph.\r\n\r\n## Next section\r\n\r\nThe <example> shows why."
	result := renderMeetingDigests([]meetingDigest{{Overview: overview}}, []string{"Ship on Friday."}, nil)
	const want = "# Craft of Programming\n\nThe first paragraph.\n\n## Next section\n\nThe &lt;example&gt; shows why.\n\n## Decisions\n\n- Ship on Friday."
	if result != want {
		t.Fatalf("generated overview lost its Markdown structure: %q", result)
	}
}

func TestMeetingRuntimeRequiresCompleteStructuredOutput(t *testing.T) {
	overview := strings.Repeat("The team discussed the launch timeline and reviewed the remaining tasks. ", 80) + "The launch is scheduled for Wednesday."
	content, err := json.Marshal(meetingDigest{
		Overview: overview, KeyPoints: []string{"First", "Second", "Third", "Fourth", "Fifth"},
		Decisions: []string{"Ship Wednesday"}, Actions: []meetingAction{}, OpenQuestions: []string{"Who?", "When?", "Where?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{"stop", "length"} {
		t.Run(reason, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					w.WriteHeader(http.StatusOK)
					return
				}
				var payload struct {
					Messages       []struct{ Role, Content string }
					ResponseFormat json.RawMessage `json:"response_format"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if len(payload.Messages) != 2 || payload.Messages[0].Role != "system" || payload.Messages[0].Content != meetingPromptDefaults.Summary ||
					payload.Messages[1].Role != "user" || !strings.Contains(payload.Messages[1].Content, "SOURCE_MARKER") {
					t.Error("default summary instructions and source were not sent separately")
				}
				if strings.Contains(string(payload.ResponseFormat), `"maxLength"`) || strings.Contains(string(payload.ResponseFormat), `"maxItems"`) {
					t.Error("empty-notebook schema still imposes a fixed length or item limit")
				}
				_, _ = fmt.Fprint(w, chatMeetingTestEvent(string(content), reason))
			}))
			defer server.Close()
			runtime := &cleanupRuntime{client: &http.Client{Transport: rewriteMeetingTransport{url: server.URL}}, done: make(chan struct{})}
			digest, err := meetingCompletion(runtime.completeMeeting).meetingDigest(context.Background(), meetingInstructions, "SOURCE_MARKER", nil)
			if reason == "stop" && (err != nil || digest.Overview != overview || len(digest.Decisions) != 1 || len(digest.KeyPoints) != 5 || len(digest.OpenQuestions) != 3) {
				t.Fatalf("valid summary: %+v %v", digest, err)
			}
			if reason == "length" && err == nil {
				t.Fatal("accepted truncated summary")
			}
		})
	}
}

type rewriteMeetingTransport struct{ url string }

func (r rewriteMeetingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	target, _ := http.NewRequest(req.Method, r.url+req.URL.RequestURI(), nil)
	clone.URL = target.URL
	return http.DefaultTransport.RoundTrip(clone)
}

func TestMeetingSummaryCloseCancelsAndJoinsGeneration(t *testing.T) {
	c, v := meetingController(t)
	c.workerDone = make(chan struct{})
	close(c.workerDone)
	_, _ = c.SaveMeetingNotes(v.ID, "Source notes")
	started := make(chan struct{})
	c.summaryGenerate = func(ctx context.Context, _ session.Session) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	result := make(chan error, 1)
	go func() { _, err := c.GenerateMeetingSummary(MeetingSummaryRequest{ID: v.ID}); result <- err }()
	<-started
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not join summary generation")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown result: %v", err)
	}
	if _, err := c.GenerateMeetingSummary(MeetingSummaryRequest{ID: v.ID}); !errors.Is(err, context.Canceled) {
		t.Fatalf("generation accepted after shutdown: %v", err)
	}
}
