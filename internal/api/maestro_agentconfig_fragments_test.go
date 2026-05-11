package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/maestro/agentconfig"
)

// seedFragmentAgent registers a city-scoped agent named "fragworker"
// with a prompt_template pointing at a file in <cityPath>/prompts/.
// It also writes a `prompts/shared/safety.template.md` containing a
// single {{define "safety"}} block — the canonical happy-path fixture
// for the fragments tests. Returns the relative path of the seeded
// fragment file so callers can sanity-check the response source.
//
// IMPORTANT: agentconfig.ListAgentFragments re-loads city.toml from
// disk via config.Load on every call (it doesn't use state.Config()),
// so this helper also writes a real city.toml to the cityPath
// matching the registered agent. The agent name "fragworker" avoids
// colliding with the default "worker" agent seeded by newFakeState,
// which has Dir="myrig" and no PromptTemplate.
func seedFragmentAgent(t *testing.T, fs *fakeState) string {
	t.Helper()
	const tmplPath = "prompts/fragworker.template.md"
	const fragRel = "prompts/shared/safety.template.md"
	if err := os.MkdirAll(filepath.Join(fs.cityPath, "prompts", "shared"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fs.cityPath, tmplPath), []byte("# body"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(fs.cityPath, fragRel),
		[]byte(`{{define "safety"}}Be careful.{{end}}`),
		0o644,
	); err != nil {
		t.Fatalf("write fragment: %v", err)
	}
	cityTOML := `[workspace]
name = "test-city"

[[agent]]
name = "fragworker"
provider = "test-agent"
prompt_template = "prompts/fragworker.template.md"
`
	if err := os.WriteFile(filepath.Join(fs.cityPath, "city.toml"), []byte(cityTOML), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:           "fragworker",
		Provider:       "test-agent",
		PromptTemplate: tmplPath,
	})
	return fragRel
}

// TestMaestroAgentGetFragments_HappyPath covers the read path: an
// agent with a fragment file resolves via the four-dir scan and
// returns 200 with the expected entry and a content-hash ETag.
func TestMaestroAgentGetFragments_HappyPath(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	wantSource := seedFragmentAgent(t, fs)
	h := newTestCityHandler(t, fs)

	req := httptest.NewRequest("GET", cityURL(fs, "/agent/fragworker/fragments"), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if w.Header().Get("ETag") == "" {
		t.Error("ETag header missing — clients need it for If-None-Match short-circuit")
	}

	var got agentconfig.FragmentsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\nraw=%s", err, w.Body.String())
	}
	if len(got.Fragments) != 1 {
		t.Fatalf("got %d fragments, want 1: %#v", len(got.Fragments), got.Fragments)
	}
	if got.Fragments[0].Name != "safety" {
		t.Errorf("Name = %q, want safety", got.Fragments[0].Name)
	}
	if got.Fragments[0].Source != wantSource {
		t.Errorf("Source = %q, want %q", got.Fragments[0].Source, wantSource)
	}
	if got.Fragments[0].SHA == "" {
		t.Error("SHA empty")
	}
}

// TestMaestroAgentGetFragments_AgentNotFound asserts the 404 path
// for an unknown agent name — ErrAgentNotFound from the agentconfig
// layer maps to a problem+json 404.
func TestMaestroAgentGetFragments_AgentNotFound(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	seedFragmentAgent(t, fs) // ensures the city has at least one agent, but not "ghost"
	h := newTestCityHandler(t, fs)

	req := httptest.NewRequest("GET", cityURL(fs, "/agent/ghost/fragments"), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
}

// TestMaestroAgentGetFragments_IfNoneMatch304 captures the ETag from
// the first GET and asserts that a follow-up GET with that ETag in
// If-None-Match short-circuits to 304 with no body — saves the
// frontend a full re-render when nothing changed on disk.
func TestMaestroAgentGetFragments_IfNoneMatch304(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	seedFragmentAgent(t, fs)
	h := newTestCityHandler(t, fs)

	// First GET to capture the ETag.
	req1 := httptest.NewRequest("GET", cityURL(fs, "/agent/fragworker/fragments"), nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first GET status = %d, want 200", w1.Code)
	}
	etag := w1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag missing on first GET")
	}

	// Second GET with If-None-Match — should 304.
	req2 := httptest.NewRequest("GET", cityURL(fs, "/agent/fragworker/fragments"), nil)
	req2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)

	if w2.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304; body = %s", w2.Code, w2.Body.String())
	}
	if w2.Body.Len() != 0 {
		t.Errorf("body length = %d, want 0 for 304", w2.Body.Len())
	}
}

// TestMaestroAgentGetFragments_IfNoneMatchStale200 confirms that a
// stale (wrong) If-None-Match still produces a 200 with the current
// representation — the server doesn't silently honor a non-matching
// validator.
func TestMaestroAgentGetFragments_IfNoneMatchStale200(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	seedFragmentAgent(t, fs)
	h := newTestCityHandler(t, fs)

	req := httptest.NewRequest("GET", cityURL(fs, "/agent/fragworker/fragments"), nil)
	req.Header.Set("If-None-Match", `"deadbeef00000000"`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if w.Header().Get("ETag") == `"deadbeef00000000"` {
		t.Error("ETag should differ from the stale If-None-Match value")
	}
}
