package agentconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/fsys"
)

func TestComputeFragmentsETag_Deterministic(t *testing.T) {
	t.Parallel()
	refs := []FragmentRef{
		{Name: "safety", Source: "prompts/shared/safety.template.md", SHA: "abc"},
		{Name: "danger", Source: "prompts/shared/safety.template.md", SHA: "abc"},
	}
	a := ComputeFragmentsETag(refs)
	b := ComputeFragmentsETag(refs)
	if a != b {
		t.Errorf("non-deterministic: %q vs %q", a, b)
	}
	if a == "" {
		t.Error("ETag empty, want non-empty hash")
	}
}

func TestComputeFragmentsETag_FormatIsStrongValidator(t *testing.T) {
	t.Parallel()
	got := ComputeFragmentsETag([]FragmentRef{{Name: "x", Source: "y", SHA: "z"}})
	if !strings.HasPrefix(got, "\"") || !strings.HasSuffix(got, "\"") {
		t.Errorf("ETag = %q, want quoted strong validator", got)
	}
}

func TestComputeFragmentsETag_OrderSensitive(t *testing.T) {
	t.Parallel()
	a := ComputeFragmentsETag([]FragmentRef{
		{Name: "x", Source: "f1", SHA: "h1"},
		{Name: "y", Source: "f2", SHA: "h2"},
	})
	b := ComputeFragmentsETag([]FragmentRef{
		{Name: "y", Source: "f2", SHA: "h2"},
		{Name: "x", Source: "f1", SHA: "h1"},
	})
	if a == b {
		t.Error("ETag should differ when order differs (regression guard)")
	}
}

func TestComputeFragmentsETag_EmptyList(t *testing.T) {
	t.Parallel()
	got := ComputeFragmentsETag(nil)
	if got == "" {
		t.Error("ETag for nil list should still be non-empty quoted hash")
	}
}

// setupCityWithAgent writes a minimal city tree under t.TempDir() and
// returns (cityPath, agentBase). Each map entry is a path relative to
// the city root and its file content. Parent directories are created
// automatically. Used by the ListAgentFragments tests.
func setupCityWithAgent(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return root, "worker"
}

func TestListAgentFragments_EmptyDirs(t *testing.T) {
	t.Parallel()
	cityPath, agentBase := setupCityWithAgent(t, map[string]string{
		"city.toml": `[workspace]
name = "test"

[[agent]]
name = "worker"
prompt_template = "prompts/worker.template.md"
`,
		"prompts/worker.template.md": `# bare body, no fragments`,
	})
	got, err := ListAgentFragments(fsys.OSFS{}, cityPath, agentBase)
	if err != nil {
		t.Fatalf("ListAgentFragments: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty list, got %d entries: %v", len(got), got)
	}
}

func TestListAgentFragments_SingleDefine(t *testing.T) {
	t.Parallel()
	cityPath, agentBase := setupCityWithAgent(t, map[string]string{
		"city.toml": `[workspace]
name = "test"

[[agent]]
name = "worker"
prompt_template = "prompts/worker.template.md"
`,
		"prompts/worker.template.md":        `# body`,
		"prompts/shared/safety.template.md": `{{define "safety"}}Be careful.{{end}}`,
	})
	got, err := ListAgentFragments(fsys.OSFS{}, cityPath, agentBase)
	if err != nil {
		t.Fatalf("ListAgentFragments: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(got), got)
	}
	if got[0].Name != "safety" {
		t.Errorf("Name = %q, want safety", got[0].Name)
	}
	want := filepath.Join("prompts", "shared", "safety.template.md")
	if got[0].Source != want {
		t.Errorf("Source = %q, want %q", got[0].Source, want)
	}
	if got[0].SHA == "" {
		t.Error("SHA empty")
	}
}

func TestListAgentFragments_MultipleDefinesPerFile(t *testing.T) {
	t.Parallel()
	cityPath, agentBase := setupCityWithAgent(t, map[string]string{
		"city.toml": `[workspace]
name = "test"

[[agent]]
name = "worker"
prompt_template = "prompts/worker.template.md"
`,
		"prompts/worker.template.md": `# body`,
		"prompts/shared/multi.template.md": `
{{define "warning"}}Warning text.{{end}}
{{define "danger"}}Danger text.{{end}}
`,
	})
	got, err := ListAgentFragments(fsys.OSFS{}, cityPath, agentBase)
	if err != nil {
		t.Fatalf("ListAgentFragments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(got), got)
	}
	if got[0].Source != got[1].Source {
		t.Errorf("expected same Source for both, got %q / %q", got[0].Source, got[1].Source)
	}
	if got[0].SHA != got[1].SHA {
		t.Errorf("expected same SHA for both, got %q / %q", got[0].SHA, got[1].SHA)
	}
	// Sorted alphabetically: danger < warning
	if got[0].Name != "danger" || got[1].Name != "warning" {
		t.Errorf("Names = [%q, %q], want [danger, warning]", got[0].Name, got[1].Name)
	}
}

func TestListAgentFragments_CollisionPromptDirWins(t *testing.T) {
	t.Parallel()
	// Same fragment name "safety" defined in two priority layers:
	// - lower priority: <cityPath>/prompts/shared/safety.template.md      (pack-level shared)
	// - higher priority: <promptDir>/template-fragments/safety.template.md (per-agent template-fragments)
	//
	// Since both files contain `{{define "safety"}}` blocks with DIFFERENT contents,
	// they produce different SHAs — we can distinguish which one won by inspecting
	// got[0].SHA / got[0].Source.
	cityPath, agentBase := setupCityWithAgent(t, map[string]string{
		"city.toml": `[workspace]
name = "test"

[[agent]]
name = "worker"
prompt_template = "prompts/worker.template.md"
`,
		"prompts/worker.template.md":                    `# body`,
		"prompts/shared/safety.template.md":             `{{define "safety"}}PACK VERSION{{end}}`,
		"prompts/template-fragments/safety.template.md": `{{define "safety"}}AGENT VERSION{{end}}`,
	})
	got, err := ListAgentFragments(fsys.OSFS{}, cityPath, agentBase)
	if err != nil {
		t.Fatalf("ListAgentFragments: %v", err)
	}
	var safetyRef *FragmentRef
	for i := range got {
		if got[i].Name == "safety" {
			safetyRef = &got[i]
			break
		}
	}
	if safetyRef == nil {
		t.Fatal("no 'safety' fragment found")
	}
	// The higher-priority layer is <promptDir>/template-fragments/ where
	// promptDir = <cityPath>/prompts (because prompt_template = "prompts/worker.template.md").
	// So the winning source should contain "template-fragments".
	if !strings.Contains(safetyRef.Source, "template-fragments") {
		t.Errorf("Source = %q, expected higher-priority layer (template-fragments) to win", safetyRef.Source)
	}
}

func TestListAgentFragments_ParseErrorSkipped(t *testing.T) {
	t.Parallel()
	cityPath, agentBase := setupCityWithAgent(t, map[string]string{
		"city.toml": `[workspace]
name = "test"

[[agent]]
name = "worker"
prompt_template = "prompts/worker.template.md"
`,
		"prompts/worker.template.md":      `# body`,
		"prompts/shared/good.template.md": `{{define "good"}}OK.{{end}}`,
		// Broken syntax — unclosed action delimiter.
		"prompts/shared/broken.template.md": `{{define "broken"}}{{ unclosed`,
	})
	got, err := ListAgentFragments(fsys.OSFS{}, cityPath, agentBase)
	if err != nil {
		t.Fatalf("expected no top-level error (best-effort), got: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry (good), got %d: %v", len(got), got)
	}
	if got[0].Name != "good" {
		t.Errorf("Name = %q, want good", got[0].Name)
	}
}

func TestListAgentFragments_PerAgentFragments(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string]string{
		"city.toml": `[workspace]
name = "test"

[[agent]]
name = "worker-a"
prompt_template = "agents/worker-a/agent.template.md"

[[agent]]
name = "worker-b"
prompt_template = "agents/worker-b/agent.template.md"
`,
		"agents/worker-a/agent.template.md":                      `# A`,
		"agents/worker-a/template-fragments/private.template.md": `{{define "private_a"}}A only.{{end}}`,
		"agents/worker-b/agent.template.md":                      `# B`,
		"agents/worker-b/template-fragments/private.template.md": `{{define "private_b"}}B only.{{end}}`,
	}
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	gotA, err := ListAgentFragments(fsys.OSFS{}, root, "worker-a")
	if err != nil {
		t.Fatalf("ListAgentFragments(worker-a): %v", err)
	}
	if len(gotA) != 1 || gotA[0].Name != "private_a" {
		t.Errorf("worker-a got %v, want [private_a]", gotA)
	}

	gotB, err := ListAgentFragments(fsys.OSFS{}, root, "worker-b")
	if err != nil {
		t.Fatalf("ListAgentFragments(worker-b): %v", err)
	}
	if len(gotB) != 1 || gotB[0].Name != "private_b" {
		t.Errorf("worker-b got %v, want [private_b]", gotB)
	}
}

func TestListAgentFragments_AgentNotFound(t *testing.T) {
	t.Parallel()
	cityPath, _ := setupCityWithAgent(t, map[string]string{
		"city.toml": `[workspace]
name = "test"

[[agent]]
name = "worker"
prompt_template = "prompts/worker.template.md"
`,
		"prompts/worker.template.md": `# body`,
	})
	_, err := ListAgentFragments(fsys.OSFS{}, cityPath, "nonexistent")
	var notFound ErrAgentNotFound
	if !errors.As(err, &notFound) {
		t.Errorf("err = %v, want ErrAgentNotFound", err)
	}
}
