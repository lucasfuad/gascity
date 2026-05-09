package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/maestro/agentconfig"
)

// TestMaestroAgentGetFull_Cases exercises the fork-only
// GET /v0/city/{cityName}/agent/{base}/full and qualified variant
// against a seed augmented with one city-scoped (Dir="") agent and the
// rig-scoped seed agent that newFakeState already provides. Wired
// through newTestCityHandler so the request travels the same
// SupervisorMux + middleware as production.
func TestMaestroAgentGetFull_Cases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		path           string
		wantStatus     int
		wantRuntime    string // expected runtime.name in the response
		wantAgentName  string // expected definition.name (config.Agent.Name)
		wantProvider   string
		wantDirectory  string
		expectFullBody bool
	}{
		{
			name:           "unqualified_city_scoped_agent_returns_full_response",
			path:           "/agent/mayor/full",
			wantStatus:     http.StatusOK,
			wantRuntime:    "mayor",
			wantAgentName:  "mayor",
			wantProvider:   "test-agent",
			wantDirectory:  "",
			expectFullBody: true,
		},
		{
			name:           "qualified_rig_scoped_agent_returns_full_response",
			path:           "/agent/myrig/worker/full",
			wantStatus:     http.StatusOK,
			wantRuntime:    "myrig/worker",
			wantAgentName:  "worker",
			wantProvider:   "test-agent",
			wantDirectory:  "myrig",
			expectFullBody: true,
		},
		{
			name:       "unknown_unqualified_agent_returns_404",
			path:       "/agent/ghost/full",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unknown_qualified_agent_returns_404",
			path:       "/agent/myrig/ghost/full",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fs := newFakeState(t)
			// Augment seed: city-scoped (Dir="") agent so the
			// unqualified-bare-name route has a real target.
			fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
				Name:        "mayor",
				Description: "city overseer",
				Provider:    "test-agent",
				IdleTimeout: "30m",
				WakeMode:    "resume",
			})
			h := newTestCityHandler(t, fs)

			req := httptest.NewRequest("GET", cityURL(fs, tc.path), nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", w.Code, tc.wantStatus, w.Body.String())
			}
			if !tc.expectFullBody {
				return
			}

			var body agentconfig.AgentFullResponse
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v\nraw=%s", err, w.Body.String())
			}

			if body.Definition.Name != tc.wantAgentName {
				t.Errorf("Definition.Name = %q, want %q", body.Definition.Name, tc.wantAgentName)
			}
			if body.Definition.Provider != tc.wantProvider {
				t.Errorf("Definition.Provider = %q, want %q", body.Definition.Provider, tc.wantProvider)
			}
			if body.Definition.Dir != tc.wantDirectory {
				t.Errorf("Definition.Dir = %q, want %q", body.Definition.Dir, tc.wantDirectory)
			}

			runtime, ok := body.Runtime.(map[string]any)
			if !ok {
				t.Fatalf("Runtime is %T, want object after JSON round-trip", body.Runtime)
			}
			if got, _ := runtime["name"].(string); got != tc.wantRuntime {
				t.Errorf("runtime.name = %q, want %q", got, tc.wantRuntime)
			}
		})
	}
}

// TestMaestroAgentGetFull_DefinitionFieldsExposed verifies the studio
// fields the proposal cares about (idle_timeout, wake_mode, dir, etc.)
// actually round-trip through to the wire body. Acts as a tripwire: if
// someone trims AgentDefinition or BuildDefinition without updating the
// test, this catches the regression.
func TestMaestroAgentGetFull_DefinitionFieldsExposed(t *testing.T) {
	t.Parallel()

	fs := newFakeState(t)
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

	req := httptest.NewRequest("GET", cityURL(fs, "/agent/myrig/polecat/full"), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var body agentconfig.AgentFullResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v\nraw=%s", err, w.Body.String())
	}

	def := body.Definition
	if def.PromptTemplate != "prompts/polecat.md" {
		t.Errorf("PromptTemplate = %q, want prompts/polecat.md", def.PromptTemplate)
	}
	if def.IdleTimeout != "30m" {
		t.Errorf("IdleTimeout = %q, want 30m", def.IdleTimeout)
	}
	if def.WakeMode != "fresh" {
		t.Errorf("WakeMode = %q, want fresh", def.WakeMode)
	}
	if def.SleepAfterIdle != "off" {
		t.Errorf("SleepAfterIdle = %q, want off", def.SleepAfterIdle)
	}
	if def.DrainTimeout != "5m" {
		t.Errorf("DrainTimeout = %q, want 5m", def.DrainTimeout)
	}
	if def.MaxActiveSessions == nil || *def.MaxActiveSessions != 4 {
		t.Errorf("MaxActiveSessions = %v, want pointer to 4", def.MaxActiveSessions)
	}
	if def.MinActiveSessions == nil || *def.MinActiveSessions != 1 {
		t.Errorf("MinActiveSessions = %v, want pointer to 1", def.MinActiveSessions)
	}
	if got := def.InjectFragments; len(got) != 1 || got[0] != "frag-a" {
		t.Errorf("InjectFragments = %v, want [frag-a]", got)
	}
	if got := def.Env["FOO"]; got != "bar" {
		t.Errorf("Env[FOO] = %q, want bar", got)
	}
}
