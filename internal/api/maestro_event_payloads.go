package api

import (
	"github.com/gastownhall/gascity/internal/events"
)

// Maestro-fork event payload types. This file lives in the fork
// extension surface (paired with internal/api/maestro_agentconfig.go)
// so a future merge with upstream gas city causes minimal conflict.
// The single line of overlap with upstream lives in
// internal/events/events.go (the AgentConfigUpdated constant + its
// entry in KnownEventTypes); every other moving part of the agent
// config event lives here or in maestro_agentconfig.go.

// Operation values for AgentConfigUpdatedPayload. The handler that
// emits the event picks one of these so SSE subscribers can branch on
// "first time seen" (create) versus "already in my list" (update)
// without diffing the agent set against their last snapshot.
const (
	// AgentConfigOperationCreate is emitted from POST /agent/{base}/full
	// after the new agent becomes visible through findAgent. Subscribers
	// should treat this as "new agent appeared in this city; refetch the
	// agent list if you cache it" rather than a configuration change of
	// an existing entry.
	AgentConfigOperationCreate = "create"
	// AgentConfigOperationUpdate is emitted from PATCH /agent/{base}/full
	// after the patch is persisted. Subscribers should invalidate any
	// cached agent definition + ETag keyed by (cityName, qualifiedName).
	AgentConfigOperationUpdate = "update"
)

// AgentConfigUpdatedPayload is the typed shape of the
// events.AgentConfigUpdated event. It carries enough state for an SSE
// subscriber to invalidate a precise React-Query / TanStack-Query key
// (`['agent-full', cityName, qualifiedName]`) without a follow-up
// fetch to learn which agent moved.
//
// ETag is the post-mutation content hash returned in the matching
// HTTP response header — including it on the event saves subscribers
// a round-trip when they want the new ETag to start an optimistic-
// concurrency PATCH chain immediately after another tab created the
// agent. Operation distinguishes the create path from the update
// path so subscribers can branch list-cache vs detail-cache
// invalidation without inspecting their own state.
type AgentConfigUpdatedPayload struct {
	// CityName is the city the agent belongs to. Required so the
	// supervisor-level /v0/events/stream (which multiplexes events from
	// every running city) can be filtered/routed by subscribers that
	// scope cache invalidation to the currently-open city.
	CityName string `json:"city_name" doc:"City the agent belongs to."`
	// QualifiedName is the canonical agent identifier (`{dir}/{base}`
	// for rig-scoped agents, `{base}` for city-scoped). Matches the
	// shape used by GET /agent/{...}/full and is the natural cache key
	// on the frontend side.
	QualifiedName string `json:"qualified_name" doc:"Qualified agent name (dir/base or base) — same shape used by GET /agent/{...}/full."`
	// ETag is the post-mutation content hash of the agent definition,
	// identical to the value returned in the matching HTTP response
	// ETag header. Subscribers can use it as If-Match on a follow-up
	// PATCH without re-reading.
	ETag string `json:"etag" doc:"Opaque content hash of the agent definition after the mutation. Same value as the ETag response header."`
	// Operation is "create" (POST /full) or "update" (PATCH /full).
	Operation string `json:"operation" enum:"create,update" doc:"Which write path produced the event: \"create\" for POST /full, \"update\" for PATCH /full."`
}

// IsEventPayload marks AgentConfigUpdatedPayload as an events.Payload variant.
func (AgentConfigUpdatedPayload) IsEventPayload() {}

func init() {
	events.RegisterPayload(events.AgentConfigUpdated, AgentConfigUpdatedPayload{})
}
