package configedit

import (
	"fmt"

	"github.com/gastownhall/gascity/internal/config"
)

// ApplyAgentPatchFull applies a config.AgentPatch to the agent named by
// name, dispatching on agent provenance:
//
//   - OriginInline: the patch is applied directly to the matching
//     [[agent]] entry in city.toml via config.ApplyPatches, which
//     mutates the in-memory struct in place. Subsequent fields the
//     PATCH did not touch are untouched. The Editor's standard
//     post-edit validation pass still runs.
//   - OriginDerived: the patch is merged into the existing
//     [[patches.agent]] block (or a new one is appended if none exists
//     yet) so pack-declared agents keep their override entirely in
//     city.toml — pack files stay read-only.
//   - OriginNotFound: returns an error wrapping ErrNotFound so the API
//     layer can translate to 404 via mutationError.
//
// This is the durable mutation path for the fork-only PATCH /full
// endpoint. The handler in internal/api/maestro_agentconfig.go owns
// schema validation and ETag/If-Match concurrency control; the Editor's
// job here is only persistence with provenance.
func (e *Editor) ApplyAgentPatchFull(name string, patch config.AgentPatch) error {
	return e.EditExpanded(func(raw, expanded *config.City) error {
		switch AgentOrigin(raw, expanded, name) {
		case OriginInline:
			// Pin the patch's identity to the agent we resolved, in
			// case the caller passed a bare name and the agent is
			// rig-scoped — config.ApplyPatches matches on (Dir, Name).
			patch.Dir, patch.Name = config.ParseQualifiedName(name)
			return config.ApplyPatches(raw, config.Patches{Agents: []config.AgentPatch{patch}})
		case OriginDerived:
			// AddOrUpdateAgentPatch finds-or-appends a [[patches.agent]]
			// entry. The merge callback only copies non-nil/non-empty
			// fields so a second PATCH that sets just one field does
			// not erase fields persisted by an earlier PATCH.
			return AddOrUpdateAgentPatch(raw, name, func(existing *config.AgentPatch) {
				mergeAgentPatchForFull(existing, patch)
			})
		case OriginNotFound:
			return fmt.Errorf("%w: agent %q", ErrNotFound, name)
		}
		return fmt.Errorf("agent %q: unknown origin", name)
	})
}

// mergeAgentPatchForFull copies non-nil/non-empty fields from src into
// dst. Mirrors config.applyAgentPatchFields for the subset of fields the
// studio's PATCH /full surfaces — Identity, Lifecycle, Scaling, Env, and
// the inject_fragments / pre_start lists. The intentional subset means
// upstream fields that grow on AgentPatch (e.g. session_setup,
// install_agent_hooks) are not auto-forwarded here; that matches the
// fork's stance that each new editable field needs an explicit decision
// in the studio surface before it leaks into a [[patches.agent]] block.
func mergeAgentPatchForFull(dst *config.AgentPatch, src config.AgentPatch) {
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
	if src.InjectFragments != nil {
		cloned := append([]string(nil), (*src.InjectFragments)...)
		dst.InjectFragments = &cloned
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
