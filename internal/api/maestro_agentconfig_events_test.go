package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
)

// findAgentConfigUpdatedEvent returns the first agent.config.updated
// event recorded on prov, or nil. Returned by value so callers can
// inspect Type / Subject / Payload without locking the underlying
// slice — the fake event provider snapshots into Events on Record so
// the slice element doesn't move under us.
func findAgentConfigUpdatedEvent(t *testing.T, prov *events.Fake) *events.Event {
	t.Helper()
	for i := range prov.Events {
		if prov.Events[i].Type == events.AgentConfigUpdated {
			ev := prov.Events[i]
			return &ev
		}
	}
	return nil
}

// decodeAgentConfigUpdatedPayload decodes a recorded event's bytes
// payload into the typed AgentConfigUpdatedPayload, failing the test
// with the raw bytes on error so debugging a broken emission doesn't
// require enabling more verbose logging.
func decodeAgentConfigUpdatedPayload(t *testing.T, ev *events.Event) AgentConfigUpdatedPayload {
	t.Helper()
	if ev == nil {
		t.Fatal("decodeAgentConfigUpdatedPayload: event is nil")
	}
	var p AgentConfigUpdatedPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatalf("decode payload: %v\nraw=%s", err, ev.Payload)
	}
	return p
}

// TestMaestroAgentPatchFull_EmitsAgentConfigUpdated verifies that a
// successful PATCH /agent/{base}/full emits an agent.config.updated
// event whose payload identifies (city, qualified name, new ETag,
// operation="update"). This is Phase 11's primary contract: it gives
// other tabs / supervisor consumers enough information to invalidate a
// `['agent-full', cityName, qualifiedName]` cache key and refetch
// without any out-of-band knowledge of how the request flowed through.
//
// The event's ETag must equal the response ETag header — they are
// computed from the same post-mutation snapshot, and any drift would
// silently desync clients that use the event payload to seed the next
// optimistic-concurrency PATCH.
func TestMaestroAgentPatchFull_EmitsAgentConfigUpdated(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:        "mayor",
		Description: "city overseer",
		Provider:    "test-agent",
		IdleTimeout: "30m",
		WakeMode:    "resume",
	})
	h := newTestCityHandler(t, fs)

	req := newPatchRequest(cityURL(fs.fakeState, "/agent/mayor/full"), `{"idle_timeout":"2h"}`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	fake := fs.eventProv.(*events.Fake)
	ev := findAgentConfigUpdatedEvent(t, fake)
	if ev == nil {
		t.Fatalf("agent.config.updated event not emitted; got %d events: %+v",
			len(fake.Events), fake.Events)
	}
	if ev.Subject != "mayor" {
		t.Errorf("event.Subject = %q, want %q (qualified agent name)", ev.Subject, "mayor")
	}

	payload := decodeAgentConfigUpdatedPayload(t, ev)
	if payload.CityName != fs.cityName {
		t.Errorf("payload.CityName = %q, want %q", payload.CityName, fs.cityName)
	}
	if payload.QualifiedName != "mayor" {
		t.Errorf("payload.QualifiedName = %q, want %q", payload.QualifiedName, "mayor")
	}
	if payload.Operation != AgentConfigOperationUpdate {
		t.Errorf("payload.Operation = %q, want %q", payload.Operation, AgentConfigOperationUpdate)
	}
	wantETag := w.Header().Get("ETag")
	if wantETag == "" {
		t.Fatal("ETag response header missing — handler should always set it on 200")
	}
	if payload.ETag != wantETag {
		t.Errorf("payload.ETag = %q, want %q (response ETag header)", payload.ETag, wantETag)
	}
}

// TestMaestroAgentPatchFullQualified_EmitsAgentConfigUpdated mirrors
// the unqualified-name test for the rig-scoped route. The qualified
// name (`{dir}/{base}`) must flow into the payload's QualifiedName so
// subscribers can invalidate the exact cache key without re-parsing
// the URL.
func TestMaestroAgentPatchFullQualified_EmitsAgentConfigUpdated(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:        "polecat",
		Dir:         "myrig",
		Description: "rig polecat",
		Provider:    "test-agent",
		IdleTimeout: "30m",
		WakeMode:    "resume",
	})
	h := newTestCityHandler(t, fs)

	req := newPatchRequest(cityURL(fs.fakeState, "/agent/myrig/polecat/full"), `{"idle_timeout":"2h"}`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	fake := fs.eventProv.(*events.Fake)
	ev := findAgentConfigUpdatedEvent(t, fake)
	if ev == nil {
		t.Fatalf("agent.config.updated event not emitted; got %d events", len(fake.Events))
	}
	payload := decodeAgentConfigUpdatedPayload(t, ev)
	if payload.QualifiedName != "myrig/polecat" {
		t.Errorf("payload.QualifiedName = %q, want %q (rig-scoped qualified name)",
			payload.QualifiedName, "myrig/polecat")
	}
	if payload.Operation != AgentConfigOperationUpdate {
		t.Errorf("payload.Operation = %q, want %q", payload.Operation, AgentConfigOperationUpdate)
	}
}

// TestMaestroAgentPatchFull_NoEventOn404 verifies the failure-path
// contract: when the agent doesn't exist, the handler must short-
// circuit before emitting any event. Emitting on failure would lie to
// subscribers — they'd invalidate a cache key for an agent that never
// existed in this revision, then refetch and find nothing.
func TestMaestroAgentPatchFull_NoEventOn404(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	h := newTestCityHandler(t, fs)

	req := newPatchRequest(cityURL(fs.fakeState, "/agent/ghost/full"), `{"idle_timeout":"2h"}`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
	fake := fs.eventProv.(*events.Fake)
	if ev := findAgentConfigUpdatedEvent(t, fake); ev != nil {
		t.Fatalf("agent.config.updated must not be emitted on 404; got %+v", ev)
	}
}

// TestMaestroAgentPatchFull_NoEventOn409Stale verifies the failure-
// path contract for stale-If-Match collisions: the patch is rejected
// before mutation, so no event must escape. Without this gate two tabs
// fighting over the same ETag could both publish updates even when one
// of them was the losing rejected branch.
func TestMaestroAgentPatchFull_NoEventOn409Stale(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:        "mayor",
		Description: "city overseer",
		Provider:    "test-agent",
		IdleTimeout: "30m",
		WakeMode:    "resume",
	})
	h := newTestCityHandler(t, fs)

	req := newPatchRequest(cityURL(fs.fakeState, "/agent/mayor/full"), `{"idle_timeout":"2h"}`)
	req.Header.Set("If-Match", `"deadbeef00000000"`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	fake := fs.eventProv.(*events.Fake)
	if ev := findAgentConfigUpdatedEvent(t, fake); ev != nil {
		t.Fatalf("agent.config.updated must not be emitted on 409; got %+v", ev)
	}
}

// TestMaestroAgentCreateFull_EmitsAgentConfigUpdatedCreate verifies
// that a successful POST /agent/{base}/full emits an
// agent.config.updated event with Operation="create" so SSE
// subscribers can branch on first-sighting (refresh agent list) vs
// in-place update (invalidate one detail cache).
func TestMaestroAgentCreateFull_EmitsAgentConfigUpdatedCreate(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	h := newTestCityHandler(t, fs)

	req := newPostRequestJSON(
		cityURL(fs.fakeState, "/agent/scribe/full"),
		`{"provider":"test-agent","idle_timeout":"45m","wake_mode":"fresh"}`,
	)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
	}

	fake := fs.eventProv.(*events.Fake)
	ev := findAgentConfigUpdatedEvent(t, fake)
	if ev == nil {
		t.Fatalf("agent.config.updated event not emitted on create; got %d events", len(fake.Events))
	}
	if ev.Subject != "scribe" {
		t.Errorf("event.Subject = %q, want %q (qualified agent name)", ev.Subject, "scribe")
	}

	payload := decodeAgentConfigUpdatedPayload(t, ev)
	if payload.CityName != fs.cityName {
		t.Errorf("payload.CityName = %q, want %q", payload.CityName, fs.cityName)
	}
	if payload.QualifiedName != "scribe" {
		t.Errorf("payload.QualifiedName = %q, want %q", payload.QualifiedName, "scribe")
	}
	if payload.Operation != AgentConfigOperationCreate {
		t.Errorf("payload.Operation = %q, want %q (POST /full must emit create)",
			payload.Operation, AgentConfigOperationCreate)
	}
	wantETag := w.Header().Get("ETag")
	if wantETag == "" {
		t.Fatal("ETag response header missing — create must set it for the follow-up PATCH chain")
	}
	if payload.ETag != wantETag {
		t.Errorf("payload.ETag = %q, want %q (response ETag header)", payload.ETag, wantETag)
	}
}

// TestMaestroAgentCreateFullQualified_EmitsAgentConfigUpdatedCreate
// covers the rig-scoped POST /agent/{dir}/{base}/full variant so the
// qualified-name flow doesn't regress independently of the bare-name
// flow.
func TestMaestroAgentCreateFullQualified_EmitsAgentConfigUpdatedCreate(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	h := newTestCityHandler(t, fs)

	req := newPostRequestJSON(
		cityURL(fs.fakeState, "/agent/myrig/polecat/full"),
		`{"provider":"test-agent","idle_timeout":"45m","wake_mode":"fresh"}`,
	)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
	}

	fake := fs.eventProv.(*events.Fake)
	ev := findAgentConfigUpdatedEvent(t, fake)
	if ev == nil {
		t.Fatalf("agent.config.updated event not emitted on qualified create; got %d events", len(fake.Events))
	}
	payload := decodeAgentConfigUpdatedPayload(t, ev)
	if payload.QualifiedName != "myrig/polecat" {
		t.Errorf("payload.QualifiedName = %q, want %q", payload.QualifiedName, "myrig/polecat")
	}
	if payload.Operation != AgentConfigOperationCreate {
		t.Errorf("payload.Operation = %q, want %q", payload.Operation, AgentConfigOperationCreate)
	}
}

// TestMaestroAgentPatchFull_NilEventProviderIsBestEffort verifies that
// a deployment without an event provider configured (events.Provider
// = nil — e.g. read-only / diagnostic supervisor) still completes the
// mutation and returns the normal 200 response. The contract here
// matches recordMailEvent and the other supervisor emitters:
// emission is best-effort, so a missing provider must not surface as
// a 500 or block the write path.
func TestMaestroAgentPatchFull_NilEventProviderIsBestEffort(t *testing.T) {
	t.Parallel()

	fs := newFakeMutatorState(t)
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:        "mayor",
		Description: "city overseer",
		Provider:    "test-agent",
		IdleTimeout: "30m",
		WakeMode:    "resume",
	})
	fs.eventProv = nil
	h := newTestCityHandler(t, fs)

	req := newPatchRequest(cityURL(fs.fakeState, "/agent/mayor/full"), `{"idle_timeout":"2h"}`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
}

// TestMaestroAgentCreateFull_NoEventOn409Duplicate verifies the
// failure-path contract for POST /full: if CreateAgent rejects the
// request (duplicate name → ErrAlreadyExists → 409), no event must
// escape. This matches the PATCH failure-path test above for
// symmetric coverage.
func TestMaestroAgentCreateFull_NoEventOn409Duplicate(t *testing.T) {
	t.Parallel()

	inner := newFakeMutatorState(t)
	fs := &maestroDupCheckState{fakeMutatorState: inner}
	fs.cfg.Agents = append(fs.cfg.Agents, config.Agent{
		Name:     "scribe",
		Provider: "test-agent",
	})
	h := newTestCityHandler(t, fs)

	req := newPostRequestJSON(
		cityURL(inner.fakeState, "/agent/scribe/full"),
		`{"provider":"test-agent"}`,
	)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	fake := inner.eventProv.(*events.Fake)
	if ev := findAgentConfigUpdatedEvent(t, fake); ev != nil {
		t.Fatalf("agent.config.updated must not be emitted on 409 duplicate; got %+v", ev)
	}
}
