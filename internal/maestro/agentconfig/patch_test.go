package agentconfig

import (
	"reflect"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

func TestBuildConfigAgentPatch_TableDriven(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		req  AgentPatchRequest
		dir  string
		base string
		want config.AgentPatch
	}{
		{
			name: "empty_request_keeps_only_targeting_keys",
			req:  AgentPatchRequest{},
			dir:  "",
			base: "mayor",
			want: config.AgentPatch{Dir: "", Name: "mayor"},
		},
		{
			name: "scalar_overrides_become_pointer_fields",
			req: AgentPatchRequest{
				IdleTimeout: strPtr("2h"),
				WakeMode:    strPtr("fresh"),
				Provider:    strPtr("codex"),
				Scope:       strPtr("city"),
				Nudge:       strPtr("Check mail and act."),
				WorkDir:     strPtr(".gc/agents/mayor"),
				Suspended:   boolPtrLocal(true),
			},
			dir:  "",
			base: "mayor",
			want: config.AgentPatch{
				Dir:         "",
				Name:        "mayor",
				IdleTimeout: strPtr("2h"),
				WakeMode:    strPtr("fresh"),
				Provider:    strPtr("codex"),
				Scope:       strPtr("city"),
				Nudge:       strPtr("Check mail and act."),
				WorkDir:     strPtr(".gc/agents/mayor"),
				Suspended:   boolPtrLocal(true),
			},
		},
		{
			name: "pool_fields_collapse_into_pool_override",
			req: AgentPatchRequest{
				MinSessions:  intPtr(1),
				MaxSessions:  intPtr(4),
				ScaleCheck:   strPtr("echo 2"),
				DrainTimeout: strPtr("5m"),
			},
			dir:  "rig-a",
			base: "polecat",
			want: config.AgentPatch{
				Dir:  "rig-a",
				Name: "polecat",
				Pool: &config.PoolOverride{
					Min:          intPtr(1),
					Max:          intPtr(4),
					Check:        strPtr("echo 2"),
					DrainTimeout: strPtr("5m"),
				},
			},
		},
		{
			name: "pool_partial_only_sets_specified_keys",
			req: AgentPatchRequest{
				MaxSessions: intPtr(2),
			},
			dir:  "",
			base: "deacon",
			want: config.AgentPatch{
				Dir:  "",
				Name: "deacon",
				Pool: &config.PoolOverride{Max: intPtr(2)},
			},
		},
		{
			name: "collections_pass_through_with_targeting_keys",
			req: AgentPatchRequest{
				Env:             map[string]string{"FOO": "bar", "BAZ": "qux"},
				PreStart:        []string{"echo a", "echo b"},
				InjectFragments: config.Fragments("frag-a"),
			},
			dir:  "",
			base: "boot",
			want: config.AgentPatch{
				Dir:             "",
				Name:            "boot",
				Env:             map[string]string{"FOO": "bar", "BAZ": "qux"},
				PreStart:        []string{"echo a", "echo b"},
				InjectFragments: config.Fragments("frag-a"),
			},
		},
		{
			name: "qualified_name_propagates_dir",
			req: AgentPatchRequest{
				IdleTimeout: strPtr("1h"),
			},
			dir:  "rig-x",
			base: "worker",
			want: config.AgentPatch{
				Dir:         "rig-x",
				Name:        "worker",
				IdleTimeout: strPtr("1h"),
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := BuildConfigAgentPatch(tc.req, tc.dir, tc.base)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("BuildConfigAgentPatch mismatch\n  got:  %#v\n  want: %#v", got, tc.want)
			}
		})
	}
}

// TestBuildConfigAgentPatch_EmptyInjectFragmentsPropagatesAsClear is the
// Q-103 regression guard. The Studio's Fragments tab dispatches PATCH
// /full with `inject_fragments: []` (non-nil empty slice) when the user
// clears the list. The wire-to-config mapping must distinguish that
// "explicit clear" from `inject_fragments` simply being absent (nil
// slice = "leave unchanged"), otherwise the clear silently no-ops.
//
// Pre-fix this test is RED: BuildConfigAgentPatch gated assignment on
// `len(req.InjectFragments) > 0`, treating `[]` and `nil` identically.
// Post-fix the gate is `req.InjectFragments != nil`, preserving the
// empty slice as the "clear" signal all the way down to ApplyPatches.
func TestBuildConfigAgentPatch_EmptyInjectFragmentsPropagatesAsClear(t *testing.T) {
	t.Parallel()

	got := BuildConfigAgentPatch(AgentPatchRequest{
		InjectFragments: []string{},
	}, "", "boot")

	if got.InjectFragments == nil {
		t.Fatalf("InjectFragments = nil, want non-nil empty slice []string{} (clear signal)")
	}
	if len(got.InjectFragments) != 0 {
		t.Fatalf("InjectFragments = %v, want empty slice", got.InjectFragments)
	}
}

// TestBuildConfigAgentPatch_OmittedInjectFragmentsStaysNil is the
// negative-side guard for Q-103: a request with no inject_fragments key
// (nil slice) must continue to mean "leave unchanged" and produce a nil
// slice in the resulting AgentPatch. This pins the distinction
// preserved by the != nil gate.
func TestBuildConfigAgentPatch_OmittedInjectFragmentsStaysNil(t *testing.T) {
	t.Parallel()

	got := BuildConfigAgentPatch(AgentPatchRequest{
		InjectFragments: nil,
	}, "", "boot")

	if got.InjectFragments != nil {
		t.Fatalf("InjectFragments = %v, want nil (no-change signal)", got.InjectFragments)
	}
}

func TestBuildConfigAgentPatch_OmitsPoolWhenAllPoolFieldsAbsent(t *testing.T) {
	t.Parallel()

	got := BuildConfigAgentPatch(AgentPatchRequest{
		IdleTimeout: strPtr("30s"),
	}, "", "mayor")

	if got.Pool != nil {
		t.Fatalf("Pool should be nil when no scaling field is set, got %#v", got.Pool)
	}
}

func TestComputeAgentETag_DeterministicAcrossCalls(t *testing.T) {
	t.Parallel()

	def := AgentDefinition{
		Name:        "mayor",
		Provider:    "codex",
		IdleTimeout: "1h",
		WakeMode:    "fresh",
	}
	first := ComputeAgentETag(def)
	second := ComputeAgentETag(def)

	if first == "" {
		t.Fatal("ETag is empty, want non-empty hash")
	}
	if first != second {
		t.Fatalf("ETag not deterministic: first=%q second=%q", first, second)
	}
}

func TestComputeAgentETag_DiffersOnFieldChange(t *testing.T) {
	t.Parallel()

	a := AgentDefinition{Name: "mayor", Provider: "codex", IdleTimeout: "1h"}
	b := a
	b.IdleTimeout = "2h"

	if ComputeAgentETag(a) == ComputeAgentETag(b) {
		t.Fatalf("ETag must differ when IdleTimeout changes (a=b=%q)", ComputeAgentETag(a))
	}
}

func TestComputeAgentETag_StableAcrossMapKeyInsertionOrder(t *testing.T) {
	t.Parallel()

	a := AgentDefinition{
		Name: "mayor",
		Env:  map[string]string{"A": "1", "B": "2"},
	}

	b := AgentDefinition{Name: "mayor"}
	b.Env = make(map[string]string, 2)
	b.Env["B"] = "2"
	b.Env["A"] = "1"

	if ComputeAgentETag(a) != ComputeAgentETag(b) {
		t.Fatalf("ETag must be stable across map insertion order:\n  a=%q\n  b=%q", ComputeAgentETag(a), ComputeAgentETag(b))
	}
}

func boolPtrLocal(b bool) *bool { return &b }
