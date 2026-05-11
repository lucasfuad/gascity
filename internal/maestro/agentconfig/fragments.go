package agentconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/gastownhall/gascity/internal/config"
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

// ErrAgentNotFound is returned by ListAgentFragments when the named
// agent is not present in the city configuration. The API layer maps
// this to a 404 application/problem+json.
type ErrAgentNotFound struct {
	Name string
}

func (e ErrAgentNotFound) Error() string {
	return "agent " + e.Name + " not found"
}

// ListAgentFragments scans the four directories the supervisor reads
// at boot (cmd/gc/prompt.go: loadSharedTemplates priority order) and
// returns one FragmentRef per {{define "X"}} block exposed via
// tmpl.Lookup. Priority order (last wins on Name collision):
//
//  1. pack/prompts/shared/             (lowest)
//  2. pack/template-fragments/
//  3. <agent prompt_template dir>/shared/
//  4. <agent prompt_template dir>/template-fragments/  (highest)
//
// "Bare" files (no {{define}}) are silently ignored — they only
// overwrite the root template at parse time, which is not what
// inject_fragments resolves via tmpl.Lookup.
//
// Returns ErrAgentNotFound when the agent is not present in
// city.toml; returns a wrapped config.Load error for malformed TOML;
// silently skips individual files with parse errors (parity with the
// supervisor's best-effort loadSharedTemplates).
func ListAgentFragments(fs fsys.FS, cityPath, agentBase string) ([]FragmentRef, error) {
	cfg, err := config.Load(fs, filepath.Join(cityPath, "city.toml"))
	if err != nil {
		return nil, err
	}

	var promptDir string
	found := false
	for _, a := range cfg.Agents {
		if a.Name == agentBase {
			found = true
			if a.PromptTemplate != "" {
				promptDir = filepath.Dir(filepath.Join(cityPath, a.PromptTemplate))
			}
			break
		}
	}
	if !found {
		return nil, ErrAgentNotFound{Name: agentBase}
	}

	// Scan directories in supervisor priority order. Lower-priority
	// entries are inserted first; higher-priority entries overwrite
	// on name collision via the nameToRef map.
	dirs := []string{
		filepath.Join(cityPath, "prompts", "shared"),
		filepath.Join(cityPath, "template-fragments"),
	}
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
