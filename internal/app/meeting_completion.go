package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type meetingCompletion func(ctx context.Context, name, instructions, evidence string, schema map[string]any) (string, error)

func (r *cleanupRuntime) completeMeeting(ctx context.Context, name, instructions, evidence string, schema map[string]any) (string, error) {
	if err := r.ready(ctx); err != nil {
		return "", err
	}
	payload := map[string]any{
		"messages":    chatMeetingMessages(instructions, evidence),
		"temperature": 0, "max_tokens": 2048, "response_format": chatMeetingFormat(name, schema),
	}
	return postMeeting(ctx, r.client.Do, "http://tala.local/v1/chat/completions", "The local model", nil, payload, decodeChatMeeting)
}

var meetingCloudClient = &http.Client{
	Timeout: 120 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func cloudMeetingCompletion(client *http.Client, target meetingTarget, key string) meetingCompletion {
	provider, _ := lookupMeetingProvider(target.Provider)
	return func(ctx context.Context, name, instructions, evidence string, schema map[string]any) (string, error) {
		switch provider.ID {
		case "gemini":
			config := map[string]any{
				"responseMimeType": "application/json", "responseJsonSchema": cloudMeetingSchema(schema), "maxOutputTokens": 8192,
			}
			if target.Model == "gemini-3.8-flash" {
				config["thinkingConfig"] = map[string]string{"thinkingLevel": "LOW"}
			}
			payload := map[string]any{
				"systemInstruction": map[string]any{"parts": []map[string]string{{"text": instructions}}},
				"contents":          []map[string]any{{"role": "user", "parts": []map[string]string{{"text": evidence}}}},
				"generationConfig":  config,
			}
			endpoint := "https://generativelanguage.googleapis.com/v1beta/models/" + target.Model + ":generateContent"
			return postMeeting(ctx, func(req *http.Request) (*http.Response, error) {
				return sendGeminiMeeting(client, req)
			}, endpoint, provider.Name, map[string]string{"x-goog-api-key": key}, payload, decodeGeminiMeeting)
		case "openai":
			payload := map[string]any{
				"model": target.Model, "messages": chatMeetingMessages(instructions, evidence),
				"max_completion_tokens": 8192, "response_format": chatMeetingFormat(name, cloudMeetingSchema(schema)),
			}
			return postMeeting(ctx, client.Do, "https://api.openai.com/v1/chat/completions", provider.Name,
				map[string]string{"Authorization": "Bearer " + key}, payload, decodeChatMeeting)
		case "anthropic":
			payload := map[string]any{
				"model": target.Model, "system": instructions, "max_tokens": 4096,
				"messages":      []map[string]string{{"role": "user", "content": evidence}},
				"output_config": map[string]any{"format": map[string]any{"type": "json_schema", "schema": cloudMeetingSchema(schema)}},
			}
			return postMeeting(ctx, client.Do, "https://api.anthropic.com/v1/messages", provider.Name,
				map[string]string{"x-api-key": key, "anthropic-version": "2023-06-01"}, payload, decodeClaudeMeeting)
		default:
			return "", errors.New("choose an available meeting notes provider")
		}
	}
}

func sendGeminiMeeting(client *http.Client, req *http.Request) (*http.Response, error) {
	const maxRetries = 2
	ctx := req.Context()
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		res, err := client.Do(req)
		if err != nil || res.StatusCode != http.StatusServiceUnavailable || attempt == maxRetries || req.GetBody == nil {
			return res, err
		}
		backoff := (time.Second << attempt) + time.Duration(rand.IntN(500))*time.Millisecond
		delay := geminiRetryDelay(res.Header.Get("Retry-After"), backoff)
		if delay > 15*time.Second {
			return res, nil
		}
		res.Body.Close()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		req = req.Clone(ctx)
		req.Body, err = req.GetBody()
		if err != nil {
			return nil, err
		}
	}
}

func geminiRetryDelay(header string, backoff time.Duration) time.Duration {
	header = strings.TrimSpace(header)
	if seconds, err := strconv.ParseUint(header, 10, 64); err == nil {
		// Clamp before converting so a provider-supplied value cannot overflow.
		return time.Duration(min(seconds, 60)) * time.Second
	}
	if date, err := http.ParseTime(header); err == nil {
		return max(0, time.Until(date))
	}
	return backoff
}

func cloudMeetingSchema(schema map[string]any) map[string]any {
	result := make(map[string]any, len(schema))
	for key, value := range schema {
		switch key {
		case "type", "required", "additionalProperties":
			result[key] = value
		case "items", "properties":
			child := value.(map[string]any)
			if key == "items" {
				result[key] = cloudMeetingSchema(child)
			} else {
				properties := make(map[string]any, len(child))
				for name, property := range child {
					properties[name] = cloudMeetingSchema(property.(map[string]any))
				}
				result[key] = properties
			}
		}
	}
	return result
}

func chatMeetingMessages(instructions, evidence string) []map[string]string {
	return []map[string]string{{"role": "system", "content": instructions}, {"role": "user", "content": evidence}}
}

func chatMeetingFormat(name string, schema map[string]any) map[string]any {
	return map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": name, "strict": true, "schema": schema}}
}

func postMeeting(ctx context.Context, send func(*http.Request) (*http.Response, error), endpoint, source string, headers map[string]string, payload any, decode func(io.Reader) (string, error)) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	res, err := send(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("could not reach %s for note enhancement", source)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		// Provider error bodies can echo private input. Only report the status.
		switch res.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return "", fmt.Errorf("%s denied access. Check your API key and model access in Meeting notes settings", source)
		case http.StatusBadRequest, http.StatusNotFound:
			return "", fmt.Errorf("%s rejected the request (HTTP %d). Choose a text model with structured output support", source, res.StatusCode)
		case http.StatusPaymentRequired, http.StatusTooManyRequests:
			return "", fmt.Errorf("%s quota or billing limit reached (HTTP %d). Check your provider account or use the local model", source, res.StatusCode)
		case http.StatusServiceUnavailable:
			return "", fmt.Errorf("%s is temporarily unavailable (HTTP %d). Try again later or choose another model in Meeting notes settings", source, res.StatusCode)
		default:
			return "", fmt.Errorf("%s failed to enhance notes (HTTP %d)", source, res.StatusCode)
		}
	}
	content, err := decode(io.LimitReader(res.Body, 256*1024))
	if err != nil || strings.TrimSpace(content) == "" {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var feedback meetingOutputError
		if errors.As(err, &feedback) {
			return "", fmt.Errorf("%s %w", source, feedback)
		}
		return "", fmt.Errorf("%s returned an incomplete or invalid enhancement. Try again or choose another model", source)
	}
	return content, nil
}

var errIncompleteMeeting = errors.New("incomplete meeting response")

// Only report known completion outcomes. Provider responses can contain meeting text.
type meetingOutputError string

func (e meetingOutputError) Error() string { return string(e) }

func decodeChatMeeting(body io.Reader) (string, error) {
	var result struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct{ Content, Refusal string }
		}
	}
	if err := json.NewDecoder(body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Choices) != 1 || result.Choices[0].FinishReason != "stop" || result.Choices[0].Message.Refusal != "" {
		return "", errIncompleteMeeting
	}
	return result.Choices[0].Message.Content, nil
}

func decodeGeminiMeeting(body io.Reader) (string, error) {
	var result struct {
		PromptFeedback struct{ BlockReason string }
		Candidates     []struct {
			FinishReason string
			Content      struct {
				Parts []struct {
					Text    string
					Thought bool
				}
			}
		}
	}
	if err := json.NewDecoder(body).Decode(&result); err != nil {
		return "", err
	}
	if result.PromptFeedback.BlockReason != "" && result.PromptFeedback.BlockReason != "BLOCK_REASON_UNSPECIFIED" {
		return "", meetingOutputError("blocked the request before generating notes. Review what was shared or choose another provider")
	}
	if len(result.Candidates) != 1 {
		return "", errIncompleteMeeting
	}
	candidate := result.Candidates[0]
	switch candidate.FinishReason {
	case "STOP":
	case "MAX_TOKENS":
		return "", meetingOutputError("reached its output limit before finishing the notes. Try a shorter transcript or another model")
	case "SAFETY", "RECITATION", "PROHIBITED_CONTENT", "SPII", "BLOCKLIST":
		return "", meetingOutputError("blocked the generated notes because of its content filters. Try another provider or the local model")
	default:
		return "", errIncompleteMeeting
	}
	var text strings.Builder
	for _, part := range candidate.Content.Parts {
		if !part.Thought {
			text.WriteString(part.Text)
		}
	}
	return text.String(), nil
}

func decodeClaudeMeeting(body io.Reader) (string, error) {
	var result struct {
		StopReason string `json:"stop_reason"`
		Content    []struct{ Type, Text string }
	}
	if err := json.NewDecoder(body).Decode(&result); err != nil {
		return "", err
	}
	if result.StopReason != "end_turn" {
		return "", errIncompleteMeeting
	}
	var text strings.Builder
	for _, block := range result.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return text.String(), nil
}
