package agentconfig

import (
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
