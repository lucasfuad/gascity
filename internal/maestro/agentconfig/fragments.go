package agentconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/gastownhall/gascity/internal/fsys"
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

// ListAgentFragments scans the directories the supervisor reads at
// boot (cmd/gc/prompt.go:102-122, loadSharedTemplates priority order)
// and returns one FragmentRef per {{define "X"}} block exposed via
// tmpl.Lookup. Priority order (last wins on Name collision):
//
//  1. <packDir>/prompts/shared/           (lowest, one entry per packDir)
//  2. <packDir>/template-fragments/       (V2 pack-level)
//  3. <cityPath>/prompts/shared/          (city-level legacy)
//  4. <cityPath>/template-fragments/      (city-level V2)
//  5. <promptDir>/shared/                 (sibling of prompt_template)
//  6. <promptDir>/template-fragments/     (per-agent V2, highest)
//
// packDirs is the ordered list of resolved pack directories
// (config.City.PackDirs). It must be passed because real cities import
// fragments from system packs (gastown, bd, maintenance...) and a city
// without these dirs would silently return an empty list even when
// the supervisor would resolve dozens of fragments at boot. Empty
// packDirs is allowed for test fixtures with no pack imports.
//
// promptTemplate is the agent's configured prompt_template path
// (relative to cityPath, as stored in the composed config). Empty
// promptTemplate means the agent has no prompt configured — only the
// pack-level + city-level dirs are scanned. Agent resolution (mapping
// a request name to its config.Agent, including pack-imported and
// pool members) is the caller's responsibility; pass the result of
// findAgent here.
//
// Results are sorted by Name so the response is deterministic and the
// aggregate ETag is stable across calls. Collision resolution is by
// priority (last-wins on overwrite) and the winning Source field
// reflects which layer the entry was loaded from — order in the
// returned slice is a separate concern from priority semantics.
//
// "Bare" files (no {{define}}) are silently ignored — they only
// overwrite the root template at parse time, which is not what
// inject_fragments resolves via tmpl.Lookup. Individual file parse
// errors are silently skipped (parity with the supervisor's
// best-effort loadSharedTemplates).
func ListAgentFragments(fs fsys.FS, cityPath string, packDirs []string, promptTemplate string) ([]FragmentRef, error) {
	var promptDir string
	if promptTemplate != "" {
		promptDir = filepath.Dir(filepath.Join(cityPath, promptTemplate))
	}

	// Scan directories in supervisor priority order. Lower-priority
	// entries are inserted first; higher-priority entries overwrite
	// on name collision via the nameToRef map.
	dirs := make([]string, 0, 2*len(packDirs)+4)
	for _, pd := range packDirs {
		dirs = append(dirs,
			filepath.Join(pd, "prompts", "shared"),
			filepath.Join(pd, "template-fragments"),
		)
	}
	dirs = append(dirs,
		filepath.Join(cityPath, "prompts", "shared"),
		filepath.Join(cityPath, "template-fragments"),
	)
	if promptDir != "" {
		dirs = append(dirs,
			filepath.Join(promptDir, "shared"),
			filepath.Join(promptDir, "template-fragments"),
		)
	}

	nameToRef := make(map[string]FragmentRef)
	for _, dir := range dirs {
		collectFragmentsFromDir(fs, cityPath, dir, nameToRef)
	}

	refs := make([]FragmentRef, 0, len(nameToRef))
	for _, ref := range nameToRef {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].Name < refs[j].Name
	})
	return refs, nil
}

// collectFragmentsFromDir reads every supported template file in dir
// (.template.md and legacy .md.tmpl), parses each as text/template,
// and inserts one FragmentRef per {{define "X"}} block into out. The
// SHA is per-file; multiple defines in the same file share the same
// SHA. Missing dirs and individual file parse errors are silently
// skipped (parity with cmd/gc/prompt.go loadSharedTemplates).
func collectFragmentsFromDir(fs fsys.FS, cityPath, dir string, out map[string]FragmentRef) {
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return
	}
	for _, name := range sortedTemplateFileNames(entries) {
		fullPath := filepath.Join(dir, name)
		data, err := fs.ReadFile(fullPath)
		if err != nil {
			continue
		}
		defines, parseErr := extractDefineNames(string(data))
		if parseErr != nil {
			continue
		}
		sha := computeFileSHA(data)
		relSource, err := filepath.Rel(cityPath, fullPath)
		if err != nil {
			relSource = fullPath
		}
		for _, def := range defines {
			out[def] = FragmentRef{
				Name:   def,
				Source: relSource,
				SHA:    sha,
			}
		}
	}
}

// sortedTemplateFileNames returns the names of .template.md (canonical)
// and .md.tmpl (legacy) files in entries, with legacy first then
// canonical — same ordering as cmd/gc/prompt.go: sharedTemplateFileNames,
// so canonical defines override legacy defines of the same name when
// loaded sequentially.
func sortedTemplateFileNames(entries []os.DirEntry) []string {
	var legacy, canonical []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".md.tmpl"):
			legacy = append(legacy, name)
		case strings.HasSuffix(name, ".template.md"):
			canonical = append(canonical, name)
		}
	}
	sort.Strings(legacy)
	sort.Strings(canonical)
	return append(legacy, canonical...)
}

// computeFileSHA returns the first 16 hex chars of the SHA-256 of the
// file content. Short enough for terse JSON, long enough to be unique
// within a single pack.
func computeFileSHA(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:8])
}

// extractDefineNames parses content as a text/template and returns the
// names of every {{define "X"}} block, mirroring how the supervisor's
// loadSharedTemplates exposes templates via tmpl.Lookup. The root
// template name ("_root") is filtered out so "bare" files (no {{define}})
// do not produce phantom entries.
func extractDefineNames(content string) ([]string, error) {
	tmpl, err := template.New("_root").Parse(content)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, 4)
	for _, t := range tmpl.Templates() {
		if t.Name() == "_root" {
			continue
		}
		names = append(names, t.Name())
	}
	return names, nil
}
