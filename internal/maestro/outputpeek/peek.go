// Package outputpeek summarizes tool inputs and tool_result text for the
// human-readable conversation flatten emitted by the supervisor's
// /agent/output/turns endpoints.
//
// This package is part of the Maestro fork extension surface
// (internal/maestro/*). It is intentionally additive over the upstream
// internal/api transcript flatten so a future merge with upstream gas city
// causes minimal conflict (see frontend docs/architecture/fork-extension-strategy.md).
package outputpeek

import (
	"encoding/json"
	"strings"
)

// ShortMax bounds compact tool peeks (e.g. Bash command preview).
// PathMax bounds path-like tool peeks (file paths, URLs, glob patterns).
const (
	ShortMax = 80
	PathMax  = 120
)

// ToolResultPeekMax bounds tool_result text length in the conversation
// flatten — long enough to read meaningful command output, short enough
// that a runaway log doesn't dominate the chat surface. The full body is
// always available via the raw transcript endpoint (?format=raw).
const ToolResultPeekMax = 2000

// ForToolInput returns a single-line summary of the tool's input payload
// suitable for embedding inside the [Name: <peek>] marker the conversation
// flatten emits. Only known tools get a typed extractor; unknown tools
// return an empty string so the flatten degrades to the bare [Name] form.
//
// Length is capped per tool so the chat surface stays compact — the rich
// surface is the raw transcript view.
func ForToolInput(name string, input json.RawMessage) string {
	fn, ok := toolPeekers[name]
	if !ok {
		return ""
	}
	return fn(input)
}

type peekFn func(input json.RawMessage) string

// toolPeekers maps known tool names to a function that extracts the most
// useful single-line summary of the tool's input. Adding a new tool here
// is the right place — it keeps logic out of the upstream flatten switch.
var toolPeekers = map[string]peekFn{
	"Bash":     peekStringField("command", ShortMax),
	"Read":     peekStringField("file_path", PathMax),
	"Edit":     peekStringField("file_path", PathMax),
	"Write":    peekStringField("file_path", PathMax),
	"Glob":     peekStringField("pattern", PathMax),
	"Grep":     peekStringField("pattern", PathMax),
	"WebFetch": peekStringField("url", PathMax),
}

func peekStringField(field string, max int) peekFn {
	return func(input json.RawMessage) string {
		if len(input) == 0 {
			return ""
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(input, &m); err != nil {
			return ""
		}
		raw, ok := m[field]
		if !ok {
			return ""
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return ""
		}
		s = strings.TrimSpace(s)
		// Collapse newlines so the marker stays single-line.
		s = strings.ReplaceAll(s, "\n", " ")
		if len(s) > max {
			return s[:max-1] + "…"
		}
		return s
	}
}
