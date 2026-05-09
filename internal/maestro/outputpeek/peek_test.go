package outputpeek

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestForToolInput_KnownTools(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		tool  string
		input string
		want  string
	}{
		{
			name:  "Bash extracts command",
			tool:  "Bash",
			input: `{"command":"ls -la /tmp"}`,
			want:  "ls -la /tmp",
		},
		{
			name:  "Read extracts file_path",
			tool:  "Read",
			input: `{"file_path":"/home/u/project/main.go"}`,
			want:  "/home/u/project/main.go",
		},
		{
			name:  "Edit extracts file_path",
			tool:  "Edit",
			input: `{"file_path":"/etc/hosts","old_string":"x","new_string":"y"}`,
			want:  "/etc/hosts",
		},
		{
			name:  "Write extracts file_path",
			tool:  "Write",
			input: `{"file_path":"/tmp/out.txt","content":"hi"}`,
			want:  "/tmp/out.txt",
		},
		{
			name:  "Glob extracts pattern",
			tool:  "Glob",
			input: `{"pattern":"**/*.go"}`,
			want:  "**/*.go",
		},
		{
			name:  "Grep extracts pattern",
			tool:  "Grep",
			input: `{"pattern":"TODO|FIXME"}`,
			want:  "TODO|FIXME",
		},
		{
			name:  "WebFetch extracts url",
			tool:  "WebFetch",
			input: `{"url":"https://example.com"}`,
			want:  "https://example.com",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ForToolInput(tc.tool, json.RawMessage(tc.input))
			if got != tc.want {
				t.Errorf("ForToolInput(%q, %s) = %q, want %q", tc.tool, tc.input, got, tc.want)
			}
		})
	}
}

func TestForToolInput_UnknownToolReturnsEmpty(t *testing.T) {
	t.Parallel()
	got := ForToolInput("UnknownTool", json.RawMessage(`{"x":1}`))
	if got != "" {
		t.Errorf("ForToolInput unknown tool = %q, want empty", got)
	}
}

func TestForToolInput_MissingFieldReturnsEmpty(t *testing.T) {
	t.Parallel()
	got := ForToolInput("Bash", json.RawMessage(`{"not_command":"x"}`))
	if got != "" {
		t.Errorf("ForToolInput missing field = %q, want empty", got)
	}
}

func TestForToolInput_EmptyInputReturnsEmpty(t *testing.T) {
	t.Parallel()
	got := ForToolInput("Bash", nil)
	if got != "" {
		t.Errorf("ForToolInput empty input = %q, want empty", got)
	}
}

func TestForToolInput_InvalidJSONReturnsEmpty(t *testing.T) {
	t.Parallel()
	got := ForToolInput("Bash", json.RawMessage(`not json`))
	if got != "" {
		t.Errorf("ForToolInput invalid JSON = %q, want empty", got)
	}
}

func TestForToolInput_NonStringFieldReturnsEmpty(t *testing.T) {
	t.Parallel()
	got := ForToolInput("Bash", json.RawMessage(`{"command":123}`))
	if got != "" {
		t.Errorf("ForToolInput non-string field = %q, want empty", got)
	}
}

func TestForToolInput_CollapsesNewlines(t *testing.T) {
	t.Parallel()
	got := ForToolInput("Bash", json.RawMessage(`{"command":"echo a\nb"}`))
	if strings.Contains(got, "\n") {
		t.Errorf("ForToolInput should collapse newlines, got %q", got)
	}
	if got != "echo a b" {
		t.Errorf("ForToolInput collapsed = %q, want %q", got, "echo a b")
	}
}

func TestForToolInput_TruncatesShortCap(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 200)
	got := ForToolInput("Bash", json.RawMessage(`{"command":"`+long+`"}`))
	if !strings.HasSuffix(got, "…") {
		t.Errorf("Bash long command should end with …, got %q", got)
	}
	// Bash uses ShortMax (80). Truncated form is (max-1) chars + "…".
	wantLen := ShortMax
	if runeCountStr(got) != wantLen {
		t.Errorf("Bash truncation rune count = %d, want %d (string=%q)",
			runeCountStr(got), wantLen, got)
	}
}

func TestForToolInput_TruncatesPathCap(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("y", 200)
	got := ForToolInput("Read", json.RawMessage(`{"file_path":"`+long+`"}`))
	if !strings.HasSuffix(got, "…") {
		t.Errorf("Read long path should end with …, got %q", got)
	}
	if runeCountStr(got) != PathMax {
		t.Errorf("Read truncation rune count = %d, want %d (string=%q)",
			runeCountStr(got), PathMax, got)
	}
}

func TestForToolInput_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	got := ForToolInput("Bash", json.RawMessage(`{"command":"   ls   "}`))
	if got != "ls" {
		t.Errorf("ForToolInput trim = %q, want %q", got, "ls")
	}
}

func TestToolResultPeekMax_IsExported(t *testing.T) {
	t.Parallel()
	if ToolResultPeekMax <= 0 {
		t.Errorf("ToolResultPeekMax should be positive, got %d", ToolResultPeekMax)
	}
}

func runeCountStr(s string) int {
	return len([]rune(s))
}
