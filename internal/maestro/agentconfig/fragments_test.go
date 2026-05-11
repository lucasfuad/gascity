package agentconfig

import (
	"strings"
	"testing"
)

func TestComputeFragmentsETag_Deterministic(t *testing.T) {
	t.Parallel()
	refs := []FragmentRef{
		{Name: "safety", Source: "prompts/shared/safety.template.md", SHA: "abc"},
		{Name: "danger", Source: "prompts/shared/safety.template.md", SHA: "abc"},
	}
	a := ComputeFragmentsETag(refs)
	b := ComputeFragmentsETag(refs)
	if a != b {
		t.Errorf("non-deterministic: %q vs %q", a, b)
	}
	if a == "" {
		t.Error("ETag empty, want non-empty hash")
	}
}

func TestComputeFragmentsETag_FormatIsStrongValidator(t *testing.T) {
	t.Parallel()
	got := ComputeFragmentsETag([]FragmentRef{{Name: "x", Source: "y", SHA: "z"}})
	if !strings.HasPrefix(got, "\"") || !strings.HasSuffix(got, "\"") {
		t.Errorf("ETag = %q, want quoted strong validator", got)
	}
}

func TestComputeFragmentsETag_OrderSensitive(t *testing.T) {
	t.Parallel()
	a := ComputeFragmentsETag([]FragmentRef{
		{Name: "x", Source: "f1", SHA: "h1"},
		{Name: "y", Source: "f2", SHA: "h2"},
	})
	b := ComputeFragmentsETag([]FragmentRef{
		{Name: "y", Source: "f2", SHA: "h2"},
		{Name: "x", Source: "f1", SHA: "h1"},
	})
	if a == b {
		t.Error("ETag should differ when order differs (regression guard)")
	}
}

func TestComputeFragmentsETag_EmptyList(t *testing.T) {
	t.Parallel()
	got := ComputeFragmentsETag(nil)
	if got == "" {
		t.Error("ETag for nil list should still be non-empty quoted hash")
	}
}
