package api

import (
	"fmt"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/configedit"
)

// ApplyAgentPatchFull satisfies agentconfig.FullPatchMutator on the fake
// state used by maestro PATCH tests. Mirrors the real
// controllerState.ApplyAgentPatchFull dispatch:
//
//   - inline agent in raw config: apply the patch fields directly to the
//     matching cfg.Agents entry via config.ApplyPatches, which mutates the
//     target struct in place.
//   - pack-derived agent: append/merge a [[patches.agent]] entry on the raw
//     config so the next config load applies it during expansion.
//   - not found: return configedit.ErrNotFound so the handler maps it to
//     a 404 problem detail.
//
// The fake exists in the same package as the handler so test wiring is
// boring (newFakeMutatorState satisfies State, StateMutator, and
// FullPatchMutator with no extra plumbing). It's a separate file from
// fake_state_test.go because it adds a fork-only capability and we want
// upstream fakes to stay unmodified.
func (f *fakeMutatorState) ApplyAgentPatchFull(name string, patch config.AgentPatch) error {
	rawCfg := f.cfg
	if f.rawCfg != nil {
		rawCfg = f.rawCfg
	}

	switch configedit.AgentOrigin(rawCfg, f.cfg, name) {
	case configedit.OriginInline:
		// ApplyPatches finds the agent by qualified identity in
		// cfg.Agents and rewrites its fields in place. Wrapping the
		// single patch in a Patches{Agents: ...} envelope keeps the
		// fake in sync with whatever applyAgentPatchFields evolves to
		// in the upstream config package — no field-by-field copy in
		// the fake to drift.
		return config.ApplyPatches(rawCfg, config.Patches{Agents: []config.AgentPatch{patch}})
	case configedit.OriginDerived:
		merged := false
		for i := range rawCfg.Patches.Agents {
			existing := &rawCfg.Patches.Agents[i]
			if existing.Dir == patch.Dir && existing.Name == patch.Name {
				mergeFakeAgentPatch(existing, patch)
				merged = true
				break
			}
		}
		if !merged {
			rawCfg.Patches.Agents = append(rawCfg.Patches.Agents, patch)
		}
		return nil
	default:
		return fmt.Errorf("%w: agent %q", configedit.ErrNotFound, name)
	}
}

// mergeFakeAgentPatch applies non-nil/non-empty fields from src onto dst.
// Mirrors the production merge but only for the fields AgentPatchRequest
// surfaces today; expand alongside config.AgentPatch as new editable
// fields land in the studio.
func mergeFakeAgentPatch(dst *config.AgentPatch, src config.AgentPatch) {
	if src.Provider != nil {
		dst.Provider = src.Provider
	}
	if src.Scope != nil {
		dst.Scope = src.Scope
	}
	if src.Nudge != nil {
		dst.Nudge = src.Nudge
	}
	if src.WorkDir != nil {
		dst.WorkDir = src.WorkDir
	}
	if src.Suspended != nil {
		dst.Suspended = src.Suspended
	}
	if src.IdleTimeout != nil {
		dst.IdleTimeout = src.IdleTimeout
	}
	if src.SleepAfterIdle != nil {
		dst.SleepAfterIdle = src.SleepAfterIdle
	}
	if src.WakeMode != nil {
		dst.WakeMode = src.WakeMode
	}
	if len(src.Env) > 0 {
		if dst.Env == nil {
			dst.Env = make(map[string]string, len(src.Env))
		}
		for k, v := range src.Env {
			dst.Env[k] = v
		}
	}
	if len(src.PreStart) > 0 {
		dst.PreStart = append([]string(nil), src.PreStart...)
	}
	if len(src.InjectFragments) > 0 {
		dst.InjectFragments = append([]string(nil), src.InjectFragments...)
	}
	if src.Pool != nil {
		if dst.Pool == nil {
			dst.Pool = &config.PoolOverride{}
		}
		if src.Pool.Min != nil {
			dst.Pool.Min = src.Pool.Min
		}
		if src.Pool.Max != nil {
			dst.Pool.Max = src.Pool.Max
		}
		if src.Pool.Check != nil {
			dst.Pool.Check = src.Pool.Check
		}
		if src.Pool.DrainTimeout != nil {
			dst.Pool.DrainTimeout = src.Pool.DrainTimeout
		}
	}
}
