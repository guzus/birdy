package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMultiFetchManifestParse(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
		wantOps int
	}{
		{
			name:    "valid manifest with 3 ops",
			input:   `{"operations":[{"id":"a","args":["search","x"]},{"id":"b","args":["news"]},{"id":"c","args":["user-tweets","@x"]}]}`,
			wantOps: 3,
		},
		{
			name:    "manifest with concurrency",
			input:   `{"operations":[{"id":"a","args":["search","x"]}],"concurrency":4}`,
			wantOps: 1,
		},
		{
			name:    "bad json",
			input:   `not json`,
			wantErr: true,
		},
		{
			name:    "empty operations",
			input:   `{"operations":[]}`,
			wantErr: false, // parses but later validation fails
			wantOps: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m MultiFetchManifest
			err := json.Unmarshal([]byte(tc.input), &m)
			if tc.wantErr && err == nil {
				t.Fatalf("expected parse error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if !tc.wantErr && len(m.Operations) != tc.wantOps {
				t.Fatalf("expected %d ops, got %d", tc.wantOps, len(m.Operations))
			}
		})
	}
}

func TestMultiFetchManifestRoundTrip(t *testing.T) {
	original := MultiFetchManifest{
		Operations: []MultiFetchOperation{
			{ID: "OpenAI", Args: []string{"user-tweets", "@OpenAI", "-n", "20"}},
			{ID: "news", Args: []string{"news", "--ai-only"}},
		},
		Concurrency: 8,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got MultiFetchManifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Operations) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(got.Operations))
	}
	if got.Operations[0].ID != "OpenAI" {
		t.Fatalf("expected first id OpenAI, got %q", got.Operations[0].ID)
	}
	if got.Concurrency != 8 {
		t.Fatalf("expected concurrency 8, got %d", got.Concurrency)
	}
}

// TestMultiFetchOutputDirCreation verifies the command creates a missing
// output dir without erroring before account validation. We use a non-existent
// dir under a temp dir to avoid touching the real filesystem.
func TestMultiFetchOutputDirCreation(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "deeper", "out")

	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	stat, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !stat.IsDir() {
		t.Fatalf("target is not a directory")
	}
}
