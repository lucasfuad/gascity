package agentconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/gastownhall/gascity/internal/config"
)

// AgentPatchRequest is the wire shape of PATCH /v0/city/{cityName}/agent/{base}/full
// (and the qualified variant). It mirrors the editable subset of
// config.AgentPatch — the fields the Agent Studio Identity, Lifecycle, and
// Scaling tabs surface.
//
// Pointer fields distinguish "absent" (leave unchanged) from "explicitly set
// to zero" (clear or set to empty/zero). Slices and maps use nil for absent.
//
// The shape is fork-only and lives in this package so the studio's wire
// surface is decoupled from upstream config.AgentPatch evolution. Mapping
// happens in BuildConfigAgentPatch.
//
// Description is intentionally absent: config.AgentPatch upstream does not
// expose it, so accepting it here would silently swallow user input. Add it
// once the upstream patch grows a Description field.
type AgentPatchRequest struct {
	Provider       *string `json:"provider,omitempty"`
	Scope          *string `json:"scope,omitempty" enum:"city,rig"`
	Nudge          *string `json:"nudge,omitempty"`
	WorkDir        *string `json:"work_dir,omitempty"`
	Suspended      *bool   `json:"suspended,omitempty"`
	IdleTimeout    *string `json:"idle_timeout,omitempty" doc:"Go duration string ('30s', '5m', '1h')."`
	SleepAfterIdle *string `json:"sleep_after_idle,omitempty" doc:"Duration string or 'off'."`
	WakeMode       *string `json:"wake_mode,omitempty" enum:"resume,fresh"`
	DrainTimeout   *string `json:"drain_timeout,omitempty" doc:"Go duration string."`
	ScaleCheck     *string `json:"scale_check,omitempty"`
	MinSessions    *int    `json:"min_active_sessions,omitempty" minimum:"0"`
	MaxSessions    *int    `json:"max_active_sessions,omitempty" minimum:"0"`

	Env      map[string]string `json:"env,omitempty"`
	PreStart []string          `json:"pre_start,omitempty"`
	// InjectFragments mirrors upstream config.AgentPatch.InjectFragments
	// presence-aware semantics (PR #1952): nil = leave unchanged; non-nil
	// empty pointer = clear; non-nil populated = replace.
	InjectFragments *[]string `json:"inject_fragments,omitempty"`
}

// BuildConfigAgentPatch maps an AgentPatchRequest into the upstream
// config.AgentPatch shape, with the (Dir, Name) targeting keys filled in
// from the URL path. Pure: no I/O, no validation. Validation lives in the
// handler layer where Huma's schema enforces enums and types before this
// runs.
//
// Scaling fields (MinSessions, MaxSessions, ScaleCheck, DrainTimeout)
// collapse into Pool *PoolOverride when at least one is set, mirroring
// how upstream's [pool] block works on AgentPatch.
func BuildConfigAgentPatch(req AgentPatchRequest, dir, name string) config.AgentPatch {
	p := config.AgentPatch{Dir: dir, Name: name}

	if req.Provider != nil {
		p.Provider = req.Provider
	}
	if req.Scope != nil {
		p.Scope = req.Scope
	}
	if req.Nudge != nil {
		p.Nudge = req.Nudge
	}
	if req.WorkDir != nil {
		p.WorkDir = req.WorkDir
	}
	if req.Suspended != nil {
		p.Suspended = req.Suspended
	}
	if req.IdleTimeout != nil {
		p.IdleTimeout = req.IdleTimeout
	}
	if req.SleepAfterIdle != nil {
		p.SleepAfterIdle = req.SleepAfterIdle
	}
	if req.WakeMode != nil {
		p.WakeMode = req.WakeMode
	}
	if len(req.Env) > 0 {
		p.Env = req.Env
	}
	if len(req.PreStart) > 0 {
		p.PreStart = req.PreStart
	}
	if req.InjectFragments != nil {
		p.InjectFragments = req.InjectFragments
	}

	if req.MinSessions != nil || req.MaxSessions != nil ||
		req.ScaleCheck != nil || req.DrainTimeout != nil {
		p.Pool = &config.PoolOverride{
			Min:          req.MinSessions,
			Max:          req.MaxSessions,
			Check:        req.ScaleCheck,
			DrainTimeout: req.DrainTimeout,
		}
	}

	return p
}

// ComputeAgentETag returns a stable opaque hash over the PATCH-mutable
// fields of the AgentDefinition. GET /full sets ETag to this value;
// PATCH /full validates If-Match against it. Determinism across map key
// insertion order is required so concurrent reads on the same snapshot
// don't produce drifting ETags.
//
// The returned string is suitable for direct use as an HTTP ETag header
// value (already wrapped per RFC 9110 § 8.8.3).
func ComputeAgentETag(def AgentDefinition) string {
	// encoding/json sorts map keys alphabetically, so identical content
	// hashes the same regardless of insertion order.
	raw, err := json.Marshal(def)
	if err != nil {
		// AgentDefinition has no unmarshalable types; this branch is
		// defensive against future struct evolution.
		return ""
	}
	sum := sha256.Sum256(raw)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}
