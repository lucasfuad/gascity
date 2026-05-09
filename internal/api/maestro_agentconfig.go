package api

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/gastownhall/gascity/internal/maestro/agentconfig"
)

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

// humaHandleMaestroAgentGetFull is the Huma-typed handler for the
// fork-only GET /v0/city/{cityName}/agent/{base}/full (unqualified form).
func (s *Server) humaHandleMaestroAgentGetFull(ctx context.Context, input *MaestroAgentGetFullInput) (*IndexOutput[agentconfig.AgentFullResponse], error) {
	return s.maestroAgentFullByName(ctx, input.Name)
}

// humaHandleMaestroAgentGetFullQualified is the Huma-typed handler for
// the fork-only GET /v0/city/{cityName}/agent/{dir}/{base}/full
// (qualified form).
func (s *Server) humaHandleMaestroAgentGetFullQualified(ctx context.Context, input *MaestroAgentGetFullQualifiedInput) (*IndexOutput[agentconfig.AgentFullResponse], error) {
	return s.maestroAgentFullByName(ctx, input.QualifiedName())
}

// maestroAgentFullByName composes the upstream runtime view returned by
// agentByName with the fork-only AgentDefinition built from the same
// city Config snapshot. Read-only; PATCH support is a follow-up phase.
func (s *Server) maestroAgentFullByName(_ context.Context, name string) (*IndexOutput[agentconfig.AgentFullResponse], error) {
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

	return &IndexOutput[agentconfig.AgentFullResponse]{
		Index: runtime.Index,
		Body: agentconfig.AgentFullResponse{
			Runtime:    runtime.Body,
			Definition: agentconfig.BuildDefinition(&agentCfg),
		},
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
}
