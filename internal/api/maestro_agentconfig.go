package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/danielgtaylor/huma/v2"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/fsys"
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

	s.recordAgentConfigUpdated(name, post.ETag, AgentConfigOperationUpdate)

	return &MaestroAgentPatchFullOutput{
		Index: post.Index,
		ETag:  post.ETag,
		Body:  post.Body,
	}, nil
}

// MaestroAgentCreateFullInput is the Huma input for
// POST /v0/city/{cityName}/agent/{base}/full. Identity comes from the
// URL path (Name = {base}, Dir = "" for the unqualified form); the body
// carries every other editable field of the new agent.
type MaestroAgentCreateFullInput struct {
	CityScope
	Name string                         `path:"base" doc:"Agent name (unqualified, no rig)."`
	Body agentconfig.AgentCreateRequest `contentType:"application/json"`
}

// MaestroAgentCreateFullQualifiedInput is the Huma input for
// POST /v0/city/{cityName}/agent/{dir}/{base}/full.
type MaestroAgentCreateFullQualifiedInput struct {
	CityScope
	Dir  string                         `path:"dir" doc:"Agent directory (rig name)."`
	Base string                         `path:"base" doc:"Agent base name."`
	Body agentconfig.AgentCreateRequest `contentType:"application/json"`
}

// QualifiedName joins dir and base into a canonical agent name.
func (i *MaestroAgentCreateFullQualifiedInput) QualifiedName() string {
	return joinAgentQualifiedName(i.Dir, i.Base)
}

// MaestroAgentCreateFullOutput mirrors MaestroAgentPatchFullOutput so
// successful creates and patches share a wire shape — the studio's
// create wizard hands the response off to the same code path the edit
// flow uses for read-after-write. The 201 status is set via the
// cityRegister Operation declaration in registerMaestroRoutes.
type MaestroAgentCreateFullOutput struct {
	Index uint64 `header:"X-GC-Index" doc:"Latest event sequence number."`
	ETag  string `header:"ETag" doc:"Opaque content hash of the agent definition. Use as If-Match on the next PATCH."`
	Body  agentconfig.AgentFullResponse
}

// humaHandleMaestroAgentCreateFull is the Huma-typed handler for the
// fork-only POST /v0/city/{cityName}/agent/{base}/full (unqualified form).
//
// Unlike the upstream POST /v0/city/{cityName}/agents (which takes
// name+dir+provider+scope in the body and returns a minimal "created"
// envelope), this endpoint reads identity from the URL path and returns
// the full AgentFullResponse plus ETag, so the Studio's create wizard
// can hand off to the same edit surface without an extra GET round-trip.
func (s *Server) humaHandleMaestroAgentCreateFull(ctx context.Context, input *MaestroAgentCreateFullInput) (*MaestroAgentCreateFullOutput, error) {
	return s.maestroAgentCreateFullByName(ctx, "", input.Name, input.Body)
}

// humaHandleMaestroAgentCreateFullQualified is the qualified
// (rig-scoped) variant of POST /full.
func (s *Server) humaHandleMaestroAgentCreateFullQualified(ctx context.Context, input *MaestroAgentCreateFullQualifiedInput) (*MaestroAgentCreateFullOutput, error) {
	return s.maestroAgentCreateFullByName(ctx, input.Dir, input.Base, input.Body)
}

// maestroAgentCreateFullByName is the shared dispatch for the qualified
// and unqualified POST /full handlers. It:
//
//  1. validates the path-derived agent name (400 otherwise),
//  2. maps the request body into a config.Agent via BuildConfigAgent,
//  3. asks the state's StateMutator to create the agent — duplicate
//     names surface as ErrAlreadyExists and become 409 via mutationError,
//  4. waits for the new agent to be reachable through findAgent (same
//     read-after-write contract as POST /v0/city/{cityName}/agents),
//  5. composes the response from the post-create snapshot through
//     maestroAgentFullByName, so POST/PATCH/GET share one source of
//     truth for the wire shape.
func (s *Server) maestroAgentCreateFullByName(ctx context.Context, dir, base string, body agentconfig.AgentCreateRequest) (*MaestroAgentCreateFullOutput, error) {
	if base == "" {
		return nil, huma.Error400BadRequest("agent name required")
	}

	mutator, ok := s.state.(StateMutator)
	if !ok {
		return nil, errMutationsNotSupported
	}

	agent := agentconfig.BuildConfigAgent(body, dir, base)
	if err := mutator.CreateAgent(agent); err != nil {
		return nil, mutationError(err)
	}

	qualifiedName := agent.QualifiedName()
	if waiter, ok := s.state.(AgentVisibilityWaiter); ok {
		waitCtx, cancel := context.WithTimeout(ctx, s.agentCreateVisibilityWaitTimeout())
		err := waiter.WaitForAgentVisibility(waitCtx, qualifiedName)
		cancel()
		if err != nil {
			return nil, agentVisibilityWaitHTTPError(err)
		}
	}

	post, err := s.maestroAgentFullByName(ctx, qualifiedName)
	if err != nil {
		return nil, err
	}

	s.recordAgentConfigUpdated(qualifiedName, post.ETag, AgentConfigOperationCreate)

	return &MaestroAgentCreateFullOutput{
		Index: post.Index,
		ETag:  post.ETag,
		Body:  post.Body,
	}, nil
}

// recordAgentConfigUpdated emits an agent.config.updated event on the
// per-city event bus so SSE subscribers can invalidate a cached agent
// definition (or refresh the agent list on create) without polling.
// Best-effort: silently skips if no event provider is configured —
// matching the contract used by recordMailEvent / recordExtMsgEvent
// for the upstream event surfaces.
//
// The payload's ETag must equal the one returned on the matching HTTP
// response header (callers are expected to pass the post-mutation
// ETag); see maestro_event_payloads.go for the wire shape.
func (s *Server) recordAgentConfigUpdated(qualifiedName, etag, operation string) {
	ep := s.state.EventProvider()
	if ep == nil {
		return
	}
	payload, err := json.Marshal(AgentConfigUpdatedPayload{
		CityName:      s.state.CityName(),
		QualifiedName: qualifiedName,
		ETag:          etag,
		Operation:     operation,
	})
	if err != nil {
		// Marshal can only fail on cyclic graphs / unsupported types;
		// every field above is a plain string. Dropping the event on
		// this impossible-in-practice path matches the best-effort
		// contract used by every other supervisor event emitter.
		return
	}
	ep.Record(events.Event{
		Type:    events.AgentConfigUpdated,
		Actor:   "maestro",
		Subject: qualifiedName,
		Payload: payload,
	})
}

// MaestroAgentGetPromptTemplateInput is the Huma input for
// GET /v0/city/{cityName}/agent/{base}/prompt-template.
type MaestroAgentGetPromptTemplateInput struct {
	CityScope
	Name string `path:"base" doc:"Agent name (unqualified, no rig)."`
}

// MaestroAgentGetPromptTemplateQualifiedInput is the Huma input for
// GET /v0/city/{cityName}/agent/{dir}/{base}/prompt-template.
type MaestroAgentGetPromptTemplateQualifiedInput struct {
	CityScope
	Dir  string `path:"dir" doc:"Agent directory (rig name)."`
	Base string `path:"base" doc:"Agent base name."`
}

// QualifiedName joins dir and base into a canonical agent name.
func (i *MaestroAgentGetPromptTemplateQualifiedInput) QualifiedName() string {
	return joinAgentQualifiedName(i.Dir, i.Base)
}

// MaestroAgentPromptTemplateOutput carries the GET/PUT response body
// plus the content-hash ETag header. Shared between the two verbs so
// optimistic-concurrency clients see the same wire shape from both —
// the GET ETag is valid as If-Match on the next PUT, and the PUT
// ETag is valid as If-Match on the PUT after that.
type MaestroAgentPromptTemplateOutput struct {
	ETag string `header:"ETag" doc:"Opaque content hash of the prompt template file. Use as If-Match on the next PUT."`
	Body agentconfig.PromptTemplateResponse
}

// MaestroAgentPutPromptTemplateInput is the Huma input for
// PUT /v0/city/{cityName}/agent/{base}/prompt-template.
type MaestroAgentPutPromptTemplateInput struct {
	CityScope
	Name    string                            `path:"base" doc:"Agent name (unqualified, no rig)."`
	IfMatch string                            `header:"If-Match" doc:"ETag returned by the most recent GET or PUT. When present and stale, the request is rejected with 409 Conflict. Empty skips optimistic concurrency."`
	Body    agentconfig.PromptTemplatePutBody `contentType:"application/json"`
}

// MaestroAgentPutPromptTemplateQualifiedInput is the Huma input for
// PUT /v0/city/{cityName}/agent/{dir}/{base}/prompt-template.
type MaestroAgentPutPromptTemplateQualifiedInput struct {
	CityScope
	Dir     string                            `path:"dir" doc:"Agent directory (rig name)."`
	Base    string                            `path:"base" doc:"Agent base name."`
	IfMatch string                            `header:"If-Match" doc:"ETag returned by the most recent GET or PUT. When present and stale, the request is rejected with 409 Conflict. Empty skips optimistic concurrency."`
	Body    agentconfig.PromptTemplatePutBody `contentType:"application/json"`
}

// QualifiedName joins dir and base into a canonical agent name.
func (i *MaestroAgentPutPromptTemplateQualifiedInput) QualifiedName() string {
	return joinAgentQualifiedName(i.Dir, i.Base)
}

// humaHandleMaestroAgentGetPromptTemplate is the Huma-typed handler
// for the fork-only GET .../agent/{base}/prompt-template (unqualified
// form). Reads the template content from disk and emits a
// content-hash ETag for optimistic-concurrency clients.
func (s *Server) humaHandleMaestroAgentGetPromptTemplate(_ context.Context, input *MaestroAgentGetPromptTemplateInput) (*MaestroAgentPromptTemplateOutput, error) {
	return s.maestroAgentReadPromptTemplate(input.Name)
}

// humaHandleMaestroAgentGetPromptTemplateQualified is the qualified
// (rig-scoped) variant of GET prompt-template.
func (s *Server) humaHandleMaestroAgentGetPromptTemplateQualified(_ context.Context, input *MaestroAgentGetPromptTemplateQualifiedInput) (*MaestroAgentPromptTemplateOutput, error) {
	return s.maestroAgentReadPromptTemplate(input.QualifiedName())
}

// humaHandleMaestroAgentPutPromptTemplate is the Huma-typed handler
// for the fork-only PUT .../agent/{base}/prompt-template. Validates
// optimistic concurrency via If-Match (when present), creates
// intermediate directories, and writes atomically (temp + rename) so
// readers never see a half-written file.
func (s *Server) humaHandleMaestroAgentPutPromptTemplate(_ context.Context, input *MaestroAgentPutPromptTemplateInput) (*MaestroAgentPromptTemplateOutput, error) {
	return s.maestroAgentWritePromptTemplate(input.Name, input.IfMatch, input.Body.Content)
}

// humaHandleMaestroAgentPutPromptTemplateQualified is the qualified
// (rig-scoped) variant of PUT prompt-template.
func (s *Server) humaHandleMaestroAgentPutPromptTemplateQualified(_ context.Context, input *MaestroAgentPutPromptTemplateQualifiedInput) (*MaestroAgentPromptTemplateOutput, error) {
	return s.maestroAgentWritePromptTemplate(input.QualifiedName(), input.IfMatch, input.Body.Content)
}

// maestroAgentReadPromptTemplate composes the file-on-disk view for
// GET .../prompt-template. The four error paths are intentionally
// distinguishable via problem+json detail so the studio can render
// targeted UX (configure a path, point at a different file, surface
// a pack-derived banner, etc.) rather than a generic 404.
func (s *Server) maestroAgentReadPromptTemplate(name string) (*MaestroAgentPromptTemplateOutput, error) {
	resolution, err := s.resolveAgentPromptTemplate(name)
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(resolution.Resolved)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, huma.Error404NotFound("prompt_template file not found at " + resolution.Configured)
		}
		return nil, huma.Error500InternalServerError("read prompt_template: " + err.Error())
	}
	info, err := os.Stat(resolution.Resolved)
	if err != nil {
		return nil, huma.Error500InternalServerError("stat prompt_template: " + err.Error())
	}
	return &MaestroAgentPromptTemplateOutput{
		ETag: agentconfig.ComputePromptTemplateETag(content),
		Body: agentconfig.BuildPromptTemplateResponse(resolution.Configured, content, info.ModTime()),
	}, nil
}

// maestroAgentWritePromptTemplate is the shared body for PUT handlers.
// Order of operations matters: locate agent → validate If-Match against
// current on-disk content → mkdir parents → atomic write → re-stat for
// the response mtime. If-Match check happens before any disk mutation
// so a stale request never even touches the filesystem.
func (s *Server) maestroAgentWritePromptTemplate(name, ifMatch, content string) (*MaestroAgentPromptTemplateOutput, error) {
	resolution, err := s.resolveAgentPromptTemplate(name)
	if err != nil {
		return nil, err
	}

	// Read current content for If-Match comparison. A missing file is
	// treated as "current content is empty bytes" so the operator can
	// PUT to create with If-Match: "<hash of empty>" if they want
	// strict create semantics, or just send an empty If-Match to skip
	// optimistic concurrency entirely.
	currentContent, err := os.ReadFile(resolution.Resolved)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, huma.Error500InternalServerError("read prompt_template: " + err.Error())
	}
	if ifMatch != "" {
		if ifMatch != agentconfig.ComputePromptTemplateETag(currentContent) {
			return nil, huma.Error409Conflict("prompt_template changed; refresh and retry")
		}
	}

	if err := os.MkdirAll(filepath.Dir(resolution.Resolved), 0o755); err != nil {
		return nil, huma.Error500InternalServerError("create prompt_template directory: " + err.Error())
	}
	newContent := []byte(content)
	if err := fsys.WriteFileAtomic(fsys.OSFS{}, resolution.Resolved, newContent, 0o644); err != nil {
		return nil, huma.Error500InternalServerError("write prompt_template: " + err.Error())
	}

	info, err := os.Stat(resolution.Resolved)
	if err != nil {
		return nil, huma.Error500InternalServerError("stat prompt_template: " + err.Error())
	}
	return &MaestroAgentPromptTemplateOutput{
		ETag: agentconfig.ComputePromptTemplateETag(newContent),
		Body: agentconfig.BuildPromptTemplateResponse(resolution.Configured, newContent, info.ModTime()),
	}, nil
}

// resolveAgentPromptTemplate is the shared GET/PUT preamble: locate
// the agent, resolve its prompt_template path against the city root,
// and surface the three pre-IO error branches (agent not found, no
// prompt_template configured, path escapes city root) as the right
// HTTP status. Filesystem-level errors (file missing, read failure)
// are caller-specific and handled in the GET/PUT bodies.
func (s *Server) resolveAgentPromptTemplate(name string) (agentconfig.PromptTemplatePathResolution, error) {
	if name == "" {
		return agentconfig.PromptTemplatePathResolution{}, huma.Error400BadRequest("agent name required")
	}
	agentCfg, ok := findAgent(s.state.Config(), name)
	if !ok {
		return agentconfig.PromptTemplatePathResolution{}, huma.Error404NotFound("agent " + name + " not found")
	}
	resolution, hasTemplate := agentconfig.ResolvePromptTemplatePath(s.state.CityPath(), agentCfg.PromptTemplate)
	if !hasTemplate {
		return agentconfig.PromptTemplatePathResolution{}, huma.Error404NotFound("agent " + name + " has no prompt_template configured")
	}
	if resolution.EscapesCityRoot {
		return agentconfig.PromptTemplatePathResolution{}, huma.Error403Forbidden("prompt_template lives outside city directory; pack-derived templates are read-only")
	}
	return resolution, nil
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
	// POST /full uses cityRegister so the 201 Created status is explicit
	// in the OpenAPI spec — distinguishes the create surface from the
	// PATCH/GET response (200) at the schema level.
	cityRegister(sm, huma.Operation{
		OperationID:   "maestro-create-agent-full",
		Method:        http.MethodPost,
		Path:          "/agent/{base}/full",
		Summary:       "Create an agent with the full editable subset",
		Description:   "Fork-only create endpoint mirroring PATCH /full's editable shape. Returns the post-create AgentFullResponse plus ETag, so the Studio's create wizard can hand off to the edit surface without a follow-up GET.",
		DefaultStatus: http.StatusCreated,
	}, (*Server).humaHandleMaestroAgentCreateFull)
	cityRegister(sm, huma.Operation{
		OperationID:   "maestro-create-agent-full-qualified",
		Method:        http.MethodPost,
		Path:          "/agent/{dir}/{base}/full",
		Summary:       "Create an agent in a rig with the full editable subset",
		Description:   "Rig-scoped variant of POST /agent/{base}/full.",
		DefaultStatus: http.StatusCreated,
	}, (*Server).humaHandleMaestroAgentCreateFullQualified)
	cityGet(sm, "/agent/{base}/prompt-template", (*Server).humaHandleMaestroAgentGetPromptTemplate)
	cityGet(sm, "/agent/{dir}/{base}/prompt-template", (*Server).humaHandleMaestroAgentGetPromptTemplateQualified)
	cityPut(sm, "/agent/{base}/prompt-template", (*Server).humaHandleMaestroAgentPutPromptTemplate)
	cityPut(sm, "/agent/{dir}/{base}/prompt-template", (*Server).humaHandleMaestroAgentPutPromptTemplateQualified)
}
