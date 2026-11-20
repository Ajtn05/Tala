package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (p *chatGPTPlan) listModels(ctx context.Context, accessToken string) ([]chatGPTModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, chatGPTResource+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("could not load the models available to this ChatGPT account")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, chatGPTHTTPError(res)
	}
	var catalog struct {
		Models []struct {
			Slug       string
			Visibility string
			Name       string `json:"display_name"`
		}
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 2*1024*1024)).Decode(&catalog); err != nil {
		return nil, errors.New("ChatGPT returned an invalid model list")
	}
	var models []chatGPTModel
	for _, model := range catalog.Models {
		if model.Visibility == "list" && meetingModelID.MatchString(model.Slug) {
			name := model.Name
			if name == "" {
				name = model.Slug
			}
			models = append(models, chatGPTModel{ID: model.Slug, Name: name})
		}
	}
	if len(models) == 0 {
		return nil, errors.New("this ChatGPT account has no available models for plan usage")
	}
	return models, nil
}

func (p *chatGPTPlan) complete(client *http.Client, target meetingTarget) meetingCompletion {
	return func(ctx context.Context, name, instructions, evidence string, schema map[string]any) (string, error) {
		accessToken, err := p.accessToken(ctx, target.Account)
		if err != nil {
			return "", err
		}
		payload, err := json.Marshal(map[string]any{
			"model": target.Model, "instructions": instructions, "store": false, "stream": true,
			"input": []map[string]string{{"role": "user", "content": evidence}},
			"text": map[string]any{"format": map[string]any{
				"type": "json_schema", "name": name, "strict": true, "schema": cloudMeetingSchema(schema),
			}},
		})
		if err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, chatGPTResource+"/responses", bytes.NewReader(payload))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		res, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", errors.New("could not reach ChatGPT for note enhancement")
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return "", chatGPTHTTPError(res)
		}
		content, err := readChatGPTStream(res.Body)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return content, err
	}
}

func chatGPTServiceError(code string, status int) error {
	switch code {
	case "subscription_sharing_usage_limit_exceeded":
		return errChatGPTLimit
	case "subscription_sharing_user_not_eligible":
		return errors.New("ChatGPT plan usage is unavailable for this account or workspace")
	case "subscription_sharing_usage_unavailable", "subscription_sharing_user_unavailable":
		return errors.New("ChatGPT plan usage is temporarily unavailable. Try again later")
	case "subscription_sharing_unsupported_capability":
		return errors.New("this ChatGPT model could not return structured notes. Choose another model in Meeting notes settings")
	}
	switch status {
	case http.StatusUnauthorized:
		return errChatGPTSignIn
	case http.StatusForbidden:
		return errors.New("ChatGPT denied access. Check the account’s plan and workspace permissions")
	case http.StatusTooManyRequests:
		return errChatGPTLimit
	case http.StatusBadRequest, http.StatusNotFound:
		return errors.New("ChatGPT rejected this model or request. Try another model in Meeting notes settings")
	case 0:
		return errors.New("ChatGPT could not complete the enhancement")
	default:
		return fmt.Errorf("ChatGPT could not complete the request (HTTP %d)", status)
	}
}

func chatGPTHTTPError(res *http.Response) error {
	var body struct {
		Error struct{ Code string }
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 32*1024)).Decode(&body)
	return chatGPTServiceError(body.Error.Code, res.StatusCode)
}

func readChatGPTStream(r io.Reader) (string, error) {
	const maxEventBytes = 512 * 1024
	scanner := bufio.NewScanner(io.LimitReader(r, 8*1024*1024))
	scanner.Buffer(make([]byte, 4096), maxEventBytes)
	var data, output strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if field, ok := strings.CutPrefix(line, "data:"); ok {
			if data.Len()+len(field)+1 > maxEventBytes {
				return "", errors.New("ChatGPT returned an oversized response")
			}
			data.WriteString(strings.TrimPrefix(field, " "))
			data.WriteByte('\n')
			continue
		}
		if line != "" || data.Len() == 0 {
			continue
		}
		var event struct {
			Type, Delta, Code string
			Response          struct {
				Error struct{ Code string }
			}
		}
		body := data.String()
		data.Reset()
		if strings.TrimSpace(body) == "[DONE]" {
			break
		}
		if err := json.Unmarshal([]byte(body), &event); err != nil {
			return "", errors.New("ChatGPT returned an invalid response stream")
		}
		switch event.Type {
		case "response.output_text.delta":
			output.WriteString(event.Delta)
		case "response.completed":
			if strings.TrimSpace(output.String()) != "" {
				return output.String(), nil
			}
			return "", errors.New("ChatGPT completed the request without returning meeting notes")
		case "response.failed":
			return "", chatGPTServiceError(event.Response.Error.Code, 0)
		case "error":
			return "", chatGPTServiceError(event.Code, 0)
		case "response.incomplete", "response.refusal.done":
			return "", errors.New("ChatGPT returned an incomplete enhancement. Try again or choose another model")
		}
	}
	return "", errors.New("the ChatGPT response ended before enhancement completed")
}
