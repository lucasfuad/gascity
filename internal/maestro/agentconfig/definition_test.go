package agentconfig

import (
	"reflect"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }

func TestBuildDefinition_NilAgent(t *testing.T) {
	t.Parallel()

	got := BuildDefinition(nil)
	if !reflect.DeepEqual(got, AgentDefinition{}) {
		t.Fatalf("BuildDefinition(nil) = %+v, want zero AgentDefinition", got)
	}
}

func TestBuildDefinition_TableDriven(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   config.Agent
		want AgentDefinition
	}{
		{
			name: "minimal_agent_keeps_defaults",
			in:   config.Agent{Name: "mayor"},
			want: AgentDefinition{Name: "mayor"},
		},
		{
			name: "rich_agent_maps_all_studio_fields",
			in: config.Agent{
				Name:           "polecat",
				Description:    "long-running coder",
				Dir:            "rig-a",
				WorkDir:        "/work/polecat",
				Scope:          "rig",
				Suspended:      true,
				PreStart:       []string{"echo a", "echo b"},
				PromptTemplate: "prompts/polecat.md",
				Nudge:          "go",
				Session:        "tmux",
				Provider:       "codex",
				StartCommand:   "codex --headless",
				Args:           []string{"--flag", "1"},
				Env: map[string]string{
					"FOO": "bar",
					"BAZ": "qux",
				},
				OptionDefaults: map[string]string{
					"permission_mode": "plan",
					"model":           "sonnet",
				},
				MaxActiveSessions:   intPtr(4),
				MinActiveSessions:   intPtr(1),
				ScaleCheck:          "echo 2",
				DrainTimeout:        "5m",
				IdleTimeout:         "30m",
				SleepAfterIdle:      "off",
				WakeMode:            "fresh",
				InjectFragments:     []string{"frag-a", "frag-b"},
				OverlayDir:          "overlays/polecat",
				Namepool:            "names.txt",
				WorkQuery:           "bd ready",
				DefaultSlingFormula: strPtr("mol-polecat-work"),
			},
			want: AgentDefinition{
				Name:           "polecat",
				Description:    "long-running coder",
				Dir:            "rig-a",
				WorkDir:        "/work/polecat",
				Scope:          "rig",
				Suspended:      true,
				PreStart:       []string{"echo a", "echo b"},
				PromptTemplate: "prompts/polecat.md",
				Nudge:          "go",
				Session:        "tmux",
				Provider:       "codex",
				StartCommand:   "codex --headless",
				Args:           []string{"--flag", "1"},
				Env: map[string]string{
					"FOO": "bar",
					"BAZ": "qux",
				},
				OptionDefaults: map[string]string{
					"permission_mode": "plan",
					"model":           "sonnet",
				},
				MaxActiveSessions:   intPtr(4),
				MinActiveSessions:   intPtr(1),
				ScaleCheck:          "echo 2",
				DrainTimeout:        "5m",
				IdleTimeout:         "30m",
				SleepAfterIdle:      "off",
				WakeMode:            "fresh",
				InjectFragments:     []string{"frag-a", "frag-b"},
				OverlayDir:          "overlays/polecat",
				Namepool:            "names.txt",
				WorkQuery:           "bd ready",
				DefaultSlingFormula: "mol-polecat-work",
			},
		},
		{
			name: "default_sling_formula_nil_pointer_stays_empty_string",
			in:   config.Agent{Name: "mayor", DefaultSlingFormula: nil},
			want: AgentDefinition{Name: "mayor", DefaultSlingFormula: ""},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := BuildDefinition(&tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("BuildDefinition() mismatch\n  got:  %+v\n  want: %+v", got, tc.want)
			}
		})
	}
}

func TestBuildDefinition_DoesNotAliasInputSlices(t *testing.T) {
	t.Parallel()

	in := config.Agent{
		Name:            "mayor",
		PreStart:        []string{"a"},
		Args:            []string{"x"},
		InjectFragments: []string{"frag"},
		Env:             map[string]string{"K": "v"},
		OptionDefaults:  map[string]string{"opt": "val"},
	}
	got := BuildDefinition(&in)

	in.PreStart[0] = "MUTATED"
	in.Args[0] = "MUTATED"
	in.InjectFragments[0] = "MUTATED"
	in.Env["K"] = "MUTATED"
	in.OptionDefaults["opt"] = "MUTATED"

	if got.PreStart[0] == "MUTATED" {
		t.Errorf("PreStart aliases input slice (caller can corrupt response)")
	}
	if got.Args[0] == "MUTATED" {
		t.Errorf("Args aliases input slice")
	}
	if got.InjectFragments[0] == "MUTATED" {
		t.Errorf("InjectFragments aliases input slice")
	}
	if got.Env["K"] == "MUTATED" {
		t.Errorf("Env aliases input map")
	}
	if got.OptionDefaults["opt"] == "MUTATED" {
		t.Errorf("OptionDefaults aliases input map")
	}
}

func TestBuildDefinition_MaxActiveSessionsPointerIsCopied(t *testing.T) {
	t.Parallel()

	maxSess := 3
	in := config.Agent{Name: "polecat", MaxActiveSessions: &maxSess}

	got := BuildDefinition(&in)
	if got.MaxActiveSessions == nil {
		t.Fatalf("MaxActiveSessions = nil, want pointer to %d", maxSess)
	}
	if *got.MaxActiveSessions != maxSess {
		t.Fatalf("*MaxActiveSessions = %d, want %d", *got.MaxActiveSessions, maxSess)
	}
	// Mutating the source pointee must not propagate to the copy.
	maxSess = 99
	if *got.MaxActiveSessions == 99 {
		t.Errorf("MaxActiveSessions aliases input pointer (caller can corrupt response)")
	}
}
