package agentconfig

import "github.com/gastownhall/gascity/internal/config"

// FullPatchMutator is the fork-only capability the supervisor's State
// must implement to accept PATCH /v0/city/{cityName}/agent/{base}/full
// (and the qualified variant). Defined in this package, not in
// internal/api, so the fork-only handler stays decoupled from the
// upstream StateMutator interface and can be added without touching
// upstream-owned files.
//
// Real implementation lives on cmd/gc.controllerState; tests use a
// fake_state implementation in internal/api with the same method set.
// The handler type-asserts s.state.(FullPatchMutator) and returns
// a 501 problem detail when the assertion fails, mirroring the
// upstream pattern for StateMutator.
type FullPatchMutator interface {
	// ApplyAgentPatchFull applies patch to the agent identified by
	// name (qualified or bare). The implementation owns the
	// inline-vs-derived dispatch (see configedit.AgentOrigin) and is
	// expected to return configedit's typed sentinel errors so
	// mutationError can map them to the right HTTP status.
	ApplyAgentPatchFull(name string, patch config.AgentPatch) error
}
