package agentconfig

import (
	"crypto/sha256"
	"encoding/hex"
)

// Fragments discovery: list of named template fragments resolvable for
// a specific agent. Mirrors the supervisor's loadSharedTemplates
// contract (cmd/gc/prompt.go) — same directories, same priority order,
// same {{define}} extraction semantics.
//
// The endpoint is purely read: it scans the filesystem live on every
// call (no cache) so the UI never drifts from what the supervisor will
// actually resolve at agent boot. The ETag is the right place to skip
// repeat work — strong validator over the ordered (name, source, sha)
// sequence, served with `If-None-Match` → 304.

// FragmentRef is one discoverable fragment name plus the file it was
// loaded from and a content hash of that file. Source is relative to
// the city path so the wire view stays stable across hosts. SHA is
// per-file (not per-{{define}} block) — same file with two defines
// gets two FragmentRefs sharing the same SHA.
type FragmentRef struct {
	Name   string `json:"name" doc:"Fragment name as exposed via tmpl.Lookup (the X in {{define \"X\"}})."`
	Source string `json:"source" doc:"Path to the file containing the fragment, relative to the city path."`
	SHA    string `json:"sha" doc:"SHA-256 (hex, first 16 chars) of the source file's full contents."`
}

// FragmentsResponse is the wire shape for
// GET /v0/city/{cityName}/agent/{base}/fragments.
type FragmentsResponse struct {
	Fragments []FragmentRef `json:"fragments"`
}

// ComputeFragmentsETag returns an opaque content hash over the ordered
// sequence of (Name, Source, SHA). RFC 9110 strong validator: quoted
// hex prefix. Same shape as ComputePromptTemplateETag.
//
// Order-sensitive on purpose: a reorder of fragments in the response
// is a meaningful change for the UI (priority disambiguation visible
// to the user), so it should invalidate the cached representation.
func ComputeFragmentsETag(refs []FragmentRef) string {
	h := sha256.New()
	for _, r := range refs {
		h.Write([]byte(r.Name))
		h.Write([]byte{0})
		h.Write([]byte(r.Source))
		h.Write([]byte{0})
		h.Write([]byte(r.SHA))
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return "\"" + hex.EncodeToString(sum[:8]) + "\""
}
