package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"tala/internal/models"
	"tala/internal/session"
)

type meetingTestTransport func(*http.Request) (*http.Response, error)

func (f meetingTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func meetingTestEvent(body any) string {
	data, _ := json.Marshal(body)
	return "data: " + string(data) + "\n\n"
}

func chatMeetingTestEvent(text, finish string) string {
	return meetingTestEvent(map[string]any{"choices": []any{map[string]any{
		"finish_reason": finish, "delta": map[string]string{"content": text},
	}}})
}

func geminiMeetingTestEvent(text, finish string) string {
	return meetingTestEvent(map[string]any{"candidates": []any{map[string]any{
		"finishReason": finish, "content": map[string]any{"parts": []any{
			map[string]any{"text": "internal reasoning", "thought": true},
			map[string]any{"text": text},
		}},
	}}})
}

func claudeMeetingTestDelta(kind, text string) string {
	return meetingTestEvent(map[string]any{"type": "content_block_delta", "delta": map[string]string{"type": kind, "text": text}})
}

func TestHostedMeetingProviderProtocols(t *testing.T) {
	for _, tc := range []struct {
		provider, host, path, keyHeader, keyValue, schemaField, output string
	}{
		{"gemini", "generativelanguage.googleapis.com", "/v1beta/models/test-model:streamGenerateContent", "x-goog-api-key", "test-key", "responseJsonSchema",
			geminiMeetingTestEvent(`{"overview":"First"`, "") + geminiMeetingTestEvent(`}`, "STOP")},
		{"openai", "api.openai.com", "/v1/chat/completions", "Authorization", "Bearer test-key", "response_format",
			chatMeetingTestEvent(`{"overview":"First"`, "") + chatMeetingTestEvent(`}`, "") + chatMeetingTestEvent("", "stop")},
		{"anthropic", "api.anthropic.com", "/v1/messages", "x-api-key", "test-key", "output_config",
			claudeMeetingTestDelta("thinking_delta", "internal reasoning") +
				claudeMeetingTestDelta("text_delta", `{"overview":"First"`) + claudeMeetingTestDelta("text_delta", `}`) +
				"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			status, output := http.StatusOK, tc.output
			client := &http.Client{Transport: meetingTestTransport(func(r *http.Request) (*http.Response, error) {
				query := ""
				if tc.provider == "gemini" {
					query = "alt=sse"
				}
				if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != tc.host || r.URL.Path != tc.path || r.URL.RawQuery != query {
					t.Errorf("unexpected provider target: %s %s", r.Method, r.URL)
				}
				if r.Header.Get(tc.keyHeader) != tc.keyValue || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "text/event-stream" {
					t.Error("request headers did not match the provider protocol")
				}
				if tc.provider == "anthropic" && r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Error("missing Anthropic API version")
				}
				b, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				body := string(b)
				for _, want := range []string{"system instructions", "source transcript", tc.schemaField, "additionalProperties"} {
					if !strings.Contains(body, want) {
						t.Errorf("provider request missing %q", want)
					}
				}
				if strings.Contains(body, "test-key") || strings.Contains(body, "maxLength") {
					t.Error("provider body included a credential or unsupported schema constraint")
				}
				if tc.provider != "gemini" && !strings.Contains(body, `"stream":true`) {
					t.Error("request did not enable streaming")
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(output))}, nil
			})}
			complete := cloudMeetingCompletion(client, meetingTarget{Provider: tc.provider, Model: "test-model"}, "test-key")
			var previews []string
			got, err := complete(context.Background(), "notes", "system instructions", "source transcript", meetingDigestSchema(), func(text string) { previews = append(previews, text) })
			if err != nil || got != `{"overview":"First"}` || len(previews) != 2 || previews[0] != `{"overview":"First"` {
				t.Fatalf("completion = %q, %v", got, err)
			}
			status, output = http.StatusTooManyRequests, "secret echoed by provider"
			_, err = complete(context.Background(), "notes", "system instructions", "source transcript", meetingDigestSchema(), nil)
			if err == nil || !strings.Contains(err.Error(), "quota") || strings.Contains(err.Error(), output) {
				t.Fatalf("quota failure was not sanitized: %v", err)
			}
		})
	}
}

func TestMeetingPreviewIsGroundedTemporaryAndClearedOnCancellation(t *testing.T) {
	t.Setenv("TALA_DATA_DIR", t.TempDir())
	c, meeting := meetingController(t)
	c.meetingKeys, c.meetingKeyReady = testMeetingKeys{}, map[string]bool{}
	if _, err := c.ConfigureMeetingProvider("openai", "test-model", "test-key", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Store.Append(meeting.ID, session.Segment{Text: "Launch is Wednesday."}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SaveMeetingNotes(meeting.ID, "Launch date?"); err != nil {
		t.Fatal(err)
	}
	source, err := c.Store.Get(meeting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Store.SaveMeetingSummary(meeting.ID, "Previous recap", "old-model", session.MeetingFingerprint(source)); err != nil {
		t.Fatal(err)
	}
	previous := meetingCloudClient
	t.Cleanup(func() { meetingCloudClient = previous })
	body, writer := io.Pipe()
	defer body.Close()
	defer writer.Close()
	meetingCloudClient = &http.Client{Transport: meetingTestTransport(func(r *http.Request) (*http.Response, error) {
		go func() {
			<-r.Context().Done()
			writer.CloseWithError(r.Context().Err())
		}()
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})}
	const partial = `{"additions":[{"afterLine":0,"text":"Friday","evidence":"Launch is Friday."},{"afterLine":0,"text":"Wednesday","evidence":"Launch is Wednesday."},{"afterLine":0,"text":"still typing`
	go func() { _, _ = io.WriteString(writer, chatMeetingTestEvent(partial, "")) }()
	result := make(chan error, 1)
	go func() {
		_, err := c.GenerateMeetingSummary(MeetingSummaryRequest{ID: meeting.ID, Provider: "openai", Model: "test-model"})
		result <- err
	}()
	deadline := time.After(5 * time.Second)
	for preview := ""; preview == ""; {
		select {
		case event := <-c.Events:
			if state, ok := event.Data.(meetingSummaryState); ok {
				preview = state.Preview
			}
		case err := <-result:
			t.Fatalf("enhancement stopped before the preview: %v", err)
		case <-deadline:
			t.Fatal("no live preview before stream completion")
		}
		if preview != "" && preview != "Launch date?\n- Wednesday" {
			t.Fatalf("preview included incomplete or ungrounded notes: %q", preview)
		}
	}
	checkSaved := func(wantPreview string) {
		t.Helper()
		saved, err := c.Store.Get(meeting.ID)
		if err != nil || saved.Meeting.Summary != "Previous recap" {
			t.Fatalf("preview overwrote saved notes: %+v, %v", saved.Meeting, err)
		}
		snapshot := c.Snapshot().(map[string]any)["meetingSummary"].(meetingSummaryState)
		if snapshot.Preview != wantPreview || snapshot.Busy != (wantPreview != "") {
			t.Fatalf("unexpected snapshot: %+v", snapshot)
		}
	}
	checkSaved("Launch date?\n- Wednesday")
	if err := c.CancelMeetingSummary(meeting.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("enhancement did not cancel: %v", err)
		}
	case <-deadline:
		t.Fatal("enhancement did not exit after cancellation")
	}
	checkSaved("")
}

func TestGeminiRetriesOnlyServiceUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name, retryAfter, wantError string
		statuses                    []int
		calls                       int
		cancel                      bool
	}{
		{name: "recovers", retryAfter: "0", statuses: []int{503, 200}, calls: 2},
		{name: "stops after two retries", retryAfter: "0", statuses: []int{503, 503, 503}, calls: 3, wantError: "temporarily unavailable (HTTP 503)"},
		{name: "honors a long retry-after", retryAfter: "120", statuses: []int{503}, calls: 1, wantError: "temporarily unavailable (HTTP 503)"},
		{name: "does not repeat a quota error", retryAfter: "0", statuses: []int{429}, calls: 1, wantError: "quota or billing limit"},
		{name: "cancellation stops a retry", retryAfter: "0", statuses: []int{503}, calls: 1, cancel: true, wantError: "context canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var initialBody string
			calls := 0
			client := &http.Client{Transport: meetingTestTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls > len(tc.statuses) {
					t.Fatal("extra Gemini request")
				}
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatal(err)
				}
				if calls == 1 {
					initialBody = string(body)
				}
				if string(body) != initialBody || req.Method != http.MethodPost || req.Header.Get("x-goog-api-key") != "test-key" {
					t.Error("retry changed the original request")
				}
				if tc.cancel {
					cancel()
				}
				return &http.Response{
					StatusCode: tc.statuses[calls-1],
					Header:     http.Header{"Retry-After": {tc.retryAfter}},
					Body:       io.NopCloser(strings.NewReader(geminiMeetingTestEvent("{}", "STOP"))),
				}, nil
			})}
			complete := cloudMeetingCompletion(client, meetingTarget{Provider: "gemini", Model: "gemini-3.8-flash"}, "test-key")
			result, err := complete(ctx, "notes", "instructions", "transcript", meetingDigestSchema(), nil)
			if calls != tc.calls {
				t.Fatalf("made %d requests, want %d", calls, tc.calls)
			}
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("got error %v, want %q", err, tc.wantError)
				}
			} else if err != nil || result != "{}" {
				t.Fatalf("completion = %q, %v", result, err)
			}
		})
	}
}

type testMeetingKeys map[string]string

func (k testMeetingKeys) Has(id string) bool             { return k[id] != "" }
func (k testMeetingKeys) Load(id string) (string, error) { return k[id], nil }
func (k testMeetingKeys) Save(id, key string) error      { k[id] = key; return nil }
func (k testMeetingKeys) Remove(id string) error         { delete(k, id); return nil }

func TestHostedMeetingRequiresMatchingProviderAndKeepsKeysOutOfSettings(t *testing.T) {
	t.Setenv("TALA_DATA_DIR", t.TempDir())
	c, meeting := meetingController(t)
	c.modelStates = nil // Hosted enhancement works without a downloaded Qwen model.
	c.meetingKeys = testMeetingKeys{}
	c.meetingKeyReady = map[string]bool{}
	const key = "private-test-key"
	if before := c.meetingSummaryLocked(); before.ModelID != models.CleanupID || before.Provider != "" {
		t.Fatalf("fresh provider selection = %+v", before)
	}
	if _, err := c.ConfigureMeetingProvider("openai", "", "", ""); err == nil {
		t.Fatal("saved a cloud provider without a key")
	}
	settings, err := c.ConfigureMeetingProvider("openai", "", key, "")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(settings)
	disk, _ := os.ReadFile(c.Models.ConfigPath())
	if strings.Contains(string(encoded), key) || strings.Contains(string(disk), key) || !c.meetingSummaryLocked().Ready {
		t.Fatal("API key escaped Keychain or provider did not become ready")
	}
	if _, err := c.GenerateMeetingSummary(MeetingSummaryRequest{ID: meeting.ID}); err == nil || !strings.Contains(err.Error(), "provider changed") {
		t.Fatalf("accepted stale local selection: %v", err)
	}
	if _, err := c.Store.Append(meeting.ID, session.Segment{Text: "Ship on Wednesday."}); err != nil {
		t.Fatal(err)
	}
	prompts := models.MeetingPrompts{Summary: "Summarize in English and include five key points.", Enhancement: "Add the launch date.\nUse short phrases."}
	if _, err := c.ConfigureMeetingPrompts(prompts); err != nil {
		t.Fatal(err)
	}
	if err := c.Configure(models.Settings{Language: "en", Prompt: "Launch"}); err != nil {
		t.Fatal(err)
	}
	if saved := c.Models.Settings(); saved.MeetingPrompts != prompts || saved.MeetingProvider != "openai" {
		t.Fatalf("recognition save lost the meeting settings: %+v", saved)
	}
	wantInstructions := prompts.Summary
	wantContent := `{"overview":"Ship on Wednesday.","keyPoints":["First","Second","Third","Fourth","Fifth"],"decisions":[],"actions":[],"openQuestions":[]}`
	previous := meetingCloudClient
	t.Cleanup(func() { meetingCloudClient = previous })
	meetingCloudClient = &http.Client{Transport: meetingTestTransport(func(r *http.Request) (*http.Response, error) {
		var request struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if r.Header.Get("Authorization") != "Bearer "+key || len(request.Messages) != 2 {
			t.Error("missing credential header or transcript evidence")
		} else if request.Messages[0].Role != "system" || request.Messages[0].Content != wantInstructions ||
			request.Messages[1].Role != "user" || !strings.Contains(request.Messages[1].Content, "Ship on Wednesday.") {
			t.Errorf("instructions and evidence were not sent separately: %+v", request.Messages)
		}
		result := chatMeetingTestEvent(wantContent, "stop")
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(result))}, nil
	})}
	request := MeetingSummaryRequest{ID: meeting.ID, Provider: "openai", Model: c.meetingTarget.Model}
	if result, err := c.GenerateMeetingSummary(request); err != nil || !strings.Contains(result.Summary, "- Fifth") {
		t.Fatalf("hosted empty-notebook summary = %+v, %v", result, err)
	}
	if _, err := c.SaveMeetingNotes(meeting.ID, "Launch date"); err != nil {
		t.Fatal(err)
	}
	wantInstructions = prompts.Enhancement
	wantContent = `{"additions":[{"afterLine":0,"text":"Wednesday","evidence":"Ship on Wednesday."}],"extras":[]}`
	result, err := c.GenerateMeetingSummary(request)
	if err != nil || result.Summary != "Launch date\n- Wednesday" || result.SummaryModel != "openai/"+request.Model {
		t.Fatalf("hosted enhancement = %+v, %v", result, err)
	}
	if saved, err := c.ConfigureMeetingPrompts(models.MeetingPrompts{Summary: " \n", Enhancement: enhancementInstructions}); err != nil || saved != (models.MeetingPrompts{}) {
		t.Fatalf("restoring default prompts: %+v, %v", saved, err)
	}
	wantInstructions = enhancementInstructions
	if _, err := c.GenerateMeetingSummary(request); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RemoveMeetingProviderKey("openai"); err != nil || c.meetingSummaryLocked().Ready {
		t.Fatalf("removing the credential left the provider active: %v", err)
	}
	if _, err := c.GenerateMeetingSummary(request); err == nil {
		t.Fatal("used the provider after its key was removed")
	}
}

func TestGeminiSavesNotesFromLongTranscriptAndReportsIncompleteResponse(t *testing.T) {
	t.Setenv("TALA_DATA_DIR", t.TempDir())
	c, meeting := meetingController(t)
	c.meetingKeys = testMeetingKeys{}
	c.meetingKeyReady = map[string]bool{}
	const model = "gemini-3.8-flash"
	if _, err := c.ConfigureMeetingProvider("gemini", model, "test-key", ""); err != nil {
		t.Fatal(err)
	}
	const decision = "Ship the update on Wednesday."
	transcript := strings.Repeat("The team discussed the release process and reviewed feedback from the pilot.\n", 540) + decision
	if _, err := c.Store.Append(meeting.ID, session.Segment{Text: transcript, Source: "Microphone"}); err != nil {
		t.Fatal(err)
	}
	previous := meetingCloudClient
	t.Cleanup(func() { meetingCloudClient = previous })
	calls := 0
	overview := strings.Repeat("The team reviewed deployment feedback in detail. ", 550) + "Release discussion complete."
	finish, digest := "STOP", `{"overview":"`+overview+`","keyPoints":[],"decisions":["`+decision+`"],"actions":[],"openQuestions":[]}`
	meetingCloudClient = &http.Client{Transport: meetingTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{decision, `"thinkingLevel":"LOW"`, "Write plain text in every field."} {
			if !strings.Contains(string(body), want) {
				t.Errorf("Gemini request is missing %q", want)
			}
		}
		if strings.Contains(string(body), `"maxItems"`) {
			t.Error("Gemini received a fixed item limit from the default summary schema")
		}
		payload := geminiMeetingTestEvent(strings.Repeat(" ", 6000)+digest, finish)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}
	request := MeetingSummaryRequest{ID: meeting.ID, Provider: "gemini", Model: model}
	result, err := c.GenerateMeetingSummary(request)
	if err != nil || calls != 1 || result.SummaryModel != "gemini/"+model || !strings.Contains(result.Summary, decision) || !strings.Contains(result.Summary, overview) {
		t.Fatalf("long Gemini enhancement did not save in one request: %+v, %v, %d requests", result, err, calls)
	}
	finish, digest = "MAX_TOKENS", "partial response that should stay private"
	if _, err := c.GenerateMeetingSummary(request); err == nil || !strings.Contains(err.Error(), "reached its output limit") || strings.Contains(err.Error(), digest) {
		t.Fatalf("Gemini did not report a safe explanation: %v", err)
	}
	saved, err := c.Store.Get(meeting.ID)
	if err != nil || calls != 2 || saved.Meeting.Summary != result.Summary {
		t.Fatalf("incomplete response replaced the previous notes or was retried: %v, %d requests", err, calls)
	}
}
