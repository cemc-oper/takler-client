package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func sinceTestPage() QueryPage {
	page := queryTestPage(0, true, queryTestNode("/", "bunch"), queryTestNode("/flow", "flow"), queryTestNode("/flow/task", "task"))
	revision := int64(5)
	page.BaseRevision = &revision
	return page
}

func sinceTestCapabilities(enabled bool) QueryCapabilities {
	return QueryCapabilities{Kind: "capabilities", SchemaVersion: 1, Snapshot: true, Detail: true, Since: enabled,
		MaxPageNodes: QueryMaxPageNodes, MaxDecodedPageBytes: QueryMaxPageBytes, MaxChunkBytes: QueryMaxChunkBytes}
}

func sinceTestDelta(index int, complete bool, target int64, ops ...QueryOperation) QuerySince {
	if ops == nil {
		ops = []QueryOperation{}
	}
	page := QuerySince{Kind: "since", SchemaVersion: 1, SessionID: "session", QueryID: "query", BatchID: "batch",
		FromRevision: 5, TargetRevision: target, AsOf: "2026-10-08T00:00:00Z", ExpiresAt: "2026-10-08T00:02:00Z",
		PageIndex: index, Complete: complete, Unchanged: target == 5, Operations: ops}
	if !complete {
		cursor := "next"
		page.NextCursor = &cursor
	}
	return page
}

func sinceTestUpsert(path, status string) QueryOperation {
	node := queryTestNode(path, "task")
	if path == "/flow" {
		node.NodeKind, node.TypeID = "flow", "flow"
	}
	node.Status = status
	generation := "generation"
	return QueryOperation{Op: "upsert_summary", Path: path, FlowGeneration: &generation, Node: &node}
}

func TestQuerySinceStagesPagesAndSharesSparseBase(t *testing.T) {
	first := sinceTestDelta(0, false, 6, sinceTestUpsert("/flow", "active"))
	last := sinceTestDelta(1, true, 6, sinceTestUpsert("/flow/task", "active"))
	client, transport := queryTestClient(t, sinceTestPage(), sinceTestCapabilities(true), first, first, last)
	old, err := client.ReadQuerySnapshot(QuerySelection{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ReadQuerySince(old)
	if err != nil {
		t.Fatal(err)
	}
	oldTask, _ := old.Node("/flow/task")
	newTask, _ := result.Node("/flow/task")
	if old.Revision != 5 || result.Revision != 6 || oldTask.Status != "queued" || newTask.Status != "active" || len(result.overrides) != 2 || len(old.overrides) != 0 {
		t.Fatalf("delta did not atomically replace view: old=%+v new=%+v", oldTask, newTask)
	}
	if len(transport.requests) != 5 || transport.requests[4] != "since_request" {
		t.Fatalf("requests = %v", transport.requests)
	}
}

func TestQuerySnapshotRejectsChangedBaseRevisionAcrossPages(t *testing.T) {
	first := queryTestPage(0, false, queryTestNode("/", "bunch"), queryTestNode("/flow", "flow"))
	second := queryTestPage(1, true, queryTestNode("/flow/task", "task"))
	base, changed := int64(5), int64(6)
	first.BaseRevision, second.BaseRevision = &base, &changed
	client, _ := queryTestClient(t, first, second)
	if _, err := client.ReadQuerySnapshot(QuerySelection{}); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("revision mismatch = %v", err)
	}
	if client.CurrentQuerySnapshot() != nil {
		t.Fatal("published inconsistent base revision")
	}
}

func TestQuerySinceRejectsConflictingBatchAndKeepsWatermark(t *testing.T) {
	first := sinceTestDelta(0, false, 6, sinceTestUpsert("/flow", "active"))
	for _, tc := range []struct {
		name string
		last QuerySince
		want string
	}{
		{"duplicate", sinceTestDelta(1, true, 6, sinceTestUpsert("/flow", "active")), "duplicate"},
		{"order", sinceTestDelta(2, true, 6, sinceTestUpsert("/flow/task", "active")), "order"},
		{"target", sinceTestDelta(1, true, 7, sinceTestUpsert("/flow/task", "active")), "identity"},
		{"replay", sinceTestDelta(0, false, 6, sinceTestUpsert("/flow", "complete")), "replay"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := queryTestClient(t, sinceTestPage(), sinceTestCapabilities(true), first, tc.last)
			old, err := client.ReadQuerySnapshot(QuerySelection{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = client.ReadQuerySince(old); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v", err)
			}
			current := client.CurrentQuerySnapshot()
			if current.Revision != 5 || !current.IsStale("/flow/task") {
				t.Fatalf("failed batch changed watermark or freshness: %+v", current)
			}
		})
	}
}

func TestQuerySinceUnchangedAndFilteredEmpty(t *testing.T) {
	client, _ := queryTestClient(t, sinceTestPage(), sinceTestCapabilities(true), sinceTestDelta(0, true, 5))
	old, err := client.ReadQuerySnapshot(QuerySelection{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ReadQuerySince(old)
	if err != nil || result != old {
		t.Fatalf("unchanged result = %v, %v", result, err)
	}
	client, _ = queryTestClient(t, sinceTestPage(), sinceTestCapabilities(true), sinceTestDelta(0, true, 6))
	old, _ = client.ReadQuerySnapshot(QuerySelection{})
	result, err = client.ReadQuerySince(old)
	if err != nil || result.Revision != 6 || result.Len() != old.Len() {
		t.Fatalf("filtered result = %v, %v", result, err)
	}
}

func TestQuerySinceResetAndCapabilityDowngrade(t *testing.T) {
	reset := QueryReset{Kind: "reset", SchemaVersion: 1, SessionID: "session", QueryID: "query", SinceRevision: 5,
		ResetScope: "/", Reason: "history_expired"}
	client, _ := queryTestClient(t, sinceTestPage(), sinceTestCapabilities(true), reset)
	old, _ := client.ReadQuerySnapshot(QuerySelection{})
	_, err := client.ReadQuerySince(old)
	var typed *QueryFailureError
	if !errors.As(err, &typed) || typed.Code != "reset_required" || typed.ResetScope == nil || *typed.ResetScope != "/" || !client.CurrentQuerySnapshot().IsStale("/") {
		t.Fatalf("reset = %v", err)
	}
	client, _ = queryTestClient(t, sinceTestPage(), sinceTestCapabilities(false))
	old, _ = client.ReadQuerySnapshot(QuerySelection{})
	_, err = client.ReadQuerySince(old)
	if !errors.As(err, &typed) || typed.Code != "unsupported_capability" || !client.CurrentQuerySnapshot().IsStale("/") {
		t.Fatalf("downgrade = %v", err)
	}
}

func TestQuerySinceInvalidationAndRemoval(t *testing.T) {
	invalidate := QueryOperation{Op: "invalidate_scope", Path: "/flow/task", Reason: "structure_changed"}
	client, _ := queryTestClient(t, sinceTestPage(), sinceTestCapabilities(true), sinceTestDelta(0, true, 6, invalidate))
	old, _ := client.ReadQuerySnapshot(QuerySelection{})
	result, err := client.ReadQuerySince(old)
	if err != nil || !result.IsStale("/flow/task") || result.IsStale("/flow") {
		t.Fatalf("invalidation = %v, %v", result, err)
	}
	if _, err := client.ReadQueryDetail(result, "/flow/task", []string{"parameters"}); err == nil || !strings.Contains(err.Error(), "stale_identity") {
		t.Fatalf("stale detail = %v", err)
	}
	generation := "generation"
	remove := QueryOperation{Op: "remove_subtree", Path: "/flow/task", FlowGeneration: &generation}
	client, _ = queryTestClient(t, sinceTestPage(), sinceTestCapabilities(true), sinceTestDelta(0, true, 6, remove))
	old, _ = client.ReadQuerySnapshot(QuerySelection{})
	result, err = client.ReadQuerySince(old)
	if err != nil || result.Len() != 2 {
		t.Fatalf("removal = %v, %v", result, err)
	}
	if _, exists := result.Node("/flow/task"); exists {
		t.Fatal("removed task remained visible")
	}
}

func TestQuerySinceRejectsDetailDeltaInSummaryAndBoundsStaging(t *testing.T) {
	generation := "generation"
	attempt := "attempt-1"
	detail := QueryOperation{Op: "set_field", Path: "/flow/task", FlowGeneration: &generation,
		AttemptID: &attempt, Pointer: "/groups/runtime/task_id", Value: json.RawMessage(`null`)}
	client, _ := queryTestClient(t, sinceTestPage(), sinceTestCapabilities(true), sinceTestDelta(0, true, 6, detail))
	old, _ := client.ReadQuerySnapshot(QuerySelection{})
	if _, err := client.ReadQuerySince(old); err == nil || !strings.Contains(err.Error(), "unexpected detail") {
		t.Fatalf("detail delta = %v", err)
	}
	client, _ = queryTestClient(t, sinceTestPage(), sinceTestCapabilities(true), sinceTestDelta(0, true, 6, sinceTestUpsert("/flow/task", "active")))
	old, _ = client.ReadQuerySnapshot(QuerySelection{})
	old.baseBytes = QueryClientViewBudget - 1
	old.EstimatedBytes = old.baseBytes
	if _, err := client.ReadQuerySince(old); err == nil || !strings.Contains(err.Error(), "memory budget") {
		t.Fatalf("budget error = %v", err)
	}
	if current := client.CurrentQuerySnapshot(); current.Revision != 5 || !current.IsStale("/") {
		t.Fatalf("budget failure changed watermark or freshness: %+v", current)
	}
}

func TestQuerySyncRebuildsAfterLocalInvalidationAndStagesCLIOutput(t *testing.T) {
	invalidate := QueryOperation{Op: "invalidate_scope", Path: "/flow", Reason: "structure_changed"}
	client, transport := queryTestClient(t, sinceTestPage(), sinceTestCapabilities(true), sinceTestDelta(0, true, 6, invalidate), sinceTestPage())
	var output bytes.Buffer
	result, err := client.RunQuerySyncShow(QuerySelection{}, 1, 0, &output)
	if err != nil || result.Revision != 5 || len(result.StaleScopes()) != 0 || !strings.Contains(output.String(), "# revision=5 session=session") {
		t.Fatalf("sync = %v, %v, %q", result, err, output.String())
	}
	if len(transport.requests) != 4 {
		t.Fatalf("requests = %v", transport.requests)
	}
	if _, err := client.SyncQuery(QuerySelection{}, 0, time.Second); err == nil {
		t.Fatal("zero polls accepted")
	}
}

func TestQuerySyncRebuildsAfterSessionReset(t *testing.T) {
	reset := QueryReset{Kind: "reset", SchemaVersion: 1, SessionID: "session", QueryID: "query", SinceRevision: 5,
		ResetScope: "/", Reason: "session_changed"}
	client, transport := queryTestClient(t, sinceTestPage(), sinceTestCapabilities(true), reset, sinceTestPage())
	result, err := client.SyncQuery(QuerySelection{}, 1, 0)
	if err != nil || result == nil || result.IsStale("/") || result.Revision != 5 {
		t.Fatalf("session rebuild = %v, %v", result, err)
	}
	if len(transport.requests) != 4 {
		t.Fatalf("requests = %v", transport.requests)
	}
}
