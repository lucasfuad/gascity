package main

import (
	"github.com/gastownhall/gascity/internal/config"
)

// ApplyAgentPatchFull satisfies the fork-only
// agentconfig.FullPatchMutator capability so the supervisor's HTTP
// PATCH /v0/city/{cityName}/agent/{base}/full handler can route through
// controllerState's editor + poke pattern, mirroring SuspendAgent and
// UpdateAgent. Lives in this fork-only file so adding the maestro
// editable surface doesn't touch the upstream cmd/gc/api_state.go.
//
// Contract details (validation, ETag/If-Match, request DTO mapping)
// are owned by the handler in internal/api/maestro_agentconfig.go and
// the persistence dispatch (Inline vs Derived) is owned by
// configedit.Editor.ApplyAgentPatchFull. This method's only job is
// plumbing: forward the patch and re-poke the supervisor so the
// session reconciler picks up the change.
func (cs *controllerState) ApplyAgentPatchFull(name string, patch config.AgentPatch) error {
	return cs.mutateAndPoke(func() error {
		return cs.editor.ApplyAgentPatchFull(name, patch)
	})
}
