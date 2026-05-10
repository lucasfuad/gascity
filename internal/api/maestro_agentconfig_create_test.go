package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/configedit"
	"github.com/gastownhall/gascity/internal/maestro/agentconfig"
)

// maestroDupCheckState wraps fakeMutatorState so CreateAgent enforces
// uniqueness like the real Editor.CreateAgent does. Used only by the
// duplicate-name test below; the upstream fake doesn't check because
// the upstream POST /agents tests don't need to exercise this branch
// (they cover create-from-empty, not collision). Embedding keeps every
// other State/StateMutator/FullPatchMutator method delegated to the
// upstream fake unchanged.
type maestroDupCheckState struct {
	*fakeMutatorState
}

func (f *maestroDupCheckState) CreateAgent(a config.Agent) error {
	qn := a.QualifiedName()
	for _, ex := range f.cfg.Agents {
		if ex.QualifiedName() == qn {
			return fmt.Errorf("%w: agent %q", configedit.ErrAlreadyExists, qn)
		}
	}
	return f.fakeMutatorState.CreateAgent(a)
}

// newPostRequestJSON builds a POST request with the X-GC-Request CSRF
// header and JSON content type. Mirrors newPatchRequest from the patch
// test file so create tests travel the same SupervisorMux middleware
// production sees.
func newPostRequestJSON(url, body string) *http.Request {
	req := httptest.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GC-Request", "true")
	return req
}

// TestMaestroAgentCreateFull_HappyPath verifies the create-with-defaults
// path: a valid POST body with just the required provider field
// creates an inline city-scoped agent, returns 201 with an ETag header
// and the full AgentFullResponse in the body. The state mutation is
// also asserted so we don't silently degrade to "response looks fine
// but city.toml didn't change".
func TestMaestroAgentCreateFull_HappyPath(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	h := newTestCityHandler(t, fs)

	req := newPostRequestJSON(
		cityURL(fs.fakeState, "/agent/scribe/full"),
		`{"provider":"test-agent","idle_timeout":"45m","wake_mode":"fresh"}`,
	)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
	}
	if etag := w.Header().Get("ETag"); etag == "" {
		t.Errorf("ETag header missing — clients need it for optimistic concurrency on the next PATCH")
	}

	var got agentconfig.AgentFullResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v\nraw=%s", err, w.Body.String())
	}
	if got.Definition.Name != "scribe" {
		t.Errorf("Definition.Name = %q, want scribe", got.Definition.Name)
	}
	if got.Definition.Provider != "test-agent" {
		t.Errorf("Definition.Provider = %q, want test-agent", got.Definition.Provider)
	}
	if got.Definition.IdleTimeout != "45m" {
		t.Errorf("Definition.IdleTimeout = %q, want 45m", got.Definition.IdleTimeout)
	}
	if got.Definition.WakeMode != "fresh" {
		t.Errorf("Definition.WakeMode = %q, want fresh", got.Definition.WakeMode)
	}

	// State actually mutated.
	var matched bool
	for _, a := range fs.cfg.Agents {
		if a.Name == "scribe" && a.Dir == "" {
			if a.Provider != "test-agent" {
				t.Errorf("cfg.Agents[scribe].Provider = %q, want test-agent", a.Provider)
			}
			if a.IdleTimeout != "45m" {
				t.Errorf("cfg.Agents[scribe].IdleTimeout = %q, want 45m", a.IdleTimeout)
			}
			matched = true
			break
		}
	}
	if !matched {
		t.Fatal("scribe agent missing from cfg.Agents after POST")
	}
}

// TestMaestroAgentCreateFull_DuplicateNameReturns409 covers the
// ErrAlreadyExists path: POSTing with a path-derived name that already
// exists in cfg.Agents must surface as 409 Conflict via mutationError,
// so the frontend can route to "name taken — pick another" instead of
// silently overwriting the existing agent.
func TestMaestroAgentCreateFull_DuplicateNameReturns409(t *testing.T) {
	t.Parallel()

	inner := newFakeMutatorState(t)
	inner.cfg.Agents = append(inner.cfg.Agents, config.Agent{
		Name:     "scribe",
		Provider: "test-agent",
	})
	fs := &maestroDupCheckState{fakeMutatorState: inner}
	h := newTestCityHandler(t, fs)

	req := newPostRequestJSON(
		cityURL(inner.fakeState, "/agent/scribe/full"),
		`{"provider":"test-agent"}`,
	)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
}

// TestMaestroAgentCreateFull_InvalidBodyReturns422 covers the
// schema-validation path: Huma rejects bodies that violate
// AgentCreateRequest's enum/minLength constraints before the mutator
// is invoked, so the state never sees a half-built agent. Mirrors
// TestMaestroAgentPatchFull_InvalidBodyReturns422 for the same
// regression-guard reason on the patch side.
func TestMaestroAgentCreateFull_InvalidBodyReturns422(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{
			name: "provider_empty",
			body: `{"provider":""}`,
		},
		{
			name: "wake_mode_outside_enum",
			body: `{"provider":"test-agent","wake_mode":"rocket"}`,
		},
		{
			name: "scope_outside_enum",
			body: `{"provider":"test-agent","scope":"galaxy"}`,
		},
		{
			name: "min_active_sessions_negative",
			body: `{"provider":"test-agent","min_active_sessions":-1}`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fs := newFakeMutatorState(t)
			h := newTestCityHandler(t, fs)

			req := newPostRequestJSON(
				cityURL(fs.fakeState, "/agent/scribe/full"),
				tc.body,
			)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body=%s)", w.Code, w.Body.String())
			}
			if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "problem+json") {
				t.Errorf("Content-Type = %q, want application/problem+json", ct)
			}
			// Mutator must NOT have been called — confirm no scribe agent
			// landed in cfg.Agents.
			for _, a := range fs.cfg.Agents {
				if a.Name == "scribe" {
					t.Errorf("agent created despite 422 validation reject: %+v", a)
				}
			}
		})
	}
}

// TestMaestroAgentCreateFull_QualifiedRigScope covers the qualified
// path: POST /agent/{dir}/{base}/full creates a rig-scoped agent with
// Dir set from the URL. This is the path the Studio's create wizard
// hits when the operator picks a rig from the dropdown before naming
// the agent.
func TestMaestroAgentCreateFull_QualifiedRigScope(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	h := newTestCityHandler(t, fs)

	req := newPostRequestJSON(
		cityURL(fs.fakeState, "/agent/myrig/scribe/full"),
		`{"provider":"test-agent","scope":"rig"}`,
	)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
	}

	var got agentconfig.AgentFullResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v\nraw=%s", err, w.Body.String())
	}
	if got.Definition.Name != "scribe" {
		t.Errorf("Definition.Name = %q, want scribe", got.Definition.Name)
	}
	if got.Definition.Dir != "myrig" {
		t.Errorf("Definition.Dir = %q, want myrig", got.Definition.Dir)
	}

	// State assertion: the rig-scoped agent lives at (Dir=myrig, Name=scribe).
	var matched bool
	for _, a := range fs.cfg.Agents {
		if a.Name == "scribe" && a.Dir == "myrig" {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatal("rig-scoped scribe agent missing from cfg.Agents after qualified POST")
	}
}

// TestMaestroAgentCreateFull_AllFieldsRoundTrip ensures every field the
// AgentCreateRequest surfaces actually lands on the resulting Agent.
// This is the coverage workhorse — without it, a field added to the
// request struct but forgotten in BuildConfigAgent (or in the handler
// glue) would silently swallow operator input on the wire and surface
// only when somebody asks "why didn't my pre_start run?". Sibling of
// TestMaestroAgentPatchFull_PreservesUntouchedFields on the patch side.
func TestMaestroAgentCreateFull_AllFieldsRoundTrip(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	h := newTestCityHandler(t, fs)

	body := `{
		"provider": "test-agent",
		"description": "long-running coder",
		"scope": "rig",
		"work_dir": ".gc/agents/polecat",
		"nudge": "Check mail and act.",
		"suspended": true,
		"idle_timeout": "2h",
		"sleep_after_idle": "off",
		"wake_mode": "fresh",
		"drain_timeout": "5m",
		"scale_check": "echo 2",
		"min_active_sessions": 1,
		"max_active_sessions": 4,
		"prompt_template": "prompts/polecat.md",
		"env": {"FOO": "bar"},
		"pre_start": ["echo a", "echo b"],
		"inject_fragments": ["frag-a"]
	}`
	req := newPostRequestJSON(
		cityURL(fs.fakeState, "/agent/myrig/polecat/full"),
		body,
	)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
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
		t.Fatal("polecat agent missing from cfg.Agents after POST")
	}

	if got.Provider != "test-agent" {
		t.Errorf("Provider = %q, want test-agent", got.Provider)
	}
	if got.Description != "long-running coder" {
		t.Errorf("Description = %q, want long-running coder", got.Description)
	}
	if got.Scope != "rig" {
		t.Errorf("Scope = %q, want rig", got.Scope)
	}
	if got.WorkDir != ".gc/agents/polecat" {
		t.Errorf("WorkDir = %q, want .gc/agents/polecat", got.WorkDir)
	}
	if got.Nudge != "Check mail and act." {
		t.Errorf("Nudge = %q, want Check mail and act.", got.Nudge)
	}
	if !got.Suspended {
		t.Error("Suspended = false, want true")
	}
	if got.IdleTimeout != "2h" {
		t.Errorf("IdleTimeout = %q, want 2h", got.IdleTimeout)
	}
	if got.SleepAfterIdle != "off" {
		t.Errorf("SleepAfterIdle = %q, want off", got.SleepAfterIdle)
	}
	if got.WakeMode != "fresh" {
		t.Errorf("WakeMode = %q, want fresh", got.WakeMode)
	}
	if got.DrainTimeout != "5m" {
		t.Errorf("DrainTimeout = %q, want 5m", got.DrainTimeout)
	}
	if got.ScaleCheck != "echo 2" {
		t.Errorf("ScaleCheck = %q, want echo 2", got.ScaleCheck)
	}
	if got.MinActiveSessions == nil || *got.MinActiveSessions != 1 {
		t.Errorf("MinActiveSessions = %v, want pointer to 1", got.MinActiveSessions)
	}
	if got.MaxActiveSessions == nil || *got.MaxActiveSessions != 4 {
		t.Errorf("MaxActiveSessions = %v, want pointer to 4", got.MaxActiveSessions)
	}
	if got.PromptTemplate != "prompts/polecat.md" {
		t.Errorf("PromptTemplate = %q, want prompts/polecat.md", got.PromptTemplate)
	}
	if got.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO] = %q, want bar", got.Env["FOO"])
	}
	if len(got.PreStart) != 2 || got.PreStart[0] != "echo a" || got.PreStart[1] != "echo b" {
		t.Errorf("PreStart = %v, want [echo a, echo b]", got.PreStart)
	}
	if len(got.InjectFragments) != 1 || got.InjectFragments[0] != "frag-a" {
		t.Errorf("InjectFragments = %v, want [frag-a]", got.InjectFragments)
	}
}
