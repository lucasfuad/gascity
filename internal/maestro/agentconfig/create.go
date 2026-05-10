package agentconfig

import "github.com/gastownhall/gascity/internal/config"

// AgentCreateRequest is the wire shape of POST /v0/city/{cityName}/agent/{base}/full
// (and the qualified variant). It mirrors the editable subset of
// config.Agent — the same field set the Agent Studio's create wizard
// populates and that PATCH /full subsequently mutates.
//
// Unlike AgentPatchRequest, which uses pointer fields to distinguish
// "absent" from "explicitly set to zero", create-time fields are plain
// scalars: every omitted field falls back to the agent's zero value at
// city load. Identity comes from the URL path (Name = {base}, Dir =
// {dir} on the qualified variant); the body NEVER carries identity to
// keep the URL canonical.
//
// Provider is required (minLength:"1" + Huma schema enforcement) so
// the supervisor never registers an agent without a way to spawn
// sessions. Everything else is best-effort defaults.
type AgentCreateRequest struct {
	Provider       string `json:"provider" minLength:"1" doc:"Provider name registered in the city's providers list."`
	Description    string `json:"description,omitempty"`
	Scope          string `json:"scope,omitempty" enum:"city,rig"`
	WorkDir        string `json:"work_dir,omitempty"`
	Nudge          string `json:"nudge,omitempty"`
	Suspended      bool   `json:"suspended,omitempty"`
	IdleTimeout    string `json:"idle_timeout,omitempty" doc:"Go duration string ('30s', '5m', '1h'). Empty leaves the agent at the city default."`
	SleepAfterIdle string `json:"sleep_after_idle,omitempty" doc:"Duration string or 'off'."`
	WakeMode       string `json:"wake_mode,omitempty" enum:"resume,fresh"`
	DrainTimeout   string `json:"drain_timeout,omitempty" doc:"Go duration string."`
	ScaleCheck     string `json:"scale_check,omitempty"`
	MinSessions    *int   `json:"min_active_sessions,omitempty" minimum:"0"`
	MaxSessions    *int   `json:"max_active_sessions,omitempty" minimum:"0"`

	PromptTemplate  string            `json:"prompt_template,omitempty" doc:"Path to a prompt template file, relative to the city root."`
	Env             map[string]string `json:"env,omitempty"`
	PreStart        []string          `json:"pre_start,omitempty"`
	InjectFragments []string          `json:"inject_fragments,omitempty"`
}

// BuildConfigAgent maps an AgentCreateRequest into the upstream
// config.Agent shape used by Editor.CreateAgent. Pure: no I/O, no
// validation (Huma's schema layer enforced enums and required fields
// before this runs). Identity comes from the path params; the body's
// zero-valued fields stay zero-valued on the resulting agent so the
// city's load-time defaults apply.
//
// Sibling of BuildConfigAgentPatch — same translation discipline,
// scoped to create-time semantics.
func BuildConfigAgent(req AgentCreateRequest, dir, name string) config.Agent {
	a := config.Agent{
		Name:           name,
		Dir:            dir,
		Provider:       req.Provider,
		Description:    req.Description,
		Scope:          req.Scope,
		WorkDir:        req.WorkDir,
		Nudge:          req.Nudge,
		Suspended:      req.Suspended,
		IdleTimeout:    req.IdleTimeout,
		SleepAfterIdle: req.SleepAfterIdle,
		WakeMode:       req.WakeMode,
		DrainTimeout:   req.DrainTimeout,
		ScaleCheck:     req.ScaleCheck,
		PromptTemplate: req.PromptTemplate,
	}
	if req.MinSessions != nil {
		v := *req.MinSessions
		a.MinActiveSessions = &v
	}
	if req.MaxSessions != nil {
		v := *req.MaxSessions
		a.MaxActiveSessions = &v
	}
	if len(req.Env) > 0 {
		a.Env = make(map[string]string, len(req.Env))
		for k, v := range req.Env {
			a.Env[k] = v
		}
	}
	if len(req.PreStart) > 0 {
		a.PreStart = append([]string(nil), req.PreStart...)
	}
	if len(req.InjectFragments) > 0 {
		a.InjectFragments = append([]string(nil), req.InjectFragments...)
	}
	return a
}
