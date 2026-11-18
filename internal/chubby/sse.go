package chubby

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/guzus/birdy/internal/claude"
)

const maxStreamBytes = 4 << 20

type turnResult struct {
	text   strings.Builder
	calls  []toolCall
	finish string
	tokens *int64
	cost   *float64
}

type choice struct {
	Index  *int   `json:"index"`
	Finish string `json:"finish_reason"`
	Delta  struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		Calls   []struct {
			Index *int `json:"index"`
			toolCall
		} `json:"tool_calls"`
	} `json:"delta"`
}

func readStream(body io.Reader, emit func(claude.Event)) (*turnResult, error) {
	limited := &io.LimitedReader{R: body, N: maxStreamBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxStreamBytes)
	var data strings.Builder
	result := &turnResult{}
	for scanner.Scan() {
		if limited.N == 0 {
			return nil, errors.New("Chubby stream is too large")
		}
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			continue
		}
		if line != "" || data.Len() == 0 {
			continue
		}
		event := data.String()
		data.Reset()
		if event == "[DONE]" {
			switch result.finish {
			case "stop":
				if strings.TrimSpace(result.text.String()) == "" || len(result.calls) > 0 {
					return nil, errors.New("Chubby returned an incomplete answer")
				}
			case "tool_calls":
				if len(result.calls) == 0 {
					return nil, errors.New("Chubby returned an empty tool turn")
				}
			default:
				return nil, errors.New("Chubby stream ended without a supported finish reason")
			}
			return result, nil
		}
		if err := result.consume(event, emit); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("Chubby stream disconnected before completion")
}

func (s *turnResult) consume(data string, emit func(claude.Event)) error {
	var event struct {
		Error   json.RawMessage `json:"error"`
		Choices []choice        `json:"choices"`
		Usage   *struct {
			Total *int64   `json:"total_tokens"`
			Cost  *float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return errors.New("Chubby returned invalid stream data")
	}
	if event.Error != nil && string(event.Error) != "null" {
		return errors.New("Chubby reported an inference error")
	}
	if len(event.Choices) == 0 {
		if event.Choices == nil || s.finish == "" || event.Usage == nil || s.tokens != nil || event.Usage.Total == nil || *event.Usage.Total < 0 {
			return errors.New("Chubby returned invalid stream usage")
		}
		s.tokens = event.Usage.Total
		s.cost = event.Usage.Cost
		return nil
	}
	if len(event.Choices) != 1 || event.Choices[0].Index == nil || *event.Choices[0].Index != 0 || s.finish != "" {
		return errors.New("Chubby returned unexpected stream choices")
	}
	c := event.Choices[0]
	if c.Delta.Role != "" && c.Delta.Role != "assistant" {
		return errors.New("Chubby returned an invalid stream role")
	}
	if c.Delta.Content != "" {
		s.text.WriteString(c.Delta.Content)
		emit(claude.Event{Type: claude.EventToken, Text: c.Delta.Content})
	}
	// Office emits one complete tool call per index. Check every index before
	// handing the verified terminal turn to the executor.
	for _, d := range c.Delta.Calls {
		if d.Index == nil || *d.Index != len(s.calls) || len(s.calls) >= maxTools {
			return errors.New("Chubby returned invalid tool order")
		}
		s.calls = append(s.calls, d.toolCall)
	}
	if c.Finish != "" {
		switch c.Finish {
		case "tool_calls", "stop":
			s.finish = c.Finish
		case "length":
			return errors.New("Chubby response exceeded its output limit")
		default:
			return errors.New("Chubby returned an unsupported finish reason")
		}
	}
	return nil
}
