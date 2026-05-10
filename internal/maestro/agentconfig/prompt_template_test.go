package agentconfig

import (
	"path/filepath"
	"testing"
	"time"
)

// TestResolvePromptTemplatePath_Cases drives the table of path-resolution
// scenarios the GET/PUT prompt-template handlers depend on. Each case
// pins down how a config.Agent.PromptTemplate value maps onto the host
// filesystem, including the escape-detection branch that gates writes
// to pack-derived templates.
func TestResolvePromptTemplatePath_Cases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		cityPath       string
		agentPath      string
		wantOK         bool
		wantConfigured string
		wantResolved   string
		wantEscapes    bool
	}{
		{
			name:           "relative_path_joins_with_city_root",
			cityPath:       "/city/alpha",
			agentPath:      "prompts/mayor.md",
			wantOK:         true,
			wantConfigured: "prompts/mayor.md",
			wantResolved:   filepath.Clean("/city/alpha/prompts/mayor.md"),
			wantEscapes:    false,
		},
		{
			name:           "absolute_inside_city_root_stays_in_tree",
			cityPath:       "/city/alpha",
			agentPath:      "/city/alpha/prompts/mayor.md",
			wantOK:         true,
			wantConfigured: "/city/alpha/prompts/mayor.md",
			wantResolved:   filepath.Clean("/city/alpha/prompts/mayor.md"),
			wantEscapes:    false,
		},
		{
			name:           "absolute_outside_city_root_marked_escaping",
			cityPath:       "/city/alpha",
			agentPath:      "/var/packs/x.md",
			wantOK:         true,
			wantConfigured: "/var/packs/x.md",
			wantResolved:   filepath.Clean("/var/packs/x.md"),
			wantEscapes:    true,
		},
		{
			name:           "relative_dotdot_traversal_marked_escaping",
			cityPath:       "/city/alpha",
			agentPath:      "../etc/passwd",
			wantOK:         true,
			wantConfigured: "../etc/passwd",
			wantResolved:   filepath.Clean("/city/etc/passwd"),
			wantEscapes:    true,
		},
		{
			name:           "deep_traversal_through_clean_marked_escaping",
			cityPath:       "/city/alpha",
			agentPath:      "prompts/../../escape.md",
			wantOK:         true,
			wantConfigured: "prompts/../../escape.md",
			wantResolved:   filepath.Clean("/city/escape.md"),
			wantEscapes:    true,
		},
		{
			name:           "dot_components_normalize_inside_root",
			cityPath:       "/city/alpha",
			agentPath:      "./prompts/./mayor.md",
			wantOK:         true,
			wantConfigured: "./prompts/./mayor.md",
			wantResolved:   filepath.Clean("/city/alpha/prompts/mayor.md"),
			wantEscapes:    false,
		},
		{
			name:      "empty_returns_not_ok",
			cityPath:  "/city/alpha",
			agentPath: "",
			wantOK:    false,
		},
		{
			name:      "whitespace_returns_not_ok",
			cityPath:  "/city/alpha",
			agentPath: "   ",
			wantOK:    false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := ResolvePromptTemplatePath(tc.cityPath, tc.agentPath)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (got=%+v)", ok, tc.wantOK, got)
			}
			if !ok {
				return
			}
			if got.Configured != tc.wantConfigured {
				t.Errorf("Configured = %q, want %q", got.Configured, tc.wantConfigured)
			}
			if got.Resolved != tc.wantResolved {
				t.Errorf("Resolved = %q, want %q", got.Resolved, tc.wantResolved)
			}
			if got.EscapesCityRoot != tc.wantEscapes {
				t.Errorf("EscapesCityRoot = %v, want %v (resolved=%q, city=%q)",
					got.EscapesCityRoot, tc.wantEscapes, got.Resolved, tc.cityPath)
			}
		})
	}
}

// TestResolvePromptTemplatePath_EmptyCityRootMarksEscaping is the
// defensive case: a State with no CityPath() should never be ambiguous
// — refuse the write rather than silently land the file at "/" or
// the process working directory.
func TestResolvePromptTemplatePath_EmptyCityRootMarksEscaping(t *testing.T) {
	t.Parallel()
	got, ok := ResolvePromptTemplatePath("", "prompts/mayor.md")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if !got.EscapesCityRoot {
		t.Errorf("EscapesCityRoot = false, want true (no city root means defensive deny)")
	}
}

// TestComputePromptTemplateETag_Deterministic guards the round-trip
// invariant: the same content must always hash to the same ETag, or
// the optimistic-concurrency contract collapses (every PUT would
// surface a phantom 409).
func TestComputePromptTemplateETag_Deterministic(t *testing.T) {
	t.Parallel()
	content := []byte("hello world\n")
	a := ComputePromptTemplateETag(content)
	b := ComputePromptTemplateETag(content)
	if a != b {
		t.Errorf("non-deterministic ETag: %q vs %q", a, b)
	}
}

// TestComputePromptTemplateETag_ContentSensitive ensures the ETag
// actually changes when the content changes. Without this the
// optimistic-concurrency check silently accepts stale writes.
func TestComputePromptTemplateETag_ContentSensitive(t *testing.T) {
	t.Parallel()
	a := ComputePromptTemplateETag([]byte("hello"))
	b := ComputePromptTemplateETag([]byte("HELLO"))
	if a == b {
		t.Errorf("ETag collision on different content: %q", a)
	}
}

// TestComputePromptTemplateETag_FormatIsStrongValidator pins the wire
// shape: RFC 9110 strong validator (quoted hex). Browsers and intermediaries
// expect this format; emitting bare hex would silently break some clients.
func TestComputePromptTemplateETag_FormatIsStrongValidator(t *testing.T) {
	t.Parallel()
	got := ComputePromptTemplateETag([]byte("anything"))
	if len(got) < 3 || got[0] != '"' || got[len(got)-1] != '"' {
		t.Errorf("ETag = %q, want RFC 9110 strong validator format \"<hex>\"", got)
	}
}

// TestBuildPromptTemplateResponse_PreservesConfiguredPath is the
// portability tripwire: the wire path must be the value stored in
// city.toml, not a host-resolved absolute path. If a future refactor
// accidentally swaps in the resolved path, frontend would start
// leaking operator-specific filesystem layout to clients.
func TestBuildPromptTemplateResponse_PreservesConfiguredPath(t *testing.T) {
	t.Parallel()
	mtime := time.Date(2026, 5, 10, 20, 0, 0, 0, time.UTC)
	got := BuildPromptTemplateResponse("prompts/mayor.md", []byte("body"), mtime)
	if got.Path != "prompts/mayor.md" {
		t.Errorf("Path = %q, want configured path (not resolved)", got.Path)
	}
	if got.Content != "body" {
		t.Errorf("Content = %q, want body", got.Content)
	}
	if !got.Mtime.Equal(mtime) {
		t.Errorf("Mtime = %v, want %v", got.Mtime, mtime)
	}
}
