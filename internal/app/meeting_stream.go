package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var errIncompleteMeeting = errors.New("incomplete meeting response")

type meetingEventDecoder func(event []byte) (text string, done bool, err error)

func readMeetingStream(body io.Reader, decode meetingEventDecoder, preview func(string)) (string, error) {
	const maxEventBytes = 512 * 1024
	const maxOutputBytes = 256 * 1024
	scanner := bufio.NewScanner(io.LimitReader(body, 8*1024*1024))
	scanner.Buffer(make([]byte, 4096), maxEventBytes)
	var data, output strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if field, ok := strings.CutPrefix(line, "data:"); ok {
			if data.Len()+len(field)+1 > maxEventBytes {
				return "", errIncompleteMeeting
			}
			data.WriteString(strings.TrimPrefix(field, " "))
			data.WriteByte('\n')
			continue
		}
		if line != "" || data.Len() == 0 {
			continue
		}
		event := data.String()
		data.Reset()
		if strings.TrimSpace(event) == "[DONE]" {
			break
		}
		text, done, err := decode([]byte(event))
		if err != nil {
			return "", err
		}
		if output.Len()+len(text) > maxOutputBytes {
			return "", errIncompleteMeeting
		}
		output.WriteString(text)
		if preview != nil && strings.ContainsAny(text, `"}]`) {
			preview(output.String())
		}
		if done && strings.TrimSpace(output.String()) != "" {
			return output.String(), nil
		}
		if done {
			break
		}
	}
	return "", errIncompleteMeeting
}

func chatMeetingEvent(body []byte) (string, bool, error) {
	var event struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Delta        struct{ Content, Refusal string }
		}
		Error json.RawMessage
	}
	if json.Unmarshal(body, &event) != nil || event.Error != nil || len(event.Choices) > 1 {
		return "", false, errIncompleteMeeting
	}
	if len(event.Choices) == 0 {
		return "", false, nil
	}
	choice := event.Choices[0]
	if choice.Delta.Refusal != "" || (choice.FinishReason != "" && choice.FinishReason != "stop") {
		return "", false, errIncompleteMeeting
	}
	return choice.Delta.Content, choice.FinishReason == "stop", nil
}

func geminiMeetingEvent(body []byte) (string, bool, error) {
	var event struct {
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
		Error json.RawMessage
	}
	if json.Unmarshal(body, &event) != nil || event.Error != nil || len(event.Candidates) > 1 {
		return "", false, errIncompleteMeeting
	}
	if reason := event.PromptFeedback.BlockReason; reason != "" && reason != "BLOCK_REASON_UNSPECIFIED" {
		return "", false, meetingOutputError("blocked the request before generating notes. Review what was shared or choose another provider")
	}
	if len(event.Candidates) == 0 {
		return "", false, nil
	}
	candidate := event.Candidates[0]
	switch candidate.FinishReason {
	case "", "FINISH_REASON_UNSPECIFIED", "STOP":
	case "MAX_TOKENS":
		return "", false, meetingOutputError("reached its output limit before finishing the notes. Try a shorter transcript or another model")
	case "SAFETY", "RECITATION", "PROHIBITED_CONTENT", "SPII", "BLOCKLIST":
		return "", false, meetingOutputError("blocked the generated notes because of its content filters. Try another provider or the local model")
	default:
		return "", false, errIncompleteMeeting
	}
	var text strings.Builder
	for _, part := range candidate.Content.Parts {
		if !part.Thought {
			text.WriteString(part.Text)
		}
	}
	return text.String(), candidate.FinishReason == "STOP", nil
}

func claudeMeetingEvents() meetingEventDecoder {
	var stopReason string
	return func(body []byte) (string, bool, error) {
		var event struct {
			Type         string
			ContentBlock struct{ Type, Text string } `json:"content_block"`
			Delta        struct {
				Type, Text string
				StopReason string `json:"stop_reason"`
			}
		}
		if json.Unmarshal(body, &event) != nil || event.Type == "error" {
			return "", false, errIncompleteMeeting
		}
		switch event.Type {
		case "content_block_start":
			if event.ContentBlock.Type == "text" {
				return event.ContentBlock.Text, false, nil
			}
		case "content_block_delta":
			if event.Delta.Type == "text_delta" {
				return event.Delta.Text, false, nil
			}
		case "message_delta":
			if event.Delta.StopReason != "" {
				stopReason = event.Delta.StopReason
			}
		case "message_stop":
			if stopReason != "end_turn" {
				return "", false, errIncompleteMeeting
			}
			return "", true, nil
		}
		return "", false, nil
	}
}

// Preview complete strings and list entries from a JSON object that is still
// arriving. Final decoding separately requires the entire response and schema.
func meetingJSONPreview[T any](publish func(T)) func(string) {
	if publish == nil {
		return nil
	}
	var last []byte
	return func(content string) {
		decoder := json.NewDecoder(strings.NewReader(content))
		if start, err := decoder.Token(); err != nil || start != json.Delim('{') {
			return
		}
		fields := map[string]any{}
	fieldsLoop:
		for decoder.More() {
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok {
				break
			}
			value, err := decoder.Token()
			if err != nil {
				break
			}
			if text, ok := value.(string); ok {
				fields[name] = text
				continue
			}
			if value != json.Delim('[') {
				break
			}
			items := []json.RawMessage{}
			for decoder.More() {
				var item json.RawMessage
				if err := decoder.Decode(&item); err != nil {
					fields[name] = items
					break fieldsLoop
				}
				items = append(items, item)
			}
			fields[name] = items
			if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
				break
			}
		}
		data, _ := json.Marshal(fields)
		if bytes.Equal(data, last) {
			return
		}
		var result T
		if json.Unmarshal(data, &result) == nil {
			last = data
			publish(result)
		}
	}
}
