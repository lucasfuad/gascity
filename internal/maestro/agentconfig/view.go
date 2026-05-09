// Package agentconfig holds the read-only and (eventually) read/write
// view of an agent's configured definition for the Maestro fork's
// /v0/city/{cityName}/agent/{base}/full endpoint.
//
// Phase 2 (read-only) lives here; PATCH support is added in a follow-up.
//
// This package is part of the Maestro fork extension surface
// (internal/maestro/*). It is intentionally additive over the upstream
// internal/api agent surface so a future merge with upstream gas city
// causes minimal conflict (see frontend
// docs/architecture/fork-extension-strategy.md).
//
// Composition note: the HTTP handler in internal/api/maestro_agentconfig.go
// reuses upstream's runtime view (agentResponse) for the Runtime field
// and asks BuildDefinition (this package) to map config.Agent into
// AgentDefinition. The split keeps the runtime-composition glue close to
// upstream's internal helpers while definition-side mapping stays
// fork-only.
package agentconfig

import (
	"github.com/gastownhall/gascity/internal/config"
)

// AgentFullResponse is the wire shape returned by GET /v0/city/{cityName}/agent/{base}/full
// (and /agent/{dir}/{base}/full). It is a superset of the existing GET
// /agent/{base} body: clients on supervisor v2 fetch this once and get
// both the runtime status and the configured definition.
//
// The Runtime field intentionally is opaque to this package (any) so the
// fork can reuse upstream's agentResponse without re-declaring a parallel
// struct that would drift on every upstream change. The api-package
// handler is responsible for filling Runtime with the same value it
// returns from GET /agent/{base}.
type AgentFullResponse struct {
	Runtime    any             `json:"runtime"`
	Definition AgentDefinition `json:"definition"`
}

// AgentDefinition is the fork-only projection of config.Agent fields the
// Agent Studio UI cares about. Only fields that are safe to expose on
// the wire are included — runtime-only and pack-internal markers are
// excluded by construction.
//
// The shape mirrors the proposal in
// frontend/docs/architecture/proposals/2026-05-09-agent-studio-supervisor-evolution.md
// § 2.2. PromptPreview is reserved for a future revision; Phase 2 leaves
// it empty until a /prompt-template endpoint exists.
type AgentDefinition struct {
	Name                string            `json:"name"`
	Description         string            `json:"description,omitempty"`
	Dir                 string            `json:"dir,omitempty"`
	WorkDir             string            `json:"work_dir,omitempty"`
	Scope               string            `json:"scope,omitempty"`
	Suspended           bool              `json:"suspended"`
	PreStart            []string          `json:"pre_start,omitempty"`
	PromptTemplate      string            `json:"prompt_template,omitempty"`
	PromptPreview       string            `json:"prompt_preview,omitempty"`
	Nudge               string            `json:"nudge,omitempty"`
	Session             string            `json:"session,omitempty"`
	Provider            string            `json:"provider,omitempty"`
	StartCommand        string            `json:"start_command,omitempty"`
	Args                []string          `json:"args,omitempty"`
	Env                 map[string]string `json:"env,omitempty"`
	OptionDefaults      map[string]string `json:"option_defaults,omitempty"`
	MaxActiveSessions   *int              `json:"max_active_sessions,omitempty"`
	MinActiveSessions   *int              `json:"min_active_sessions,omitempty"`
	ScaleCheck          string            `json:"scale_check,omitempty"`
	DrainTimeout        string            `json:"drain_timeout,omitempty"`
	IdleTimeout         string            `json:"idle_timeout,omitempty"`
	SleepAfterIdle      string            `json:"sleep_after_idle,omitempty"`
	WakeMode            string            `json:"wake_mode,omitempty"`
	InjectFragments     []string          `json:"inject_fragments,omitempty"`
	OverlayDir          string            `json:"overlay_dir,omitempty"`
	Namepool            string            `json:"namepool,omitempty"`
	WorkQuery           string            `json:"work_query,omitempty"`
	DefaultSlingFormula string            `json:"default_sling_formula,omitempty"`
}

// BuildDefinition maps the in-memory config.Agent value into the
// AgentDefinition wire shape. It is a pure function: no I/O, no
// side-effects, no access to runtime state. Maps are returned as their
// underlying reference (callers must not mutate the result), keeping the
// snapshot cheap to build for read-heavy endpoints.
//
// Fields that exist on config.Agent but are intentionally NOT exposed:
//   - Skills, MCP: tombstones (config/patch.go and AGENTS.md doctrine).
//   - SessionSetup, SessionSetupScript, SessionLive, OnBoot, OnDeath:
//     environment-mutating shell commands; route through dedicated
//     endpoints once a UX is decided.
//   - HooksInstalled, EmitsPermissionWarning, ProcessNames, ReadyDelayMs,
//     PromptMode/PromptFlag/ReadyPromptPrefix: provider-shaped advanced
//     fields the studio doesn't surface yet.
//   - Implicit, NamepoolNames, SourceDir, SharedSkills, SharedMCP,
//     SkillsDir, MCPDir, AppendFragments, InheritedDefaultSlingFormula:
//     runtime-only / pack-internal projections (json:"-" in upstream).
//   - DefaultSlingFormula is dereferenced (empty string when nil) so the
//     wire stays string-typed.
func BuildDefinition(a *config.Agent) AgentDefinition {
	if a == nil {
		return AgentDefinition{}
	}
	def := AgentDefinition{
		Name:              a.Name,
		Description:       a.Description,
		Dir:               a.Dir,
		WorkDir:           a.WorkDir,
		Scope:             a.Scope,
		Suspended:         a.Suspended,
		PreStart:          cloneStrings(a.PreStart),
		PromptTemplate:    a.PromptTemplate,
		Nudge:             a.Nudge,
		Session:           a.Session,
		Provider:          a.Provider,
		StartCommand:      a.StartCommand,
		Args:              cloneStrings(a.Args),
		Env:               cloneStringMap(a.Env),
		OptionDefaults:    cloneStringMap(a.OptionDefaults),
		MaxActiveSessions: cloneIntPtr(a.MaxActiveSessions),
		MinActiveSessions: cloneIntPtr(a.MinActiveSessions),
		ScaleCheck:        a.ScaleCheck,
		DrainTimeout:      a.DrainTimeout,
		IdleTimeout:       a.IdleTimeout,
		SleepAfterIdle:    a.SleepAfterIdle,
		WakeMode:          a.WakeMode,
		InjectFragments:   cloneStrings(a.InjectFragments),
		OverlayDir:        a.OverlayDir,
		Namepool:          a.Namepool,
		WorkQuery:         a.WorkQuery,
	}
	if a.DefaultSlingFormula != nil {
		def.DefaultSlingFormula = *a.DefaultSlingFormula
	}
	return def
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneIntPtr(in *int) *int {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}
