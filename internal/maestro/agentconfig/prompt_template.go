package agentconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"time"
)

// PromptTemplateResponse is the wire shape for both GET and PUT
// /v0/city/{cityName}/agent/{base}/prompt-template responses.
//
// Path is the configured value of agent.PromptTemplate (as stored in
// city.toml) — not the host-resolved absolute path. Frontend never
// needs to know real filesystem layout; relative paths stay relative.
//
// Mtime is the file's modification time on disk after the operation
// completed. Informational only — optimistic concurrency uses the
// content-hash ETag, so a touch that doesn't change content does not
// invalidate cached reads.
type PromptTemplateResponse struct {
	Path    string    `json:"path" doc:"Configured prompt_template path (relative to city dir or absolute, as stored in city.toml)."`
	Content string    `json:"content" doc:"UTF-8 contents of the template file."`
	Mtime   time.Time `json:"mtime" doc:"Last modification time on disk (RFC 3339)."`
}

// PromptTemplatePutBody is the wire shape for PUT
// /v0/city/{cityName}/agent/{base}/prompt-template requests.
//
// Content is required. The body intentionally does NOT include path:
// the path is set via PATCH /full's prompt_template field, and this
// endpoint only writes content to the already-configured location.
type PromptTemplatePutBody struct {
	Content string `json:"content" required:"true" doc:"UTF-8 contents to write to the template file."`
}

// PromptTemplatePathResolution describes how an agent's
// prompt_template field maps onto the host filesystem.
//
// Configured echoes the raw path string from agent.PromptTemplate
// (after whitespace trim). Resolved is the cleaned absolute path the
// handler reads/writes. EscapesCityRoot is true when Resolved falls
// outside cityPath (pack-derived templates, absolute paths to other
// directories, or "../" traversal). Pack-derived templates are
// read-only via this endpoint by design — see SKILL.md "Decisões
// cravadas".
type PromptTemplatePathResolution struct {
	Configured      string
	Resolved        string
	EscapesCityRoot bool
}

// ResolvePromptTemplatePath cleans agentPath against cityPath. Returns
// ok=false when the agent has no prompt_template configured (path
// empty or whitespace-only) — handlers map that to a distinct 404
// "agent has no prompt_template configured" so the frontend can
// distinguish it from "file missing on disk".
//
// When ok=true, EscapesCityRoot must be checked before any FS write:
// pack-derived templates live outside the city directory and are
// read-only via this endpoint. Returning EscapesCityRoot=true keeps
// path-traversal exploits and accidental pack writes both contained
// at the same gate.
//
// Pure: no FS access. Resolution is filepath.Clean + filepath.Join
// (relative paths) or filepath.Clean alone (absolute paths). Escape
// detection uses filepath.Rel to catch ".." traversal even after
// Clean normalises the path so "/city/x/../../etc/passwd" is caught
// even though Clean would silently reduce it.
func ResolvePromptTemplatePath(cityPath, agentPath string) (PromptTemplatePathResolution, bool) {
	configured := strings.TrimSpace(agentPath)
	if configured == "" {
		return PromptTemplatePathResolution{}, false
	}

	cleanedCity := strings.TrimSpace(cityPath)
	resolved := configured
	if !filepath.IsAbs(resolved) && cleanedCity != "" {
		resolved = filepath.Join(cleanedCity, resolved)
	}
	resolved = filepath.Clean(resolved)

	escapes := true // defensive default: unknown city root means treat everything as outside
	if cleanedCity != "" {
		cleanedCity = filepath.Clean(cleanedCity)
		rel, err := filepath.Rel(cleanedCity, resolved)
		switch {
		case err != nil:
			escapes = true
		case rel == "..":
			escapes = true
		case strings.HasPrefix(rel, ".."+string(filepath.Separator)):
			escapes = true
		default:
			escapes = false
		}
	}

	return PromptTemplatePathResolution{
		Configured:      configured,
		Resolved:        resolved,
		EscapesCityRoot: escapes,
	}, true
}

// ComputePromptTemplateETag returns a stable opaque hash over the
// template's UTF-8 content. GET emits this as the ETag header; PUT
// validates If-Match against it. Wraps the hash in RFC 9110 strong
// validator quotes so the value is suitable for direct use as an
// HTTP ETag header.
//
// Distinct from ComputeAgentETag (which hashes AgentDefinition) so
// the same agent can have different ETags on /full vs
// /prompt-template — concurrency is per-resource, and content edits
// to the template file should not invalidate AgentDefinition caches.
func ComputePromptTemplateETag(content []byte) string {
	sum := sha256.Sum256(content)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// BuildPromptTemplateResponse builds the wire response for both GET
// and PUT. Path echoes the configured path (not the resolved host
// path) so frontend never sees absolute filesystem layout — keeps
// the SPA portable across operator machines.
func BuildPromptTemplateResponse(configuredPath string, content []byte, mtime time.Time) PromptTemplateResponse {
	return PromptTemplateResponse{
		Path:    configuredPath,
		Content: string(content),
		Mtime:   mtime,
	}
}
