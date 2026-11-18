package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestChatAgentPromptKeepsPrivacyAndAsksForThoroughSearch(t *testing.T) {
	_, agent := chatAgentDefinitions("https://h.example"+chatToolPath, "m", "s", "tool-1", scanAgentLimits{maxSteps: 8})
	system, _ := agent["system"].(string)
	if !strings.HasPrefix(system, hostedPrivacyPrefix) || !strings.HasPrefix(system, chubbyChatSystemPrompt) {
		t.Fatal("the agent prompt lost the direct chat's prompt or its privacy rule")
	}
	for _, want := range []string{"several searches", "thread", "8 reads"} {
		if !strings.Contains(system, want) {
			t.Fatalf("prompt lacks %q", want)
		}
	}
	// The direct chat's prompt is unchanged by the agent's addition.
	if strings.Contains(chubbyChatSystemPrompt, "several searches") {
		t.Fatal("direct chat prompt changed")
	}
}

// --agent-id adds a version to the running agent and deploys it, keeping its
// tool, instead of creating a new agent.
func TestChatAgentRegisterNewVersion(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	var version map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/v1/agents/agent-1/versions":
			_ = json.NewDecoder(r.Body).Decode(&version)
			w.WriteHeader(201)
			w.Write([]byte(`{"agentId":"agent-1","version":3}`))
		case "/v1/agents/agent-1/deploy":
			w.WriteHeader(201)
			w.Write([]byte(`{"id":"chd_1","version":3,"previousVersion":2}`))
		default:
			http.Error(w, "unexpected", 500)
		}
	}))
	defer srv.Close()
	t.Setenv("CHUBBY_BASE_URL", srv.URL+"/v1")
	t.Setenv("CHUBBY_CA_FILE", "")
	t.Setenv("CHUBBY_MODEL", "claude-opus-5-5")
	t.Setenv("CHUBBY_API_KEY", "chb_direct")
	t.Setenv(chatChubbyKeyEnv, "chb_chat")
	t.Setenv(chatToolSecretEnv, testChatToolSecret)
	t.Setenv(publicScanTool.secretEnv, "")
	t.Setenv(invitedScanTool.secretEnv, "")
	cmd := newChatAgentCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"register", "--host", "https://h.example", "--agent-id", "agent-1", "--tool-id", "tool-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, "|") != "POST /v1/agents/agent-1/versions Bearer chb_chat|POST /v1/agents/agent-1/deploy Bearer chb_chat" {
		t.Fatalf("calls %v", calls)
	}
	if _, named := version["name"]; named || version["system"] != chatAgentSystemPrompt || version["reasoningEffort"] != "low" || version["budgetUsd"] != 1.0 {
		t.Fatalf("version config %v", version)
	}
	if ids, _ := version["toolIds"].([]any); len(ids) != 1 || ids[0] != "tool-1" {
		t.Fatalf("tools %v", version["toolIds"])
	}
	if !strings.Contains(out.String(), "version 3 deployed") || strings.Contains(out.String(), testChatToolSecret) {
		t.Fatalf("output %q", out.String())
	}
	cmd = newChatAgentCmd()
	cmd.SetArgs([]string{"register", "--host", "https://h.example", "--agent-id", "agent-1"})
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err == nil {
		t.Fatal("--agent-id without --tool-id accepted")
	}
}

func TestChatBenchPick(t *testing.T) {
	all, err := chatBenchPick("")
	if err != nil || len(all) != len(chatBenchPrompts) {
		t.Fatalf("all: %v %v", all, err)
	}
	some, err := chatBenchPick("1, 3,6")
	if err != nil || len(some) != 3 || !some[1] || !some[3] || !some[6] {
		t.Fatalf("subset: %v %v", some, err)
	}
	for _, bad := range []string{"0", "9", "x", "1,,3"} {
		if _, err := chatBenchPick(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
