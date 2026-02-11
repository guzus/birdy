package cmd

import "testing"

func TestMouseEnabledFromEnv(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "empty", value: "", want: false},
		{name: "true", value: "true", want: true},
		{name: "1", value: "1", want: true},
		{name: "yes", value: "yes", want: true},
		{name: "on", value: "on", want: true},
		{name: "false", value: "false", want: false},
		{name: "0", value: "0", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BIRDY_TUI_MOUSE", tt.value)
			if got := mouseEnabledFromEnv(); got != tt.want {
				t.Fatalf("mouseEnabledFromEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}
