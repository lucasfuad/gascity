package configedit_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/configedit"
	"github.com/gastownhall/gascity/internal/fsys"
)

// TestApplyAgentPatchFull_Inline covers the bread-and-butter path for the
// studio's editable Identity/Lifecycle/Scaling tabs: an inline [[agent]]
// in city.toml gets patched in place, the validation pass at the end of
// EditExpanded re-runs, and the new field surfaces on the next load.
func TestApplyAgentPatchFull_Inline(t *testing.T) {
	dir := t.TempDir()
	path := writeTOML(t, dir, `[workspace]
name = "test-city"

[[agent]]
name = "mayor"
provider = "claude"
idle_timeout = "30m"
`)
	ed := configedit.NewEditor(fsys.OSFS{}, path)

	idle := "2h"
	patch := config.AgentPatch{Name: "mayor", IdleTimeout: &idle}
	if err := ed.ApplyAgentPatchFull("mayor", patch); err != nil {
		t.Fatalf("ApplyAgentPatchFull: %v", err)
	}

	cfg := readTOML(t, path)
	mayor := findAgentByName(t, cfg.Agents, "", "mayor")
	if mayor.IdleTimeout != "2h" {
		t.Errorf("IdleTimeout = %q, want 2h", mayor.IdleTimeout)
	}
	// city.toml must NOT gain a [[patches.agent]] block: the agent is
	// inline so the mutation lives directly on the [[agent]] entry.
	raw := string(mustReadFile(t, path))
	if strings.Contains(raw, "[[patches.agent]]") {
		t.Fatalf("city.toml should not gain a patch entry for an inline agent:\n%s", raw)
	}
}

// TestApplyAgentPatchFull_InlinePreservesUntouchedFields is the safety
// net for the editable studio surface. Patching one field must leave
// every other agent field intact — provider, scope, prompt template,
// pool fields, env, etc. config.ApplyPatches drives the merge, so this
// test acts as a tripwire: if a future refactor swaps in a hand-rolled
// merge that drops fields, the round-trip catches it.
func TestApplyAgentPatchFull_InlinePreservesUntouchedFields(t *testing.T) {
	dir := t.TempDir()
	path := writeTOML(t, dir, `[workspace]
name = "test-city"

[[agent]]
name = "polecat"
provider = "claude"
description = "long-running coder"
prompt_template = "prompts/polecat.md"
idle_timeout = "30m"
sleep_after_idle = "off"
wake_mode = "fresh"
max_active_sessions = 4
min_active_sessions = 1
drain_timeout = "5m"
inject_fragments = ["frag-a"]

[agent.env]
FOO = "bar"
`)
	ed := configedit.NewEditor(fsys.OSFS{}, path)

	idle := "2h"
	if err := ed.ApplyAgentPatchFull("polecat", config.AgentPatch{Name: "polecat", IdleTimeout: &idle}); err != nil {
		t.Fatalf("ApplyAgentPatchFull: %v", err)
	}

	cfg := readTOML(t, path)
	a := findAgentByName(t, cfg.Agents, "", "polecat")
	if a.IdleTimeout != "2h" {
		t.Errorf("IdleTimeout = %q, want 2h (only patched field)", a.IdleTimeout)
	}
	if a.Provider != "claude" {
		t.Errorf("Provider = %q, want preserved claude", a.Provider)
	}
	if a.Description != "long-running coder" {
		t.Errorf("Description = %q, want preserved", a.Description)
	}
	if a.PromptTemplate != "prompts/polecat.md" {
		t.Errorf("PromptTemplate = %q, want preserved", a.PromptTemplate)
	}
	if a.SleepAfterIdle != "off" {
		t.Errorf("SleepAfterIdle = %q, want preserved", a.SleepAfterIdle)
	}
	if a.WakeMode != "fresh" {
		t.Errorf("WakeMode = %q, want preserved", a.WakeMode)
	}
	if a.MaxActiveSessions == nil || *a.MaxActiveSessions != 4 {
		t.Errorf("MaxActiveSessions = %v, want preserved 4", a.MaxActiveSessions)
	}
	if a.MinActiveSessions == nil || *a.MinActiveSessions != 1 {
		t.Errorf("MinActiveSessions = %v, want preserved 1", a.MinActiveSessions)
	}
	if a.DrainTimeout != "5m" {
		t.Errorf("DrainTimeout = %q, want preserved", a.DrainTimeout)
	}
	if len(a.InjectFragments) != 1 || a.InjectFragments[0] != "frag-a" {
		t.Errorf("InjectFragments = %v, want preserved [frag-a]", a.InjectFragments)
	}
	if a.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO] = %q, want preserved bar", a.Env["FOO"])
	}
}

// TestApplyAgentPatchFull_Derived ensures that pack-declared agents are
// patched via [[patches.agent]] instead of having their inline source
// rewritten — pack files are read-only, so the [[patches.agent]] block
// in city.toml is the only authoritative override surface.
func TestApplyAgentPatchFull_Derived(t *testing.T) {
	dir := t.TempDir()
	path := writeTOML(t, dir, `[workspace]
name = "test-city"
`)
	if err := os.WriteFile(filepath.Join(dir, "pack.toml"), []byte(`[pack]
name = "test-city"
schema = 2

[[agent]]
name = "worker"
provider = "claude"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ed := configedit.NewEditor(fsys.OSFS{}, path)
	idle := "1h"
	if err := ed.ApplyAgentPatchFull("worker", config.AgentPatch{Name: "worker", IdleTimeout: &idle}); err != nil {
		t.Fatalf("ApplyAgentPatchFull: %v", err)
	}

	cfg := readTOML(t, path)
	if len(cfg.Patches.Agents) != 1 {
		t.Fatalf("len(Patches.Agents) = %d, want 1", len(cfg.Patches.Agents))
	}
	p := cfg.Patches.Agents[0]
	if p.Name != "worker" {
		t.Errorf("patch.Name = %q, want worker", p.Name)
	}
	if p.IdleTimeout == nil || *p.IdleTimeout != "1h" {
		t.Errorf("patch.IdleTimeout = %v, want pointer to 1h", p.IdleTimeout)
	}

	// And the merged expanded view should reflect the patch.
	expanded := readExpandedTOML(t, path)
	worker := findAgentByName(t, expanded.Agents, "", "worker")
	if worker.IdleTimeout != "1h" {
		t.Errorf("expanded worker IdleTimeout = %q, want 1h", worker.IdleTimeout)
	}
}

// TestApplyAgentPatchFull_DerivedMergesIntoExistingPatch covers the
// second-write case: a [[patches.agent]] block already exists for the
// target agent, so the second PATCH must merge non-nil fields into the
// existing block rather than appending a duplicate or overwriting
// previously-set fields with nils.
func TestApplyAgentPatchFull_DerivedMergesIntoExistingPatch(t *testing.T) {
	dir := t.TempDir()
	path := writeTOML(t, dir, `[workspace]
name = "test-city"

[[patches.agent]]
name = "worker"
idle_timeout = "30m"
`)
	if err := os.WriteFile(filepath.Join(dir, "pack.toml"), []byte(`[pack]
name = "test-city"
schema = 2

[[agent]]
name = "worker"
provider = "claude"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ed := configedit.NewEditor(fsys.OSFS{}, path)
	wake := "fresh"
	if err := ed.ApplyAgentPatchFull("worker", config.AgentPatch{Name: "worker", WakeMode: &wake}); err != nil {
		t.Fatalf("ApplyAgentPatchFull: %v", err)
	}

	cfg := readTOML(t, path)
	if len(cfg.Patches.Agents) != 1 {
		t.Fatalf("len(Patches.Agents) = %d, want 1 (merge, not append)", len(cfg.Patches.Agents))
	}
	p := cfg.Patches.Agents[0]
	if p.IdleTimeout == nil || *p.IdleTimeout != "30m" {
		t.Errorf("patch.IdleTimeout = %v, want preserved 30m from previous PATCH", p.IdleTimeout)
	}
	if p.WakeMode == nil || *p.WakeMode != "fresh" {
		t.Errorf("patch.WakeMode = %v, want pointer to fresh from current PATCH", p.WakeMode)
	}
}

// TestApplyAgentPatchFull_DerivedAllFields exercises every PATCH-mutable
// field through the derived branch in a single round, so the
// mergeAgentPatchForFull copy logic is hit for each field. This is the
// coverage workhorse — without it, fields nobody else patches in unit
// tests would silently regress (e.g., a typo in the Pool merge that
// drops DrainTimeout).
func TestApplyAgentPatchFull_DerivedAllFields(t *testing.T) {
	dir := t.TempDir()
	path := writeTOML(t, dir, `[workspace]
name = "test-city"
`)
	if err := os.WriteFile(filepath.Join(dir, "pack.toml"), []byte(`[pack]
name = "test-city"
schema = 2

[[agent]]
name = "worker"
provider = "claude"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ed := configedit.NewEditor(fsys.OSFS{}, path)

	// First PATCH: bring nothing from existing patch (none yet),
	// populate every patch-mutable field. Branches in
	// mergeAgentPatchForFull where dst is empty (Env nil, Pool nil)
	// fire here.
	provider := "codex"
	scope := "rig"
	nudge := "wakeup"
	work := "/tmp/x"
	susp := true
	idle := "1h"
	sleep := "off"
	wake := "fresh"
	minSess := 1
	maxSess := 4
	check := "ok"
	drain := "5m"
	if err := ed.ApplyAgentPatchFull("worker", config.AgentPatch{
		Name:            "worker",
		Provider:        &provider,
		Scope:           &scope,
		Nudge:           &nudge,
		WorkDir:         &work,
		Suspended:       &susp,
		IdleTimeout:     &idle,
		SleepAfterIdle:  &sleep,
		WakeMode:        &wake,
		Env:             map[string]string{"FOO": "bar"},
		PreStart:        []string{"./pre.sh"},
		InjectFragments: config.Fragments("frag-a"),
		Pool: &config.PoolOverride{
			Min:          &minSess,
			Max:          &maxSess,
			Check:        &check,
			DrainTimeout: &drain,
		},
	}); err != nil {
		t.Fatalf("ApplyAgentPatchFull (first): %v", err)
	}

	// Second PATCH: merge into the existing entry, exercising the
	// branches where dst already has Env / Pool populated. Touching
	// only one field per group ensures the previously-persisted values
	// survive.
	idle2 := "2h"
	minSess2 := 2
	if err := ed.ApplyAgentPatchFull("worker", config.AgentPatch{
		Name:        "worker",
		IdleTimeout: &idle2,
		Env:         map[string]string{"BAZ": "qux"},
		Pool:        &config.PoolOverride{Min: &minSess2},
	}); err != nil {
		t.Fatalf("ApplyAgentPatchFull (second): %v", err)
	}

	cfg := readTOML(t, path)
	if len(cfg.Patches.Agents) != 1 {
		t.Fatalf("len(Patches.Agents) = %d, want 1", len(cfg.Patches.Agents))
	}
	p := cfg.Patches.Agents[0]

	// Identity preserved.
	if p.Name != "worker" {
		t.Errorf("patch.Name = %q, want worker", p.Name)
	}

	// Fields set on first PATCH and untouched on second must persist.
	if p.Provider == nil || *p.Provider != "codex" {
		t.Errorf("Provider = %v, want preserved codex", p.Provider)
	}
	if p.Scope == nil || *p.Scope != "rig" {
		t.Errorf("Scope = %v, want preserved rig", p.Scope)
	}
	if p.Nudge == nil || *p.Nudge != "wakeup" {
		t.Errorf("Nudge = %v, want preserved wakeup", p.Nudge)
	}
	if p.WorkDir == nil || *p.WorkDir != "/tmp/x" {
		t.Errorf("WorkDir = %v, want preserved /tmp/x", p.WorkDir)
	}
	if p.Suspended == nil || !*p.Suspended {
		t.Errorf("Suspended = %v, want preserved true", p.Suspended)
	}
	if p.SleepAfterIdle == nil || *p.SleepAfterIdle != "off" {
		t.Errorf("SleepAfterIdle = %v, want preserved off", p.SleepAfterIdle)
	}
	if p.WakeMode == nil || *p.WakeMode != "fresh" {
		t.Errorf("WakeMode = %v, want preserved fresh", p.WakeMode)
	}
	if len(p.PreStart) != 1 || p.PreStart[0] != "./pre.sh" {
		t.Errorf("PreStart = %v, want preserved [./pre.sh]", p.PreStart)
	}
	if p.InjectFragments == nil || len(*p.InjectFragments) != 1 || (*p.InjectFragments)[0] != "frag-a" {
		t.Errorf("InjectFragments = %v, want preserved &[frag-a]", p.InjectFragments)
	}
	if p.Pool == nil {
		t.Fatal("Pool nil after merge")
	}
	if p.Pool.Max == nil || *p.Pool.Max != 4 {
		t.Errorf("Pool.Max = %v, want preserved 4", p.Pool.Max)
	}
	if p.Pool.Check == nil || *p.Pool.Check != "ok" {
		t.Errorf("Pool.Check = %v, want preserved ok", p.Pool.Check)
	}
	if p.Pool.DrainTimeout == nil || *p.Pool.DrainTimeout != "5m" {
		t.Errorf("Pool.DrainTimeout = %v, want preserved 5m", p.Pool.DrainTimeout)
	}

	// Fields updated on second PATCH must reflect the new values.
	if p.IdleTimeout == nil || *p.IdleTimeout != "2h" {
		t.Errorf("IdleTimeout = %v, want updated 2h", p.IdleTimeout)
	}
	if p.Pool.Min == nil || *p.Pool.Min != 2 {
		t.Errorf("Pool.Min = %v, want updated 2", p.Pool.Min)
	}

	// Env additive merge: original FOO=bar plus new BAZ=qux.
	if p.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO] = %q, want preserved bar", p.Env["FOO"])
	}
	if p.Env["BAZ"] != "qux" {
		t.Errorf("Env[BAZ] = %q, want appended qux", p.Env["BAZ"])
	}
}

// TestApplyAgentPatchFull_NotFound exercises the sentinel-error contract.
// Handlers in internal/api map configedit.ErrNotFound to 404 problem
// details via mutationError, so the Editor must return an error wrapping
// that sentinel — not a free-form fmt.Errorf string — for the studio's
// error taxonomy to stay correct.
func TestApplyAgentPatchFull_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := writeTOML(t, dir, minimalCity())
	ed := configedit.NewEditor(fsys.OSFS{}, path)

	idle := "2h"
	err := ed.ApplyAgentPatchFull("ghost", config.AgentPatch{Name: "ghost", IdleTimeout: &idle})
	if err == nil {
		t.Fatal("ApplyAgentPatchFull: want error, got nil")
	}
	if !errors.Is(err, configedit.ErrNotFound) {
		t.Errorf("err = %v, want wrap of configedit.ErrNotFound", err)
	}
}

// findAgentByName is a small helper for the maestro patch tests — the
// existing findAgent helper at configedit_test.go takes a config.City,
// but the patch-full tests want to assert on a slice directly so we can
// match by qualified identity (dir, name).
func findAgentByName(t *testing.T, agents []config.Agent, dir, name string) config.Agent {
	t.Helper()
	for _, a := range agents {
		if a.Dir == dir && a.Name == name {
			return a
		}
	}
	t.Fatalf("agent %q (dir=%q) not found in %d agents", name, dir, len(agents))
	return config.Agent{}
}
