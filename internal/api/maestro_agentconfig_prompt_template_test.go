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

// newPutRequest builds a PUT request with the X-GC-Request CSRF header
// already set. Mirrors newPatchRequest in maestro_agentconfig_patch_test.go;
// PUT lives behind the same SupervisorMux middleware as PATCH/POST so
// the CSRF header is mandatory or the request is rejected before the
// handler runs.
func newPutRequest(url, body string) *http.Request {
	req := httptest.NewRequest("PUT", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "true")
	return req
}

// mayorPromptPath is the canonical relative path used by every
// happy-path / round-trip test below. Hard-coded inside the helper so
// `unparam` doesn't complain about a single-call-site parameter; tests
// that need a different layout (qualified rig path, missing file,
// pack-derived) wire fs.cfg.Agents up directly.
const mayorPromptPath = "prompts/mayor.md"

// seedMayorPromptTemplate writes a markdown template to the fake
// state's city tempdir and registers a city-scoped "mayor" agent
// referencing it via a relative path. Returns nothing; callers refer
// to mayorPromptPath when they need the configured value.
func seedMayorPromptTemplate(t *testing.T, fs *fakeState, content string) {
	t.Helper()
	abs := filepath.Join(fs.cityPath, mayorPromptPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:           "mayor",
		Provider:       "test-agent",
		PromptTemplate: mayorPromptPath,
	})
}

// TestMaestroAgentGetPromptTemplate_HappyPath covers the read path:
// an agent with a configured prompt_template that exists on disk gets
// 200 with content, mtime, and a content-hash ETag.
func TestMaestroAgentGetPromptTemplate_HappyPath(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	const initial = "# Mayor\n\nLead the city.\n"
	seedMayorPromptTemplate(t, fs, initial)
	h := newTestCityHandler(t, fs)

	req := httptest.NewRequest("GET", cityURL(fs, "/agent/mayor/prompt-template"), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if etag := w.Header().Get("ETag"); etag == "" {
		t.Errorf("ETag header missing — clients need it for optimistic concurrency on PUT")
	}

	var got agentconfig.PromptTemplateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\nraw=%s", err, w.Body.String())
	}
	if got.Path != "prompts/mayor.md" {
		t.Errorf("Path = %q, want configured relative path", got.Path)
	}
	if got.Content != initial {
		t.Errorf("Content = %q, want %q", got.Content, initial)
	}
	if got.Mtime.IsZero() {
		t.Errorf("Mtime zero, want file mtime")
	}
}

// TestMaestroAgentGetPromptTemplate_ETagMatchesContent confirms the
// ETag emitted by GET equals ComputePromptTemplateETag of the file
// content — the explicit invariant that lets clients use the GET ETag
// as If-Match on the next PUT.
func TestMaestroAgentGetPromptTemplate_ETagMatchesContent(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	const content = "abc def 123"
	seedMayorPromptTemplate(t, fs, content)
	h := newTestCityHandler(t, fs)

	req := httptest.NewRequest("GET", cityURL(fs, "/agent/mayor/prompt-template"), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	want := agentconfig.ComputePromptTemplateETag([]byte(content))
	if got := w.Header().Get("ETag"); got != want {
		t.Errorf("ETag = %q, want %q", got, want)
	}
}

// TestMaestroAgentGetPromptTemplate_UnknownAgentReturns404 — the
// classic "no such agent" response. Mirrors the GET /full case so both
// endpoints share the same error taxonomy.
func TestMaestroAgentGetPromptTemplate_UnknownAgentReturns404(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	h := newTestCityHandler(t, fs)

	req := httptest.NewRequest("GET", cityURL(fs, "/agent/ghost/prompt-template"), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
}

// TestMaestroAgentGetPromptTemplate_NoTemplateConfiguredReturns404
// covers the distinct "agent has no prompt_template" branch. The
// frontend distinguishes this from "agent unknown" via the message in
// problem+json — but the status code is the same so naive clients
// don't crash on a new code.
func TestMaestroAgentGetPromptTemplate_NoTemplateConfiguredReturns404(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:     "mayor",
		Provider: "test-agent",
		// No PromptTemplate field set.
	})
	h := newTestCityHandler(t, fs)

	req := httptest.NewRequest("GET", cityURL(fs, "/agent/mayor/prompt-template"), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no prompt_template") {
		t.Errorf("body = %s, want message mentioning 'no prompt_template'", w.Body.String())
	}
}

// TestMaestroAgentGetPromptTemplate_FileMissingReturns404 covers the
// case where the agent has prompt_template configured but the file
// doesn't exist on disk (config stale, manual delete, etc.). The
// response is 404 with a message distinct from "no prompt_template
// configured" so the frontend can prompt the user to either reconfigure
// or PUT to create.
func TestMaestroAgentGetPromptTemplate_FileMissingReturns404(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:           "mayor",
		Provider:       "test-agent",
		PromptTemplate: "prompts/missing.md",
		// Note: file NOT created on disk.
	})
	h := newTestCityHandler(t, fs)

	req := httptest.NewRequest("GET", cityURL(fs, "/agent/mayor/prompt-template"), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "file not found") {
		t.Errorf("body = %s, want message mentioning 'file not found'", w.Body.String())
	}
}

// TestMaestroAgentGetPromptTemplate_OutsideCityRootReturns403 covers
// the pack-derived case: a PromptTemplate that resolves outside
// state.CityPath() is read-only via this endpoint by design.
// Frontend gets 403 (not 404) so it can render an explanatory banner
// instead of an empty editor.
func TestMaestroAgentGetPromptTemplate_OutsideCityRootReturns403(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:           "mayor",
		Provider:       "test-agent",
		PromptTemplate: "/var/packs/external.md", // absolute, outside cityPath
	})
	h := newTestCityHandler(t, fs)

	req := httptest.NewRequest("GET", cityURL(fs, "/agent/mayor/prompt-template"), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", w.Code, w.Body.String())
	}
}

// TestMaestroAgentPutPromptTemplate_HappyPath covers the write path
// without optimistic concurrency. PUT returns 200 + new ETag and the
// file on disk reflects the new content.
func TestMaestroAgentPutPromptTemplate_HappyPath(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	const initial = "old"
	seedMayorPromptTemplate(t, fs, initial)
	h := newTestCityHandler(t, fs)

	const updated = "new content\n"
	body, _ := json.Marshal(map[string]string{"content": updated})
	req := newPutRequest(cityURL(fs, "/agent/mayor/prompt-template"), string(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var got agentconfig.PromptTemplateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Content != updated {
		t.Errorf("response Content = %q, want %q", got.Content, updated)
	}

	// State on disk reflects the write.
	abs := filepath.Join(fs.cityPath, "prompts/mayor.md")
	disk, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read after PUT: %v", err)
	}
	if string(disk) != updated {
		t.Errorf("disk content = %q, want %q", string(disk), updated)
	}

	// New ETag must be the hash of the new content (not the old one).
	wantETag := agentconfig.ComputePromptTemplateETag([]byte(updated))
	if etag := w.Header().Get("ETag"); etag != wantETag {
		t.Errorf("ETag = %q, want %q (hash of new content)", etag, wantETag)
	}
}

// TestMaestroAgentPutPromptTemplate_RoundTripPreservesContent is the
// safety net: write content, read it back via GET, the content must be
// byte-identical. Without this an encoding regression (BOM stripping,
// CRLF normalisation, trailing-newline drift) could silently corrupt
// templates.
func TestMaestroAgentPutPromptTemplate_RoundTripPreservesContent(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	seedMayorPromptTemplate(t, fs, "v0")
	h := newTestCityHandler(t, fs)

	// Multi-line content with leading/trailing whitespace and a tab to
	// surface any normalisation bug.
	const written = "  # Title\n\n\tIndented body.\n\nTrailing line.\n"
	body, _ := json.Marshal(map[string]string{"content": written})
	wPut := httptest.NewRecorder()
	h.ServeHTTP(wPut, newPutRequest(cityURL(fs, "/agent/mayor/prompt-template"), string(body)))
	if wPut.Code != http.StatusOK {
		t.Fatalf("PUT status = %d (body=%s)", wPut.Code, wPut.Body.String())
	}

	wGet := httptest.NewRecorder()
	h.ServeHTTP(wGet, httptest.NewRequest("GET", cityURL(fs, "/agent/mayor/prompt-template"), nil))
	if wGet.Code != http.StatusOK {
		t.Fatalf("GET status = %d (body=%s)", wGet.Code, wGet.Body.String())
	}

	var got agentconfig.PromptTemplateResponse
	if err := json.Unmarshal(wGet.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET body: %v", err)
	}
	if got.Content != written {
		t.Errorf("round-trip content drift:\n  wrote: %q\n  read : %q", written, got.Content)
	}
}

// TestMaestroAgentPutPromptTemplate_MatchingIfMatchSucceeds confirms
// the optimistic-concurrency happy path: the ETag from the most recent
// GET, replayed as If-Match, is accepted and the write proceeds.
func TestMaestroAgentPutPromptTemplate_MatchingIfMatchSucceeds(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	const initial = "before"
	seedMayorPromptTemplate(t, fs, initial)
	h := newTestCityHandler(t, fs)

	currentETag := agentconfig.ComputePromptTemplateETag([]byte(initial))

	body, _ := json.Marshal(map[string]string{"content": "after"})
	req := newPutRequest(cityURL(fs, "/agent/mayor/prompt-template"), string(body))
	req.Header.Set("If-Match", currentETag)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with matching If-Match (body=%s)", w.Code, w.Body.String())
	}
}

// TestMaestroAgentPutPromptTemplate_StaleIfMatchReturns409 covers the
// optimistic-concurrency reject branch. A stale If-Match means another
// operator (or process) updated the file between our GET and PUT —
// the server refuses the write so we don't silently overwrite their
// edit.
func TestMaestroAgentPutPromptTemplate_StaleIfMatchReturns409(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	const initial = "before"
	seedMayorPromptTemplate(t, fs, initial)
	h := newTestCityHandler(t, fs)

	body, _ := json.Marshal(map[string]string{"content": "after"})
	req := newPutRequest(cityURL(fs, "/agent/mayor/prompt-template"), string(body))
	req.Header.Set("If-Match", `"deadbeef00000000"`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}

	// File on disk untouched after 409.
	disk, err := os.ReadFile(filepath.Join(fs.cityPath, "prompts/mayor.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(disk) != initial {
		t.Errorf("disk content = %q, want unchanged %q after 409", string(disk), initial)
	}
}

// TestMaestroAgentPutPromptTemplate_NoTemplateConfiguredReturns404
// asserts the same gate as GET: PUT does not create a missing
// prompt_template mapping. The frontend must use PATCH /full to set
// the path before PUTting content.
func TestMaestroAgentPutPromptTemplate_NoTemplateConfiguredReturns404(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:     "mayor",
		Provider: "test-agent",
	})
	h := newTestCityHandler(t, fs)

	body, _ := json.Marshal(map[string]string{"content": "x"})
	req := newPutRequest(cityURL(fs, "/agent/mayor/prompt-template"), string(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
}

// TestMaestroAgentPutPromptTemplate_CreatesFileWhenMissing covers the
// "configured but file missing" PUT branch — operator deleted the file
// manually, or it was never created. PUT creates it (and any
// intermediate directories) at the configured path.
func TestMaestroAgentPutPromptTemplate_CreatesFileWhenMissing(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:           "mayor",
		Provider:       "test-agent",
		PromptTemplate: "deeply/nested/prompts/mayor.md",
		// Intentionally NOT created on disk.
	})
	h := newTestCityHandler(t, fs)

	body, _ := json.Marshal(map[string]string{"content": "fresh"})
	req := newPutRequest(cityURL(fs, "/agent/mayor/prompt-template"), string(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	abs := filepath.Join(fs.cityPath, "deeply/nested/prompts/mayor.md")
	disk, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read after create: %v", err)
	}
	if string(disk) != "fresh" {
		t.Errorf("disk content = %q, want fresh", string(disk))
	}
}

// TestMaestroAgentPutPromptTemplate_OutsideCityRootReturns403 confirms
// the read-only-pack invariant: even with a write request, paths that
// resolve outside cityPath are rejected. Mirrors the GET /403 case.
func TestMaestroAgentPutPromptTemplate_OutsideCityRootReturns403(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:           "mayor",
		Provider:       "test-agent",
		PromptTemplate: "/var/packs/external.md",
	})
	h := newTestCityHandler(t, fs)

	body, _ := json.Marshal(map[string]string{"content": "x"})
	req := newPutRequest(cityURL(fs, "/agent/mayor/prompt-template"), string(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", w.Code, w.Body.String())
	}

	// External file MUST NOT be created (or even attempted).
	if _, err := os.Stat("/var/packs/external.md"); err == nil {
		t.Errorf("external file unexpectedly exists; PUT must not write outside cityPath")
	}
}

// TestMaestroAgentGetPromptTemplate_QualifiedAgentSucceeds exercises
// the qualified GET path so the rig-scoped variant doesn't drift from
// the unqualified one. Mirrors the PUT qualified test below.
func TestMaestroAgentGetPromptTemplate_QualifiedAgentSucceeds(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	abs := filepath.Join(fs.cityPath, "rigs/myrig/prompts/worker.md")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("worker prompt"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i, a := range fs.cfg.Agents {
		if a.Name == "worker" && a.Dir == "myrig" {
			fs.cfg.Agents[i].PromptTemplate = "rigs/myrig/prompts/worker.md"
			break
		}
	}
	h := newTestCityHandler(t, fs)

	req := httptest.NewRequest("GET", cityURL(fs, "/agent/myrig/worker/prompt-template"), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var got agentconfig.PromptTemplateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Content != "worker prompt" {
		t.Errorf("Content = %q, want worker prompt", got.Content)
	}
}

// TestMaestroAgentPutPromptTemplate_QualifiedAgentSucceeds exercises
// the qualified path (e.g. /agent/myrig/worker/prompt-template) so the
// rig-scoped variant doesn't drift from the unqualified one.
func TestMaestroAgentPutPromptTemplate_QualifiedAgentSucceeds(t *testing.T) {
	t.Parallel()
	fs := newFakeState(t)
	abs := filepath.Join(fs.cityPath, "rigs/myrig/prompts/worker.md")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("v0"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Replace the seed worker (Dir="myrig") with one that has a template path.
	for i, a := range fs.cfg.Agents {
		if a.Name == "worker" && a.Dir == "myrig" {
			fs.cfg.Agents[i].PromptTemplate = "rigs/myrig/prompts/worker.md"
			break
		}
	}
	h := newTestCityHandler(t, fs)

	body, _ := json.Marshal(map[string]string{"content": "v1"})
	req := newPutRequest(cityURL(fs, "/agent/myrig/worker/prompt-template"), string(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	disk, _ := os.ReadFile(abs)
	if string(disk) != "v1" {
		t.Errorf("disk content = %q, want v1", string(disk))
	}
}
