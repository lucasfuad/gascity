package agentconfig

import (
	"reflect"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

// TestBuildConfigAgent_TableDriven exercises the wire-to-config mapper
// for POST /full. Mirrors TestBuildConfigAgentPatch_TableDriven on the
// patch side: each case asserts the full output struct so any new field
// added to AgentCreateRequest must show up here OR the omission gets
// caught by reflect.DeepEqual.
func TestBuildConfigAgent_TableDriven(t *testing.T) {
	t.Parallel()

	minVal := 1
	maxVal := 4

	cases := []struct {
		name string
		req  AgentCreateRequest
		dir  string
		base string
		want config.Agent
	}{
		{
			name: "minimal_request_keeps_identity_and_provider",
			req:  AgentCreateRequest{Provider: "test-agent"},
			dir:  "",
			base: "mayor",
			want: config.Agent{Name: "mayor", Provider: "test-agent"},
		},
		{
			name: "qualified_name_propagates_dir",
			req:  AgentCreateRequest{Provider: "test-agent"},
			dir:  "rig-a",
			base: "worker",
			want: config.Agent{Name: "worker", Dir: "rig-a", Provider: "test-agent"},
		},
		{
			name: "scalar_fields_round_trip",
			req: AgentCreateRequest{
				Provider:       "test-agent",
				Description:    "long-running coder",
				Scope:          "rig",
				WorkDir:        ".gc/agents/polecat",
				Nudge:          "Check mail and act.",
				Suspended:      true,
				IdleTimeout:    "2h",
				SleepAfterIdle: "off",
				WakeMode:       "fresh",
				DrainTimeout:   "5m",
				ScaleCheck:     "echo 2",
				PromptTemplate: "prompts/polecat.md",
			},
			dir:  "",
			base: "polecat",
			want: config.Agent{
				Name:           "polecat",
				Provider:       "test-agent",
				Description:    "long-running coder",
				Scope:          "rig",
				WorkDir:        ".gc/agents/polecat",
				Nudge:          "Check mail and act.",
				Suspended:      true,
				IdleTimeout:    "2h",
				SleepAfterIdle: "off",
				WakeMode:       "fresh",
				DrainTimeout:   "5m",
				ScaleCheck:     "echo 2",
				PromptTemplate: "prompts/polecat.md",
			},
		},
		{
			name: "pointer_fields_round_trip_as_independent_copies",
			req: AgentCreateRequest{
				Provider:    "test-agent",
				MinSessions: &minVal,
				MaxSessions: &maxVal,
			},
			dir:  "",
			base: "polecat",
			want: config.Agent{
				Name:              "polecat",
				Provider:          "test-agent",
				MinActiveSessions: &minVal,
				MaxActiveSessions: &maxVal,
			},
		},
		{
			name: "collections_pass_through",
			req: AgentCreateRequest{
				Provider:        "test-agent",
				Env:             map[string]string{"FOO": "bar"},
				PreStart:        []string{"echo a", "echo b"},
				InjectFragments: []string{"frag-a", "frag-b"},
			},
			dir:  "",
			base: "boot",
			want: config.Agent{
				Name:            "boot",
				Provider:        "test-agent",
				Env:             map[string]string{"FOO": "bar"},
				PreStart:        []string{"echo a", "echo b"},
				InjectFragments: []string{"frag-a", "frag-b"},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := BuildConfigAgent(tc.req, tc.dir, tc.base)

			// Pointer fields need value equality, not address equality —
			// BuildConfigAgent intentionally copies through *int values
			// so callers can mutate the request without affecting the
			// resulting agent. DeepEqual handles this for us when both
			// sides point to ints with the same value.
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("BuildConfigAgent mismatch\n  got:  %#v\n  want: %#v", got, tc.want)
			}

			// Defense-in-depth: confirm we copied the int through, not
			// the request's pointer. Mutating the request's MinSessions
			// after Build must not mutate the resulting agent.
			if tc.req.MinSessions != nil && got.MinActiveSessions == tc.req.MinSessions {
				t.Error("MinActiveSessions points at the same int as the request — must be a fresh copy")
			}
			if tc.req.MaxSessions != nil && got.MaxActiveSessions == tc.req.MaxSessions {
				t.Error("MaxActiveSessions points at the same int as the request — must be a fresh copy")
			}
		})
	}
}

// TestBuildConfigAgent_EmptyEnvAndListsStayNil pins the empty-collection
// behavior: an absent map/slice in the request must NOT materialize an
// allocated empty container on the resulting Agent. The city.toml
// writer otherwise emits explicit `env = {}` or `inject_fragments = []`
// blocks for agents that didn't set those fields, polluting the diff
// with churn that operators can't explain.
func TestBuildConfigAgent_EmptyEnvAndListsStayNil(t *testing.T) {
	t.Parallel()

	got := BuildConfigAgent(AgentCreateRequest{Provider: "test-agent"}, "", "mayor")

	if got.Env != nil {
		t.Errorf("Env = %v, want nil for absent request map", got.Env)
	}
	if got.PreStart != nil {
		t.Errorf("PreStart = %v, want nil for absent request slice", got.PreStart)
	}
	if got.InjectFragments != nil {
		t.Errorf("InjectFragments = %v, want nil for absent request slice", got.InjectFragments)
	}
}
