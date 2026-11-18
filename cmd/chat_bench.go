package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// birdy chat-bench: the managed chat trial's measurement. It sends fixed
// prompts through the direct and the managed engine of a Birdy host and
// records, per answer, the time to the first token and to done on this
// client's clock, the tool rounds (tool chips), the engine that answered and
// the final text. Cost is read from the host's [chubby] and [chat-managed]
// log lines for the run window each line reports. The invite code comes from
// BIRDY_BENCH_INVITE and is never printed.

var chatBenchPrompts = []string{
	"Search X for what people are saying about OpenAI's latest model release today and summarize the main points.", // search
	"What has @OpenAI posted in the last few days? Summarize their recent posts.",                                  // user-tweets
	"Find a popular recent thread about Codex usage limits and summarize the thread.",                              // search + thread
	"Who is @karpathy, and what has he posted recently?",                                                           // about + user-tweets
	"What's in the X news right now? Give me the top five stories.",                                                // news
	"What are people replying to the most recent post from @sama?",                                                 // user-tweets + replies
	"Explain in two sentences what an API rate limit is.",                                                          // no tools
	"Write a haiku about birds watching the timeline.",                                                             // no tools
}

type chatBenchResult struct {
	Prompt     int      `json:"prompt"`
	Turn       int      `json:"turn"`
	Engine     string   `json:"engine"`   // asked for
	Answered   string   `json:"answered"` // X-Birdy-Chat-Engine
	Started    string   `json:"started"`
	FirstToken float64  `json:"first_token_s"`
	Done       float64  `json:"done_s"`
	ToolRounds int      `json:"tool_rounds"`
	Tools      []string `json:"tools"`
	Error      string   `json:"error,omitempty"`
	Text       string   `json:"text"`
}

func runChatBench(ctx context.Context, base, invite string, engine string, prompt int, turns []map[string]string) chatBenchResult {
	res := chatBenchResult{Prompt: prompt, Turn: (len(turns) + 1) / 2, Engine: engine, Started: time.Now().UTC().Format(time.RFC3339Nano), Tools: []string{}}
	body, _ := json.Marshal(map[string]any{"messages": turns})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/api/chat", bytes.NewReader(body))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Invite-Code", invite)
	if engine == "managed" {
		req.Header.Set(chatEngineHeader, "managed")
	}
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		res.Error = "request failed"
		return res
	}
	defer resp.Body.Close()
	res.Answered = resp.Header.Get(chatEngineHeader)
	if resp.StatusCode != http.StatusOK {
		res.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return res
	}
	var answer strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Command string `json:"command"`
			Error   string `json:"error"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "token", "snapshot":
			if res.FirstToken == 0 && ev.Text != "" {
				res.FirstToken = time.Since(start).Seconds()
			}
			if ev.Type == "snapshot" {
				answer.Reset()
			}
			answer.WriteString(ev.Text)
		case "tool_use":
			res.ToolRounds++
			res.Tools = append(res.Tools, ev.Command)
		case "error":
			res.Error = ev.Error
		case "done":
			res.Done = time.Since(start).Seconds()
		}
	}
	res.Text = answer.String()
	return res
}

func newChatBenchCmd() *cobra.Command {
	var base, engines, followUp string
	var gap time.Duration
	cmd := &cobra.Command{
		Use:    "chat-bench",
		Short:  "Measure direct vs managed chat answers on fixed prompts (trial)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			invite := strings.TrimSpace(os.Getenv("BIRDY_BENCH_INVITE"))
			if invite == "" || base == "" {
				return errors.New("set BIRDY_BENCH_INVITE and --url")
			}
			out := json.NewEncoder(cmd.OutOrStdout())
			for i, prompt := range chatBenchPrompts {
				for _, engine := range strings.Split(engines, ",") {
					turns := []map[string]string{{"role": "user", "content": prompt}}
					for {
						ctx, cancel := context.WithTimeout(cmd.Context(), 7*time.Minute)
						r := runChatBench(ctx, base, invite, strings.TrimSpace(engine), i+1, turns)
						cancel()
						_ = out.Encode(r)
						fmt.Fprintf(cmd.ErrOrStderr(), "prompt %d turn %d %-7s answered=%-7s first=%5.1fs done=%5.1fs tools=%d %s\n",
							r.Prompt, r.Turn, r.Engine, r.Answered, r.FirstToken, r.Done, r.ToolRounds, r.Error)
						time.Sleep(gap)
						// A follow-up turn shows what a conversation's second
						// request costs: a resumed session on the managed path.
						if followUp == "" || len(turns) > 1 || r.Error != "" || r.Text == "" {
							break
						}
						turns = append(turns, map[string]string{"role": "assistant", "content": r.Text}, map[string]string{"role": "user", "content": followUp})
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "url", "", "the Birdy host, e.g. https://birdy.guzus.xyz")
	cmd.Flags().StringVar(&engines, "engines", "direct,managed", "engines to compare, in order")
	cmd.Flags().StringVar(&followUp, "follow-up", "Summarize that in two sentences.", "a second turn after each answer; empty skips it")
	cmd.Flags().DurationVar(&gap, "gap", 5*time.Second, "pause between answers (the host rate-limits chat per address)")
	return cmd
}

func init() {
	rootCmd.AddCommand(newChatBenchCmd())
}
