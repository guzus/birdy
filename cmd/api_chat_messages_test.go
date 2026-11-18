package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/guzus/birdy/internal/chatmodel"
)

func TestChatTurnsValidatesUntrustedHistory(t *testing.T) {
	big := strings.Repeat("x", apiChatMaxHistoryBytes+1)
	many := make([]apiChatMessage, apiChatMaxMessages+1)
	for i := range many {
		many[i] = apiChatMessage{Role: "user", Content: "hi"}
	}
	var huge []apiChatMessage
	for i := 0; i < 4; i++ {
		huge = append(huge, apiChatMessage{Role: "user", Content: strings.Repeat("y", apiChatMaxHistoryBytes/4+1)})
	}
	for name, req := range map[string]apiChatRequest{
		"empty":           {},
		"both":            {Prompt: "a", Messages: []apiChatMessage{{Role: "user", Content: "a"}}},
		"system role":     {Messages: []apiChatMessage{{Role: "system", Content: "a"}, {Role: "user", Content: "b"}}},
		"tool role":       {Messages: []apiChatMessage{{Role: "tool", Content: "a"}, {Role: "user", Content: "b"}}},
		"assistant last":  {Messages: []apiChatMessage{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}}},
		"blank content":   {Messages: []apiChatMessage{{Role: "user", Content: "  "}}},
		"invalid utf8":    {Messages: []apiChatMessage{{Role: "user", Content: "\xff"}}},
		"message too big": {Messages: []apiChatMessage{{Role: "user", Content: big}}},
		"too many":        {Messages: many},
		"history too big": {Messages: huge},
	} {
		if _, err := chatTurns(req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// One long turn is fine while the conversation fits.
	if _, err := chatTurns(apiChatRequest{Messages: []apiChatMessage{{Role: "assistant", Content: strings.Repeat("z", 100<<10)}, {Role: "user", Content: "q"}}}); err != nil {
		t.Fatalf("long turn refused: %v", err)
	}
	turns, err := chatTurns(apiChatRequest{Messages: []apiChatMessage{{Role: "user", Content: " a "}, {Role: "assistant", Content: "b\n"}, {Role: "user", Content: "c"}}})
	if err != nil || len(turns) != 3 || turns[0].Content != " a " || turns[1].Content != "b\n" {
		t.Fatalf("history rewritten: %#v %v", turns, err)
	}
	turns, err = chatTurns(apiChatRequest{Prompt: " legacy "})
	if err != nil || len(turns) != 1 || turns[0] != (chatmodel.Message{Role: "user", Content: "legacy"}) {
		t.Fatalf("legacy prompt: %#v %v", turns, err)
	}
}

func TestInvitedChatRequestKeepsPrivacyRuleOnEveryBackend(t *testing.T) {
	turns := []chatmodel.Message{{Role: "user", Content: "q1"}, {Role: "assistant", Content: "a1"}, {Role: "user", Content: "q2"}}
	chubbyReq := invitedChatRequest(turns, chatmodel.Selection{Backend: chatmodel.BackendChubby}, "birdy")
	if chubbyReq.Prompt != "" || len(chubbyReq.Messages) != 3 || chubbyReq.SystemPrompt != chubbyChatSystemPrompt || !strings.HasPrefix(chubbyReq.SystemPrompt, hostedPrivacyPrefix) {
		t.Fatalf("chubby request %#v", chubbyReq)
	}
	for _, m := range chubbyReq.Messages {
		if strings.Contains(m.Content, "PRIVACY") {
			t.Fatal("privacy rule repeated inside a turn")
		}
	}
	for _, backend := range []chatmodel.Backend{chatmodel.BackendClaudeCode, chatmodel.BackendCodex, chatmodel.BackendOpenCode} {
		req := invitedChatRequest(turns, chatmodel.Selection{Backend: backend}, "birdy")
		if !strings.HasPrefix(req.Prompt, hostedPrivacyPrefix+"Conversation so far:\nUser: q1\n\nAssistant: a1\n\nUser: q2") || len(req.Messages) != 0 {
			t.Fatalf("%s prompt %q", backend, req.Prompt)
		}
	}
}

// The web client's history reaches Chubby as real turns after a fixed system
// prompt, so a conversation's next request extends its previous one.
func TestAPIChatSendsHistoryAsTurnsToChubby(t *testing.T) {
	for _, visitor := range []bool{false, true} {
		t.Run(fmt.Sprintf("visitor=%v", visitor), func(t *testing.T) {
			t.Setenv("BIRDY_WEB_CHAT_BACKEND", "chubby")
			t.Setenv("CHUBBY_API_KEY", "chb_fixture")
			t.Setenv("CHUBBY_MODEL", "auto")
			t.Setenv("CHUBBY_CA_FILE", "")
			if visitor {
				t.Setenv("BIRDY_WEB_PUBLIC", "1")
			} else {
				t.Setenv("BIRDY_WEB_PUBLIC", "")
			}
			var got struct {
				Tools    []json.RawMessage `json:"tools"`
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer upstream.Close()
			t.Setenv("CHUBBY_BASE_URL", upstream.URL+"/v1")
			body := `{"messages":[{"role":"user","content":"Public X Explore scan:\nheadline\n\nq1"},{"role":"assistant","content":"a1"},{"role":"user","content":"q2"}]}`
			r := httptest.NewRequest("POST", "/api/chat", strings.NewReader(body))
			r.RemoteAddr = "203.0.113.240:1112"
			if !visitor {
				r.Header.Set("X-Invite-Code", "birdy")
			}
			w := httptest.NewRecorder()
			handleAPIChat("birdy")(w, r)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "event: done") {
				t.Fatalf("response %d %s", w.Code, w.Body)
			}
			if len(got.Messages) != 4 || got.Messages[0].Role != "system" {
				t.Fatalf("upstream messages %+v", got.Messages)
			}
			want := []string{"user:Public X Explore scan:\nheadline\n\nq1", "assistant:a1", "user:q2"}
			for i, m := range got.Messages[1:] {
				if m.Role+":"+m.Content != want[i] {
					t.Fatalf("turn %d = %s:%q", i, m.Role, m.Content)
				}
			}
			if visitor != (len(got.Tools) == 0) {
				t.Fatalf("visitor=%v tools=%d", visitor, len(got.Tools))
			}
			if !visitor && !strings.Contains(got.Messages[0].Content, "PRIVACY") {
				t.Fatal("privacy rule missing from the system prompt")
			}
		})
	}
}

func TestAPIChatRejectsInvalidHistoryBeforeSSE(t *testing.T) {
	for _, body := range []string{
		`{"messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"}]}`,
		`{"messages":[{"role":"user","content":"a","tool_calls":[]}]}`,
		`{"prompt":"a","messages":[{"role":"user","content":"a"}]}`,
		`{"messages":[]}`,
	} {
		r := httptest.NewRequest("POST", "/api/chat", strings.NewReader(body))
		r.Header.Set("X-Invite-Code", "birdy")
		w := httptest.NewRecorder()
		handleAPIChat("birdy")(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
}
