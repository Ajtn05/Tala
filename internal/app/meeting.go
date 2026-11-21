package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"tala/internal/models"
	"tala/internal/session"
)

type meetingSummaryState struct {
	Available    bool   `json:"available"`
	ModelID      string `json:"modelId"`
	Ready        bool   `json:"ready"`
	Busy         bool   `json:"busy"`
	SessionID    string `json:"sessionId"`
	Preview      string `json:"preview,omitempty"`
	Revision     uint64 `json:"revision"`
	Provider     string `json:"provider,omitempty"`
	ProviderName string `json:"providerName,omitempty"`
	Account      string `json:"account,omitempty"`
	AccountLabel string `json:"accountLabel,omitempty"`
	UsageLimited bool   `json:"usageLimited,omitempty"`
}

func (c *Controller) meetingSummaryLocked() (state meetingSummaryState) {
	state.Busy = c.summaryID != ""
	state.SessionID, state.Preview, state.Revision = c.summaryID, c.summaryPreview, c.summaryRevision
	if c.meetingTarget.Provider == chatGPTMeetingProvider {
		account := c.chatGPTAccountLocked(c.meetingTarget.Account)
		state.UsageLimited = c.chatGPTLimited[account.ID]
		state.Available = c.chatGPT != nil
		state.Ready = c.chatGPTDone == nil && account.Authorized && account.hasModel(c.meetingTarget.Model) && !state.UsageLimited
		state.ModelID, state.Provider, state.ProviderName = c.meetingTarget.Model, chatGPTMeetingProvider, "ChatGPT"
		state.Account, state.AccountLabel = c.meetingTarget.Account, account.Label
		return state
	}
	if provider, ok := lookupMeetingProvider(c.meetingTarget.Provider); ok {
		state.Available, state.Ready = c.meetingKeys != nil, c.meetingKeyReady[provider.ID]
		state.ModelID, state.Provider, state.ProviderName = c.meetingTarget.Model, provider.ID, provider.Name
		return state
	}
	state.Available = !c.headless && cleanupExecutable(c.Models.Exe) != ""
	state.ModelID, state.Ready = models.CleanupID, state.Available && c.modelStates[models.CleanupID].Path != ""
	return state
}

func (c *Controller) emitMeetingSummaryLocked() {
	c.summaryRevision++
	c.emitLocked("meetingSummary", c.meetingSummaryLocked())
}

type MeetingSummaryRequest struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Account  string `json:"account"`
}

var meetingPromptDefaults = models.MeetingPrompts{
	Summary: meetingInstructions, Enhancement: enhancementInstructions,
}

func (c *Controller) ConfigureMeetingPrompts(prompts models.MeetingPrompts) (models.MeetingPrompts, error) {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	prompts.Summary = strings.TrimSpace(prompts.Summary)
	prompts.Enhancement = strings.TrimSpace(prompts.Enhancement)
	// Store overrides only so resetting follows the defaults in future releases.
	if prompts.Summary == meetingPromptDefaults.Summary {
		prompts.Summary = ""
	}
	if prompts.Enhancement == meetingPromptDefaults.Enhancement {
		prompts.Enhancement = ""
	}
	settings := c.Models.Settings()
	settings.MeetingPrompts = prompts
	if err := c.Models.Save(settings); err != nil {
		return models.MeetingPrompts{}, err
	}
	c.Emit("settings", settings)
	return prompts, nil
}

func (c *Controller) emitMeeting(id string, meeting *session.Meeting) {
	c.Emit("meeting", map[string]any{"sessionId": id, "meeting": meeting})
	c.Emit("sessions", nil)
}

func (c *Controller) emitCurrentMeeting(id string) {
	if v, err := c.Store.Get(id); err == nil && v.Meeting != nil {
		c.emitMeeting(id, v.Meeting)
	}
}

func (c *Controller) SaveMeetingNotes(id, notes string) (*session.Meeting, error) {
	meeting, err := c.Store.SaveMeetingNotes(id, notes)
	if err == nil {
		c.emitMeeting(id, meeting)
	}
	return meeting, err
}

func (c *Controller) SaveEnhancedNotes(id, text string) (*session.Meeting, error) {
	c.mu.Lock()
	if c.summaryID == id {
		c.mu.Unlock()
		return nil, errors.New("wait for enhancement to finish or cancel it before editing enhanced notes")
	}
	meeting, err := c.Store.SaveEnhancedNotes(id, text)
	c.mu.Unlock()
	if err == nil {
		c.emitMeeting(id, meeting)
	}
	return meeting, err
}

func (c *Controller) CancelMeetingSummary(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.summaryID != "" && c.summaryID != id {
		return errors.New("a different meeting is being summarized")
	}
	if c.summaryCancel != nil {
		c.summaryCancel()
	}
	return nil
}

// GenerateMeetingSummary owns its runtime until return. The RPC caller may wait
// without blocking the native bridge, and cancellation also follows app shutdown.
func (c *Controller) GenerateMeetingSummary(request MeetingSummaryRequest) (*session.Meeting, error) {
	id := request.ID
	// Reserve generation under the same lock ordering as provider and key changes.
	c.settingsMu.Lock()
	prompts := c.Models.Settings().MeetingPrompts
	c.mu.Lock()
	c.settingsMu.Unlock()
	if err := c.ctx.Err(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if c.summaryID != "" || c.live.Active || c.activeJob != "" || c.modelBusy {
		c.mu.Unlock()
		return nil, errors.New("finish recording, imports, and model work before generating meeting notes")
	}
	state := c.meetingSummaryLocked()
	target := c.meetingTarget
	if request.Provider != target.Provider || request.Model != target.Model || request.Account != target.Account {
		c.mu.Unlock()
		return nil, errors.New("the enhancement provider changed. Check the selected model and try again")
	}
	if !state.Available {
		c.mu.Unlock()
		return nil, errors.New("local meeting summaries require the bundled Tala app")
	}
	if !state.Ready {
		c.mu.Unlock()
		if state.UsageLimited {
			return nil, errChatGPTLimit
		}
		if target.Provider == chatGPTMeetingProvider {
			return nil, errors.New("choose a connected ChatGPT account and model in Meeting notes settings")
		}
		if target.Provider != "" {
			return nil, errors.New("save the provider’s API key in Meeting notes settings")
		}
		return nil, errors.New("download Qwen3.5 4B Q4 in Settings before generating meeting notes")
	}
	v, err := c.Store.Get(id)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if v.Status == "processing" {
		c.mu.Unlock()
		return nil, errors.New("wait for this transcription to finish before generating meeting notes")
	}
	evidence := meetingEvidence(v)
	if strings.TrimSpace(evidence) == "" {
		c.mu.Unlock()
		return nil, errors.New("add notes or transcribe speech before generating meeting notes")
	}
	ctx, cancel := context.WithTimeout(c.ctx, 30*time.Minute)
	done := make(chan struct{})
	c.summaryID, c.summaryCancel, c.summaryDone = id, cancel, done
	model := c.modelStates[models.CleanupID]
	executable := cleanupExecutable(c.Models.Exe)
	generate := c.summaryGenerate
	c.emitMeetingSummaryLocked()
	c.mu.Unlock()
	defer func() {
		cancel()
		if target.Provider == chatGPTMeetingProvider {
			accounts := c.chatGPT.accounts()
			c.mu.Lock()
			c.chatGPTAccounts = accounts
			if errors.Is(err, errChatGPTLimit) {
				c.chatGPTLimited[target.Account] = true
			}
			c.emitLocked("meetingProviders", c.meetingProvidersLocked())
			c.mu.Unlock()
		}
		c.mu.Lock()
		c.summaryID, c.summaryCancel, c.summaryDone, c.summaryPreview = "", nil, nil, ""
		c.emitMeetingSummaryLocked()
		close(done)
		c.mu.Unlock()
		c.signal()
	}()

	var summary string
	preview := func(markdown string) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if markdown != "" && markdown != c.summaryPreview && ctx.Err() == nil {
			c.summaryPreview = markdown
			c.emitMeetingSummaryLocked()
		}
	}
	modelID := models.CleanupID
	if generate != nil {
		summary, err = generate(ctx, v)
	} else {
		var complete meetingCompletion
		if target.Provider == chatGPTMeetingProvider {
			complete = c.chatGPT.complete(meetingCloudClient, target)
			modelID = target.Provider + "/" + target.Model
		} else if target.Provider != "" {
			key, keyErr := c.meetingKeys.Load(target.Provider)
			if keyErr != nil {
				return nil, keyErr
			}
			complete = cloudMeetingCompletion(meetingCloudClient, target, key)
			modelID = target.Provider + "/" + target.Model
		} else {
			// Release idle Whisper memory before loading the local notes model.
			c.engineMu.Lock()
			c.closeEngine()
			c.engineMu.Unlock()
			runtime, runtimeErr := startLocalRuntime(ctx, executable, model.Path, 16384)
			if runtimeErr != nil {
				return nil, runtimeErr
			}
			defer runtime.close()
			complete = runtime.completeMeeting
		}
		if v.Meeting != nil && strings.TrimSpace(v.Meeting.Notes) != "" {
			instructions := prompts.Enhancement
			if instructions == "" {
				instructions = meetingPromptDefaults.Enhancement
			}
			summary, err = enhanceMeeting(ctx, v, func(ctx context.Context, source string, onPartial func(noteEnhancement)) (noteEnhancement, error) {
				return complete.enhanceNotes(ctx, instructions, source, onPartial)
			}, preview)
		} else {
			instructions := prompts.Summary
			if instructions == "" {
				instructions = meetingPromptDefaults.Summary
			}
			chunkBytes, combineContextBytes := meetingChunkBytes, meetingCombineContextBytes
			if target.Provider == "gemini" {
				// Gemini can handle larger inputs in both transcript and combination requests.
				chunkBytes, combineContextBytes = 48*1024, 49*1024
			}
			combineBytes := combineContextBytes - len(instructions)
			summary, err = summarizeMeeting(ctx, evidence, chunkBytes, combineBytes, func(ctx context.Context, source string, onPartial func(meetingDigest)) (meetingDigest, error) {
				return complete.meetingDigest(ctx, instructions, source, onPartial)
			}, preview)
		}
	}
	if err != nil {
		if target.Provider == "gemini" && !errors.Is(err, context.Canceled) {
			log.Printf("Gemini meeting enhancement failed (%s): %v", target.Model, err)
		}
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(summary) == "" {
		return nil, errors.New("the model returned empty meeting notes. Try again or choose another model")
	}
	meeting, err := c.Store.SaveMeetingSummary(id, summary, modelID, session.MeetingFingerprint(v))
	if err == nil {
		c.emitMeeting(id, meeting)
	}
	return meeting, err
}

func meetingEvidence(v session.Session) string {
	var b strings.Builder
	if v.Meeting != nil && strings.TrimSpace(v.Meeting.Notes) != "" {
		b.WriteString("USER'S MEETING NOTES\n")
		b.WriteString(v.Meeting.Notes)
		b.WriteString("\n\n")
	}
	for _, seg := range v.Segments {
		if text := strings.TrimSpace(session.Text(v, seg)); text != "" {
			fmt.Fprintf(&b, "[%s · %s] %s\n", session.Clock(seg.Start, false), seg.Source, text)
		}
	}
	return b.String()
}

const meetingInstructions = `You are an intelligent meeting note-taking assistant. Your task is to transform a raw transcript into clear, comprehensive, and naturally written meeting notes.

Write as if you were a skilled human note-taker who attended the entire conversation and understood what mattered.

Your goal is to capture the substance of the meeting, not merely shorten the transcript.

Identify what matters
Determine the main topics, explanations, questions, conclusions, and outcomes. Prioritize substantial information over small talk, repetition, and conversational filler. Preserve important context, reasoning, examples, numbers, dates, names, and technical details. Include meaningful information even if it was mentioned only briefly. Do not omit important details simply to make the notes shorter.

Organize by topic
Group related ideas together, even when discussed at different points. Arrange points in a logical order rather than strictly following the transcript. Use a separate entry for each distinct idea or supporting point. Connect explanations to the ideas they support. Use short paragraphs in the overview when an explanation requires more context. Allow the length to adapt to the meeting's actual content.

Write naturally
Use clear, direct, professional but conversational language. Write like personal notes prepared by an attentive participant. Avoid repetitive phrases such as "The speaker said," "It was discussed that," or "The meeting highlighted." Avoid generic corporate language, unnecessary introductions, and artificial conclusions. Make each point informative enough to be useful on its own.

Preserve accuracy
Only include information supported by the transcript. Never invent decisions, action items, deadlines, responsibilities, or conclusions. Clearly distinguish confirmed decisions from proposals, possibilities, and unresolved discussions. Preserve uncertainty and disagreement when relevant. Correct obvious transcription errors only when the intended meaning is clear. Handle Filipino-English code-switching naturally while retaining the original meaning.

Output format
Return only the JSON object matching the requested schema. Write plain text in every field. Tala adds the visible headings and list bullets. Omit Markdown heading markers, emphasis markers, code fences, HTML, and literal backslash escapes from field values. Use separate array entries instead of embedding bullet prefixes, numbering, nested lists, or standalone subheadings inside a value.

Overview
Open with a concise sentence identifying the primary subject. Describe the meeting's purpose and most important outcomes in plain prose, without a separate title or heading. Separate paragraphs with blank lines when useful. Avoid generic summaries.

Key points
Present substantive information in concise, detailed entries. Preserve explanations, reasoning, examples, and relevant context. Consolidate repeated points without losing distinct information. Prioritize readability without sacrificing completeness.

Decisions
Include confirmed decisions and agreements, if any.

Action items
Include specific tasks, owners, and deadlines where explicitly provided. Leave owner and due blank when unstated.

Open questions
Include unresolved issues or topics requiring further clarification, if any.

Use empty arrays for categories with no supported entries. When asked to combine partial notes, merge duplicate points while preserving distinct facts and uncertainties. Leave decisions and actions empty in a combination request because Tala collects them from the source chunks.

Quality standard
The finished notes should feel as though someone carefully listened to the entire conversation and thoughtfully organized what they learned. The reader should be able to understand the important discussions, supporting context, and outcomes without accessing the original transcript. Be comprehensive but avoid unnecessary verbosity. Never sacrifice meaningful information for brevity.`

type meetingAction struct {
	Task  string `json:"task"`
	Owner string `json:"owner"`
	Due   string `json:"due"`
}

type meetingDigest struct {
	Overview      string          `json:"overview"`
	KeyPoints     []string        `json:"keyPoints"`
	Decisions     []string        `json:"decisions"`
	Actions       []meetingAction `json:"actions"`
	OpenQuestions []string        `json:"openQuestions"`
}

// Bound input sizes conservatively for models with a small context window.
const meetingChunkBytes = 6000
const meetingCombineContextBytes = 11 * 1024

func splitMeetingEvidence(text string, chunkBytes int) []string {
	var chunks []string
	for start := 0; start < len(text); {
		end := min(len(text), start+chunkBytes)
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end--
		}
		if end < len(text) {
			windowStart := max(start, end-1500)
			window := text[windowStart:end]
			if boundary := strings.LastIndexByte(window, '\n'); boundary >= 0 {
				end = windowStart + boundary + 1
			} else if boundary := strings.LastIndexAny(window, ".!?"); boundary >= 0 {
				end = windowStart + boundary + 1
			} else {
				for at := end; at > windowStart; {
					r, size := utf8.DecodeLastRuneInString(text[start:at])
					if unicode.IsSpace(r) {
						end = at
						break
					}
					at -= size
				}
			}
		}
		chunks = append(chunks, text[start:end])
		if end == len(text) {
			break
		}
		// Repeat nearby context so names and commitments spanning a boundary
		// can be understood together. Deduplication removes repeated findings.
		next := max(start+1, end-300)
		for next < end && !utf8.RuneStart(text[next]) {
			next++
		}
		for next < end {
			r, size := utf8.DecodeRuneInString(text[next:end])
			next += size
			if unicode.IsSpace(r) {
				break
			}
		}
		start = next
	}
	return chunks
}

func meetingCommitments(digests []meetingDigest) ([]string, []meetingAction) {
	var decisions []string
	var actions []meetingAction
	seenDecisions, seenActions := map[string]bool{}, map[string]bool{}
	key := func(text string) string {
		return strings.ToLower(strings.Trim(strings.Join(strings.Fields(text), " "), " .!?"))
	}
	for _, digest := range digests {
		for _, decision := range digest.Decisions {
			if normalized := key(decision); normalized != "" && !seenDecisions[normalized] {
				seenDecisions[normalized] = true
				decisions = append(decisions, decision)
			}
		}
		for _, action := range digest.Actions {
			normalized := key(action.Task) + "\x00" + key(action.Owner) + "\x00" + key(action.Due)
			if key(action.Task) != "" && !seenActions[normalized] {
				seenActions[normalized] = true
				actions = append(actions, action)
			}
		}
	}
	return decisions, actions
}

func summarizeMeeting(ctx context.Context, evidence string, chunkBytes, combineBytes int, generate func(context.Context, string, func(meetingDigest)) (meetingDigest, error), preview func(string)) (string, error) {
	var partials []meetingDigest
	for i, part := range splitMeetingEvidence(evidence, chunkBytes) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var onPartial func(meetingDigest)
		if preview != nil {
			onPartial = func(digest meetingDigest) {
				drafts := append(partials, digest)
				decisions, actions := meetingCommitments(drafts)
				preview(renderMeetingDigests(drafts, decisions, actions))
			}
		}
		digest, err := generate(ctx, fmt.Sprintf("Meeting evidence, part %d:\n%s", i+1, part), onPartial)
		if err != nil {
			return "", err
		}
		partials = append(partials, digest)
	}
	// Commitments are preserved outside the lossy summary reduction. Keep the
	// transcript-pass preview visible until the combined result is saved.
	decisions, actions := meetingCommitments(partials)
	for i := range partials {
		partials[i].Decisions, partials[i].Actions = nil, nil
	}
	for len(partials) > 1 {
		var next []meetingDigest
		for i := 0; i < len(partials); i += 2 {
			if i+1 == len(partials) {
				next = append(next, partials[i])
				continue
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			pair := partials[i : i+2]
			data, _ := json.Marshal(pair)
			if len(data) > combineBytes {
				next = append(next, pair...)
				continue
			}
			digest, err := generate(ctx, "Combine these partial meeting notes into one grounded set of notes:\n"+string(data), nil)
			if err != nil {
				return "", err
			}
			digest.Decisions, digest.Actions = nil, nil
			next = append(next, digest)
		}
		if len(next) == len(partials) {
			break
		}
		partials = next
	}
	if len(partials) == 0 {
		return "", errors.New("no meeting evidence")
	}
	return renderMeetingDigests(partials, decisions, actions), nil
}

func meetingDigestSchema() map[string]any {
	text := map[string]any{"type": "string"}
	texts := map[string]any{"type": "array", "items": text}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"overview", "keyPoints", "decisions", "actions", "openQuestions"},
		"properties": map[string]any{
			"overview": text, "keyPoints": texts, "decisions": texts, "openQuestions": texts,
			"actions": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"task", "owner", "due"},
				"properties": map[string]any{"task": text, "owner": text, "due": text},
			}},
		},
	}
}

func (complete meetingCompletion) meetingDigest(ctx context.Context, instructions, evidence string, preview func(meetingDigest)) (meetingDigest, error) {
	var digest meetingDigest
	content, err := complete(ctx, "meeting_notes", instructions, "Meeting source data:\n"+evidence, meetingDigestSchema(), meetingJSONPreview(preview))
	if err != nil {
		return digest, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &fields); err != nil {
		return digest, errors.New("the model returned invalid meeting notes. Try again or choose another model")
	}
	for _, name := range []string{"overview", "keyPoints", "decisions", "actions", "openQuestions"} {
		if value, ok := fields[name]; !ok || string(value) == "null" {
			return digest, errors.New("the model returned incomplete meeting notes. Try again or choose another model")
		}
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&digest); err != nil {
		return digest, errors.New("the model returned invalid meeting notes. Try again or choose another model")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return digest, errors.New("the model returned invalid meeting notes. Try again or choose another model")
	}
	return digest, nil
}

func renderMeetingDigests(digests []meetingDigest, decisions []string, actions []meetingAction) string {
	var b strings.Builder
	var overviews, keyPoints, openQuestions []string
	for _, digest := range digests {
		if overview := meetingOverviewMarkdown(digest.Overview); overview != "" {
			overviews = append(overviews, overview)
		}
		keyPoints = append(keyPoints, digest.KeyPoints...)
		openQuestions = append(openQuestions, digest.OpenQuestions...)
	}
	if len(overviews) != 0 {
		if !meetingOverviewHeading.MatchString(overviews[0]) {
			b.WriteString("## Overview\n\n")
		}
		b.WriteString(strings.Join(overviews, "\n\n") + "\n\n")
	}
	section := func(title string, items []string) {
		var lines []string
		for _, text := range items {
			if text = meetingPlainText(text); text != "" {
				lines = append(lines, "- "+text)
			}
		}
		if len(lines) != 0 {
			b.WriteString("## " + title + "\n\n" + strings.Join(lines, "\n") + "\n\n")
		}
	}
	section("Key points", keyPoints)
	section("Decisions", decisions)
	var actionItems []string
	for _, action := range actions {
		text := action.Task
		if strings.TrimSpace(text) == "" {
			continue
		}
		if strings.TrimSpace(action.Owner) != "" {
			text += " · Owner: " + action.Owner
		}
		if strings.TrimSpace(action.Due) != "" {
			text += " · Due: " + action.Due
		}
		actionItems = append(actionItems, text)
	}
	section("Action items", actionItems)
	section("Open questions", openQuestions)
	return strings.TrimSpace(b.String())
}

var meetingOverviewHeading = regexp.MustCompile(`^ {0,3}\\?(#{1,6})[ \t]+([^\r\n]+)`)

func meetingOverviewMarkdown(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		// Keep model-provided heading markers and line breaks. Escape their text
		// the same way as the other generated fields before saving or exporting.
		if heading := meetingOverviewHeading.FindStringSubmatch(line); heading != nil && strings.TrimSpace(heading[2]) != "" {
			lines[i] = heading[1] + " " + meetingPlainText(heading[2])
		} else {
			lines[i] = meetingPlainText(line)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func meetingPlainText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	r := strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "&lt;", ">", "&gt;", "#", "\\#", "~", "\\~")
	text = r.Replace(text)
	if marker := notebookListMarker.FindStringSubmatch(text); marker != nil {
		punct := len(marker[2]) - 1
		text = text[:punct] + "\\" + text[punct:]
	}
	return text
}
