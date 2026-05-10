package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/maestro/agentconfig"
)

// newPatchRequest builds a PATCH request with the X-GC-Request CSRF header
// already set, mirroring newPostRequest from fake_state_test.go. Tests that
// hit a maestro PATCH endpoint must travel through the same SupervisorMux
// middleware production sees, so the CSRF header is mandatory or the
// request is rejected before the handler runs.
func newPatchRequest(url, body string) *http.Request {
	req := httptest.NewRequest("PATCH", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "true")
	return req
}

// TestMaestroAgentPatchFull_AppliesAndReturnsETag covers the happy path:
// a valid PATCH against an existing inline agent applies the change,
// returns 200 with an ETag header, and surfaces the new definition in
// the response body. The test asserts on both the wire response and the
// underlying state mutation so a future refactor can't accidentally
// short-circuit one or the other.
func TestMaestroAgentPatchFull_AppliesAndReturnsETag(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	// Augment seed: city-scoped (Dir="") agent so the unqualified-bare-name
	// route has a real target. Mirrors the GET /full happy-path setup in
	// maestro_agentconfig_test.go.
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:        "mayor",
		Description: "city overseer",
		Provider:    "test-agent",
		IdleTimeout: "30m",
		WakeMode:    "resume",
	})
	h := newTestCityHandler(t, fs)

	req := newPatchRequest(cityURL(fs.fakeState, "/agent/mayor/full"), `{"idle_timeout":"2h"}`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if etag := w.Header().Get("ETag"); etag == "" {
		t.Errorf("ETag header missing — clients need it for optimistic concurrency on the next PATCH")
	}

	var got agentconfig.AgentFullResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v\nraw=%s", err, w.Body.String())
	}
	if got.Definition.Name != "mayor" {
		t.Errorf("Definition.Name = %q, want mayor", got.Definition.Name)
	}
	if got.Definition.IdleTimeout != "2h" {
		t.Errorf("Definition.IdleTimeout = %q, want 2h", got.Definition.IdleTimeout)
	}

	// State actually mutated, not just the response body.
	var matched bool
	for _, a := range fs.cfg.Agents {
		if a.Name == "mayor" && a.Dir == "" {
			if a.IdleTimeout != "2h" {
				t.Errorf("cfg.Agents[mayor].IdleTimeout = %q, want 2h", a.IdleTimeout)
			}
			matched = true
			break
		}
	}
	if !matched {
		t.Fatal("mayor agent missing from cfg.Agents after PATCH")
	}
}

// TestMaestroAgentPatchFull_UnknownAgentReturns404 covers the "no such
// agent" path. Unknown agents must surface as 404 problem details so the
// frontend can distinguish "agent gone away" from "patch invalid" without
// parsing error strings — both upstream agent endpoints return 404 in
// that case and the maestro studio is wired to share the same error
// taxonomy.
func TestMaestroAgentPatchFull_UnknownAgentReturns404(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	h := newTestCityHandler(t, fs)

	req := newPatchRequest(cityURL(fs.fakeState, "/agent/ghost/full"), `{"idle_timeout":"2h"}`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
}

// TestMaestroAgentPatchFull_StaleIfMatchReturns409 exercises the
// optimistic-concurrency contract. When the client sends an If-Match
// header that doesn't match the current ETag, the server rejects the
// PATCH with 409 Conflict and leaves the underlying state untouched.
// The studio uses this to ensure two operators editing the same agent
// in parallel can't silently overwrite each other.
func TestMaestroAgentPatchFull_StaleIfMatchReturns409(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:        "mayor",
		Description: "city overseer",
		Provider:    "test-agent",
		IdleTimeout: "30m",
		WakeMode:    "resume",
	})
	h := newTestCityHandler(t, fs)

	req := newPatchRequest(cityURL(fs.fakeState, "/agent/mayor/full"), `{"idle_timeout":"2h"}`)
	req.Header.Set("If-Match", `"deadbeef00000000"`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	// State must be unchanged on conflict.
	for _, a := range fs.cfg.Agents {
		if a.Name == "mayor" && a.Dir == "" && a.IdleTimeout != "30m" {
			t.Errorf("cfg.Agents[mayor].IdleTimeout = %q, want unchanged 30m after 409", a.IdleTimeout)
		}
	}
}

// TestMaestroAgentPatchFull_PreservesUntouchedFields is the round-trip
// safety net. A PATCH that only sets idle_timeout must NOT clobber any
// other field on the agent (description, provider, env map, scaling
// pointers, etc.). Persistence churn is the worst-case failure: a
// silently dropped MaxActiveSessions on the agent doing real work breaks
// the operator's pool sizing without surfacing in any 4xx response.
//
// Mirrors the field set TestMaestroAgentGetFull_DefinitionFieldsExposed
// guards on the read side, so any new field added to AgentDefinition gets
// pulled into both ends of the contract.
func TestMaestroAgentPatchFull_PreservesUntouchedFields(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	maxSess := 4
	minSess := 1
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:              "polecat",
		Dir:               "myrig",
		Description:       "long-running coder",
		Provider:          "test-agent",
		PromptTemplate:    "prompts/polecat.md",
		IdleTimeout:       "30m",
		SleepAfterIdle:    "off",
		WakeMode:          "fresh",
		MaxActiveSessions: &maxSess,
		MinActiveSessions: &minSess,
		DrainTimeout:      "5m",
		InjectFragments:   []string{"frag-a"},
		Env:               map[string]string{"FOO": "bar"},
	})
	h := newTestCityHandler(t, fs)

	// Patch only idle_timeout. Everything else must round-trip identical.
	req := newPatchRequest(cityURL(fs.fakeState, "/agent/myrig/polecat/full"), `{"idle_timeout":"2h"}`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var got *config.Agent
	for i := range fs.cfg.Agents {
		a := &fs.cfg.Agents[i]
		if a.Name == "polecat" && a.Dir == "myrig" {
			got = a
			break
		}
	}
	if got == nil {
		t.Fatal("polecat agent missing from cfg.Agents after PATCH")
	}

	if got.IdleTimeout != "2h" {
		t.Errorf("IdleTimeout = %q, want 2h (the only patched field)", got.IdleTimeout)
	}
	if got.Description != "long-running coder" {
		t.Errorf("Description = %q, want preserved", got.Description)
	}
	if got.Provider != "test-agent" {
		t.Errorf("Provider = %q, want preserved", got.Provider)
	}
	if got.PromptTemplate != "prompts/polecat.md" {
		t.Errorf("PromptTemplate = %q, want preserved", got.PromptTemplate)
	}
	if got.WakeMode != "fresh" {
		t.Errorf("WakeMode = %q, want preserved", got.WakeMode)
	}
	if got.SleepAfterIdle != "off" {
		t.Errorf("SleepAfterIdle = %q, want preserved", got.SleepAfterIdle)
	}
	if got.DrainTimeout != "5m" {
		t.Errorf("DrainTimeout = %q, want preserved", got.DrainTimeout)
	}
	if got.MaxActiveSessions == nil || *got.MaxActiveSessions != 4 {
		t.Errorf("MaxActiveSessions = %v, want preserved pointer to 4", got.MaxActiveSessions)
	}
	if got.MinActiveSessions == nil || *got.MinActiveSessions != 1 {
		t.Errorf("MinActiveSessions = %v, want preserved pointer to 1", got.MinActiveSessions)
	}
	if len(got.InjectFragments) != 1 || got.InjectFragments[0] != "frag-a" {
		t.Errorf("InjectFragments = %v, want preserved [frag-a]", got.InjectFragments)
	}
	if got.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO] = %q, want preserved bar", got.Env["FOO"])
	}
}

// TestMaestroAgentPatchFull_RoundTripWithIfMatch verifies the full
// optimistic-concurrency loop: GET /full surfaces an ETag, PATCH /full
// with that exact ETag in If-Match succeeds (200), and the new ETag in
// the PATCH response is suitable for the next round. This is the trip
// the studio actually exercises — the prior ETag-stale test only covers
// the rejection branch.
func TestMaestroAgentPatchFull_RoundTripWithIfMatch(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:        "mayor",
		Description: "city overseer",
		Provider:    "test-agent",
		IdleTimeout: "30m",
		WakeMode:    "resume",
	})
	h := newTestCityHandler(t, fs)

	// Step 1: GET /full to capture the current ETag.
	getReq := httptest.NewRequest("GET", cityURL(fs.fakeState, "/agent/mayor/full"), nil)
	getResp := httptest.NewRecorder()
	h.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET /full status = %d, want 200 (body=%s)", getResp.Code, getResp.Body.String())
	}
	etag := getResp.Header().Get("ETag")
	if etag == "" {
		t.Fatal("GET /full ETag header missing — clients can't drive optimistic concurrency without it")
	}

	// Step 2: PATCH /full with the ETag as If-Match.
	patchReq := newPatchRequest(cityURL(fs.fakeState, "/agent/mayor/full"), `{"idle_timeout":"2h"}`)
	patchReq.Header.Set("If-Match", etag)
	patchResp := httptest.NewRecorder()
	h.ServeHTTP(patchResp, patchReq)
	if patchResp.Code != http.StatusOK {
		t.Fatalf("PATCH /full status = %d, want 200 (body=%s)", patchResp.Code, patchResp.Body.String())
	}
	newETag := patchResp.Header().Get("ETag")
	if newETag == "" {
		t.Fatal("PATCH /full new ETag header missing")
	}
	if newETag == etag {
		t.Errorf("PATCH /full returned same ETag %q after a real mutation", newETag)
	}
}

// TestMaestroAgentPatchFull_InvalidBodyReturns422 covers schema-level
// validation. Bodies that violate AgentPatchRequest's enum constraints
// (scope, wake_mode) or numeric bounds (min/max sessions) must be
// rejected with 422 problem+json before the mutator is invoked, so the
// state never sees a half-built patch. The handler relies on Huma's
// generated validation pass — this test traps it from regressing if
// someone weakens the JSON schema tags by accident.
func TestMaestroAgentPatchFull_InvalidBodyReturns422(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{
			name: "wake_mode_outside_enum",
			body: `{"wake_mode":"rocket"}`,
		},
		{
			name: "scope_outside_enum",
			body: `{"scope":"galaxy"}`,
		},
		{
			name: "min_active_sessions_negative",
			body: `{"min_active_sessions":-1}`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fs := newFakeMutatorState(t)
			fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
				Name:        "mayor",
				Description: "city overseer",
				Provider:    "test-agent",
				IdleTimeout: "30m",
				WakeMode:    "resume",
			})
			h := newTestCityHandler(t, fs)

			req := newPatchRequest(cityURL(fs.fakeState, "/agent/mayor/full"), tc.body)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body=%s)", w.Code, w.Body.String())
			}
			if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "problem+json") {
				t.Errorf("Content-Type = %q, want application/problem+json", ct)
			}
			// Mutator must NOT have been called when validation rejects
			// the body. Confirm the agent is unchanged.
			for _, a := range fs.cfg.Agents {
				if a.Name == "mayor" && a.Dir == "" {
					if a.IdleTimeout != "30m" || a.WakeMode != "resume" {
						t.Errorf("agent mutated despite 422: %+v", a)
					}
				}
			}
		})
	}
}
