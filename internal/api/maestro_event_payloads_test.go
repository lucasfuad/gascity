package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/gastownhall/gascity/internal/events"
)

// TestAgentConfigUpdatedPayload_Registered verifies the typed-wire
// invariant (Principle 7) at the maestro fork's seam: the
// agent.config.updated event must round-trip through the events
// registry with AgentConfigUpdatedPayload as its sample, so the SSE
// projection emits a discriminated-union variant instead of falling
// through to the opaque envelope path. Without this, frontend
// subscribers would see the event but the OpenAPI spec would describe
// its payload as "unknown" and typed client codegen would break.
func TestAgentConfigUpdatedPayload_Registered(t *testing.T) {
	t.Parallel()

	sample, ok := events.LookupPayload(events.AgentConfigUpdated)
	if !ok {
		t.Fatalf("events.AgentConfigUpdated has no registered payload — internal/api/maestro_event_payloads.go init() did not run")
	}
	want := reflect.TypeOf(AgentConfigUpdatedPayload{})
	got := reflect.TypeOf(sample)
	if got != want {
		t.Fatalf("registered sample type = %v, want %v", got, want)
	}
}

// TestAgentConfigUpdatedPayload_JSONRoundtrip pins the wire encoding so
// a rename of a field on the Go struct (e.g. CityName → City) cannot
// silently break frontend consumers that key off the exact JSON names.
func TestAgentConfigUpdatedPayload_JSONRoundtrip(t *testing.T) {
	t.Parallel()

	in := AgentConfigUpdatedPayload{
		CityName:      "test-city",
		QualifiedName: "myrig/worker",
		ETag:          `"deadbeef00000000"`,
		Operation:     AgentConfigOperationUpdate,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"city_name":"test-city","qualified_name":"myrig/worker","etag":"\"deadbeef00000000\"","operation":"update"}`
	if string(raw) != want {
		t.Fatalf("wire JSON =\n  %s\nwant\n  %s", raw, want)
	}

	var out AgentConfigUpdatedPayload
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("roundtrip mismatch:\n  got  %+v\n  want %+v", out, in)
	}
}

// TestAgentConfigOperation_Constants pins the operation enum values.
// The discriminator on the wire is `operation: "create"|"update"`;
// renaming the Go consts is fine but the wire strings must not drift
// because frontend subscribers branch on them directly.
func TestAgentConfigOperation_Constants(t *testing.T) {
	t.Parallel()

	if AgentConfigOperationCreate != "create" {
		t.Errorf("AgentConfigOperationCreate = %q, want \"create\"", AgentConfigOperationCreate)
	}
	if AgentConfigOperationUpdate != "update" {
		t.Errorf("AgentConfigOperationUpdate = %q, want \"update\"", AgentConfigOperationUpdate)
	}
}
