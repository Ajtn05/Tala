package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"tala/internal/models"
	"tala/internal/session"
)

type meetingTestTransport func(*http.Request) (*http.Response, error)

func (f meetingTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHostedMeetingProviderProtocols(t *testing.T) {
	for _, tc := range []struct {
		provider, host, path, keyHeader, keyValue, schemaField, output string
	}{
		{"gemini", "generativelanguage.googleapis.com", "/v1beta/models/test-model:generateContent", "x-goog-api-key", "test-key", "responseJsonSchema",
			`{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"reasoning","thought":true},{"text":"{}"}]}}]}`},
		{"openai", "api.openai.com", "/v1/chat/completions", "Authorization", "Bearer test-key", "response_format",
			`{"choices":[{"finish_reason":"stop","message":{"content":"{}"}}]}`},
		{"anthropic", "api.anthropic.com", "/v1/messages", "x-api-key", "test-key", "output_config",
			`{"stop_reason":"end_turn","content":[{"type":"thinking"},{"type":"text","text":"{}"}]}`},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			status, output := http.StatusOK, tc.output
			client := &http.Client{Transport: meetingTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != tc.host || r.URL.Path != tc.path || r.URL.RawQuery != "" {
					t.Errorf("unexpected provider target: %s %s", r.Method, r.URL)
				}
				if r.Header.Get(tc.keyHeader) != tc.keyValue || r.Header.Get("Content-Type") != "application/json" {
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
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(output))}, nil
			})}
			complete := cloudMeetingCompletion(client, meetingTarget{Provider: tc.provider, Model: "test-model"}, "test-key")
			got, err := complete(context.Background(), "notes", "system instructions", "source transcript", meetingDigestSchema())
			if err != nil || got != "{}" {
				t.Fatalf("completion = %q, %v", got, err)
			}
			status, output = http.StatusTooManyRequests, "secret echoed by provider"
			_, err = complete(context.Background(), "notes", "system instructions", "source transcript", meetingDigestSchema())
			if err == nil || !strings.Contains(err.Error(), "quota") || strings.Contains(err.Error(), output) {
				t.Fatalf("quota failure was not sanitized: %v", err)
			}
		})
	}
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
					Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"{}"}]}}]}`)),
				}, nil
			})}
			complete := cloudMeetingCompletion(client, meetingTarget{Provider: "gemini", Model: "gemini-3.8-flash"}, "test-key")
			result, err := complete(ctx, "notes", "instructions", "transcript", meetingDigestSchema())
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
		result, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"finish_reason": "stop", "message": map[string]string{"content": wantContent},
		}}})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(result)))}, nil
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
		payload, _ := json.Marshal(map[string]any{
			"candidates": []any{map[string]any{
				"finishReason": finish, "content": map[string]any{"parts": []any{
					map[string]any{"text": "Internal reasoning", "thought": true},
					map[string]any{"text": strings.Repeat(" ", 6000) + digest},
				}},
			}},
			"usageMetadata": map[string]int{"totalTokenCount": 8200},
		})
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(payload)))}, nil
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
