package api

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/maestro/agentconfig"
)

// errFullPatchNotSupported is the 501 used when the live State doesn't
// implement agentconfig.FullPatchMutator — i.e., a read-only deployment
// or a test wiring that forgot the mutator surface. Mirrors the upstream
// errMutationsNotSupported pattern in huma_types.go but stays in the
// fork-only file so adding it doesn't require touching upstream code.
var errFullPatchNotSupported = huma.Error501NotImplemented("agent full-patch mutations not supported")

// This file is part of the Maestro fork extension surface
// (internal/maestro/* + thin glue here). It is intentionally additive
// over the upstream internal/api agent surface so a future merge with
// upstream gas city causes minimal conflict (see frontend
// docs/architecture/fork-extension-strategy.md "Add, don't modify"
// principle).
//
// Glue rationale: the maestro/agentconfig package owns the wire DTOs
// and the pure config.Agent → AgentDefinition mapping. The upstream
// runtime composition (provider lookup, session info, active bead,
// computed state) lives behind the unexported agentResponse / findAgent
// / agentByName helpers in this package — re-implementing them in a
// fork-only package would duplicate non-trivial logic and drift on
// every upstream change. The split keeps drift exposure to a single
// thin handler file here while definition-side mapping stays fork-only
// in internal/maestro/agentconfig.

// MaestroAgentGetFullInput is the Huma input for
// GET /v0/city/{cityName}/agent/{base}/full.
type MaestroAgentGetFullInput struct {
	CityScope
	Name string `path:"base" doc:"Agent name (unqualified, no rig)."`
}

// MaestroAgentGetFullQualifiedInput is the Huma input for
// GET /v0/city/{cityName}/agent/{dir}/{base}/full.
type MaestroAgentGetFullQualifiedInput struct {
	CityScope
	Dir  string `path:"dir" doc:"Agent directory (rig name)."`
	Base string `path:"base" doc:"Agent base name."`
}

// QualifiedName joins dir and base into a canonical agent name.
func (i *MaestroAgentGetFullQualifiedInput) QualifiedName() string {
	return joinAgentQualifiedName(i.Dir, i.Base)
}

// MaestroAgentGetFullOutput mirrors IndexOutput[AgentFullResponse] but
// adds the ETag response header so studio clients can capture the
// definition's current content hash on read and replay it as If-Match
// on the next PATCH /full. ETag generation is centralized in
// agentconfig.ComputeAgentETag so GET and PATCH never disagree on the
// hash for a given snapshot.
type MaestroAgentGetFullOutput struct {
	Index uint64 `header:"X-GC-Index" doc:"Latest event sequence number."`
	ETag  string `header:"ETag" doc:"Opaque content hash of the agent definition. Use as If-Match on the next PATCH."`
	Body  agentconfig.AgentFullResponse
}

// humaHandleMaestroAgentGetFull is the Huma-typed handler for the
// fork-only GET /v0/city/{cityName}/agent/{base}/full (unqualified form).
func (s *Server) humaHandleMaestroAgentGetFull(ctx context.Context, input *MaestroAgentGetFullInput) (*MaestroAgentGetFullOutput, error) {
	return s.maestroAgentFullByName(ctx, input.Name)
}

// humaHandleMaestroAgentGetFullQualified is the Huma-typed handler for
// the fork-only GET /v0/city/{cityName}/agent/{dir}/{base}/full
// (qualified form).
func (s *Server) humaHandleMaestroAgentGetFullQualified(ctx context.Context, input *MaestroAgentGetFullQualifiedInput) (*MaestroAgentGetFullOutput, error) {
	return s.maestroAgentFullByName(ctx, input.QualifiedName())
}

// maestroAgentFullByName composes the upstream runtime view returned by
// agentByName with the fork-only AgentDefinition built from the same
// city Config snapshot, then attaches the definition's ETag for
// optimistic-concurrency clients. Shared by GET /full and the post-PATCH
// recomposition path so both surfaces read from one source of truth.
func (s *Server) maestroAgentFullByName(_ context.Context, name string) (*MaestroAgentGetFullOutput, error) {
	if name == "" {
		return nil, huma.Error400BadRequest("agent name required")
	}

	runtime, err := s.agentByName(name)
	if err != nil {
		return nil, err
	}

	agentCfg, ok := findAgent(s.state.Config(), name)
	if !ok {
		// agentByName already located the agent (otherwise it would have
		// returned 404 above), so the second lookup is defensive: it
		// catches the rare case where the snapshot rotated between calls.
		return nil, huma.Error404NotFound("agent " + name + " not found")
	}

	def := agentconfig.BuildDefinition(&agentCfg)
	return &MaestroAgentGetFullOutput{
		Index: runtime.Index,
		ETag:  agentconfig.ComputeAgentETag(def),
		Body: agentconfig.AgentFullResponse{
			Runtime:    runtime.Body,
			Definition: def,
		},
	}, nil
}

// MaestroAgentPatchFullInput is the Huma input for
// PATCH /v0/city/{cityName}/agent/{base}/full.
type MaestroAgentPatchFullInput struct {
	CityScope
	Name    string                        `path:"base" doc:"Agent name (unqualified, no rig)."`
	IfMatch string                        `header:"If-Match" doc:"ETag returned by the most recent GET /full. When present and stale, the request is rejected with 409 Conflict."`
	Body    agentconfig.AgentPatchRequest `contentType:"application/json"`
}

// MaestroAgentPatchFullQualifiedInput is the Huma input for
// PATCH /v0/city/{cityName}/agent/{dir}/{base}/full.
type MaestroAgentPatchFullQualifiedInput struct {
	CityScope
	Dir     string                        `path:"dir" doc:"Agent directory (rig name)."`
	Base    string                        `path:"base" doc:"Agent base name."`
	IfMatch string                        `header:"If-Match" doc:"ETag returned by the most recent GET /full. When present and stale, the request is rejected with 409 Conflict."`
	Body    agentconfig.AgentPatchRequest `contentType:"application/json"`
}

// QualifiedName joins dir and base into a canonical agent name.
func (i *MaestroAgentPatchFullQualifiedInput) QualifiedName() string {
	return joinAgentQualifiedName(i.Dir, i.Base)
}

// MaestroAgentPatchFullOutput mirrors IndexOutput[AgentFullResponse] but
// adds the ETag response header so PATCH callers can chain optimistic
// concurrency: the ETag returned here is the same hash GET /full would
// emit on the next read, valid as If-Match for the next PATCH.
type MaestroAgentPatchFullOutput struct {
	Index uint64 `header:"X-GC-Index" doc:"Latest event sequence number."`
	ETag  string `header:"ETag" doc:"Opaque content hash of the agent definition. Use as If-Match on the next PATCH."`
	Body  agentconfig.AgentFullResponse
}

// humaHandleMaestroAgentPatchFull is the Huma-typed handler for the
// fork-only PATCH /v0/city/{cityName}/agent/{base}/full (unqualified form).
func (s *Server) humaHandleMaestroAgentPatchFull(ctx context.Context, input *MaestroAgentPatchFullInput) (*MaestroAgentPatchFullOutput, error) {
	return s.maestroAgentPatchFullByName(ctx, input.Name, input.IfMatch, input.Body)
}

// humaHandleMaestroAgentPatchFullQualified is the Huma-typed handler for
// the qualified form.
func (s *Server) humaHandleMaestroAgentPatchFullQualified(ctx context.Context, input *MaestroAgentPatchFullQualifiedInput) (*MaestroAgentPatchFullOutput, error) {
	return s.maestroAgentPatchFullByName(ctx, input.QualifiedName(), input.IfMatch, input.Body)
}

// maestroAgentPatchFullByName is the shared dispatch for the qualified and
// unqualified PATCH /full handlers. It:
//
//  1. validates the agent exists (404 otherwise),
//  2. computes the current ETag and compares with If-Match (409 on stale),
//  3. dispatches the patch through the State's FullPatchMutator surface,
//  4. recomposes the response from the post-patch snapshot, returning
//     the new ETag so callers can chain optimistic-concurrency PATCHes.
//
// Step 4 calls back into maestroAgentFullByName so PATCH and GET share a
// single source of truth for the response shape.
func (s *Server) maestroAgentPatchFullByName(ctx context.Context, name, ifMatch string, body agentconfig.AgentPatchRequest) (*MaestroAgentPatchFullOutput, error) {
	if name == "" {
		return nil, huma.Error400BadRequest("agent name required")
	}

	mutator, ok := s.state.(agentconfig.FullPatchMutator)
	if !ok {
		return nil, errFullPatchNotSupported
	}

	pre, err := s.maestroAgentFullByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if ifMatch != "" && ifMatch != pre.ETag {
		return nil, huma.Error409Conflict("agent definition changed; refresh and retry")
	}

	dir, base := config.ParseQualifiedName(name)
	patch := agentconfig.BuildConfigAgentPatch(body, dir, base)
	if err := mutator.ApplyAgentPatchFull(name, patch); err != nil {
		return nil, mutationError(err)
	}

	post, err := s.maestroAgentFullByName(ctx, name)
	if err != nil {
		return nil, err
	}

	return &MaestroAgentPatchFullOutput{
		Index: post.Index,
		ETag:  post.ETag,
		Body:  post.Body,
	}, nil
}

// registerMaestroRoutes registers fork-only Huma operations on the
// supervisor's single Huma API. Called from NewSupervisorMux after
// the upstream registerCityRoutes; the single line in supervisor.go
// is the only edit to an upstream-owned function in this fork.
//
// Adding a new fork route is the only place to wire it: register it
// here, drop the handler method on *Server in this file (or another
// internal/api/maestro_*.go file), and the rest of the framework
// (CityScope binding, problem-detail responses, OpenAPI generation,
// middleware) flows through unchanged.
func (sm *SupervisorMux) registerMaestroRoutes() {
	cityGet(sm, "/agent/{base}/full", (*Server).humaHandleMaestroAgentGetFull)
	cityGet(sm, "/agent/{dir}/{base}/full", (*Server).humaHandleMaestroAgentGetFullQualified)
	cityPatch(sm, "/agent/{base}/full", (*Server).humaHandleMaestroAgentPatchFull)
	cityPatch(sm, "/agent/{dir}/{base}/full", (*Server).humaHandleMaestroAgentPatchFullQualified)
}
