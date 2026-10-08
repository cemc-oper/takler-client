package common

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type replayQueryTransport struct {
	Transport
	replies  [][]byte
	requests []string
	opened   int
	closed   int
}

func (t *replayQueryTransport) Open() error { t.opened++; return nil }
func (t *replayQueryTransport) Close()      { t.closed++ }
func (t *replayQueryTransport) Classify(err error) FailureVerdict {
	return FailureVerdict{ExitCode: ExitServerError, Name: "fake query", Details: err.Error()}
}
func (t *replayQueryTransport) QueryDocument(_ context.Context, kind string, raw []byte) ([]byte, error) {
	t.requests = append(t.requests, kind)
	if len(t.replies) == 0 {
		return nil, errors.New("no query reply")
	}
	reply := t.replies[0]
	t.replies = t.replies[1:]
	return reply, nil
}

func queryTestClient(t *testing.T, replies ...any) (*TaklerServiceClient, *replayQueryTransport) {
	t.Helper()
	client, err := NewTaklerServiceClient("localhost", "33083", TransportGrpc, SecurityLevels{})
	if err != nil {
		t.Fatal(err)
	}
	transport := &replayQueryTransport{}
	for _, reply := range replies {
		raw, err := json.Marshal(reply)
		if err != nil {
			t.Fatal(err)
		}
		transport.replies = append(transport.replies, raw)
	}
	client.transport = transport
	return client, transport
}

func queryTestNode(path, kind string) QueryNode {
	return QueryNode{Path: path, NodeKind: kind, TypeID: kind, Status: "queued"}
}
func queryTestPage(index int, complete bool, paths ...QueryNode) QueryPage {
	page := QueryPage{Kind: "page", SchemaVersion: 1, SessionID: "session", QueryID: "query", SnapshotID: "snapshot",
		AsOf: "2026-10-08T00:00:00Z", ExpiresAt: "2026-10-08T00:02:00Z", PageIndex: index,
		Complete: complete, RootName: "root", FlowGenerations: map[string]string{"/flow": "generation"}, Nodes: paths}
	if !complete {
		cursor := "cursor"
		page.NextCursor = &cursor
	}
	return page
}

func TestQuerySnapshotPublishesAfterCompletePages(t *testing.T) {
	first := queryTestPage(0, false, queryTestNode("/", "bunch"), queryTestNode("/flow", "flow"))
	second := queryTestPage(1, true, queryTestNode("/flow/task", "task"))
	client, transport := queryTestClient(t, first, second)
	snapshot, err := client.ReadQuerySnapshot(QuerySelection{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Len() != 3 || snapshot.ScopePath != "/" || snapshot.EstimatedBytes <= 1024 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if got := strings.Join(snapshot.Paths(), ","); got != "/,/flow,/flow/task" {
		t.Fatalf("paths = %s", got)
	}
	if client.CurrentQuerySnapshot() != snapshot {
		t.Fatal("complete view was not published")
	}
	if transport.opened != 1 || transport.closed != 1 {
		t.Fatalf("transport lifetime = %d/%d", transport.opened, transport.closed)
	}
	if got := strings.Join(transport.requests, ","); got != "page_request,page_request" {
		t.Fatalf("requests = %s", got)
	}
	previous := snapshot
	bad := queryTestPage(1, true, queryTestNode("/flow/task", "task"))
	bad.SnapshotID = "changed"
	for _, reply := range []any{first, bad} {
		raw, _ := json.Marshal(reply)
		transport.replies = append(transport.replies, raw)
	}
	if _, err := client.ReadQuerySnapshot(QuerySelection{}); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("error = %v", err)
	}
	if client.CurrentQuerySnapshot() != previous {
		t.Fatal("failed read replaced old view")
	}
}

func TestQuerySnapshotRejectsInvalidContinuationAndTypedFailure(t *testing.T) {
	first := queryTestPage(0, false, queryTestNode("/", "bunch"), queryTestNode("/flow", "flow"))
	for _, tc := range []struct {
		name  string
		reply any
		want  string
	}{
		{"duplicate", queryTestPage(1, true, queryTestNode("/flow", "flow")), "duplicate"},
		{"missing parent", queryTestPage(1, true, queryTestNode("/flow/missing/task", "task")), "parent"},
		{"wrong index", queryTestPage(2, true, queryTestNode("/flow/task", "task")), "order"},
		{"expired", QueryFailure{Kind: "error", SchemaVersion: 1, Code: "snapshot_expired", Message: "expired"}, "snapshot_expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := queryTestClient(t, first, tc.reply)
			_, err := client.ReadQuerySnapshot(QuerySelection{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v", err)
			}
			if client.CurrentQuerySnapshot() != nil {
				t.Fatal("published a partial view")
			}
			if tc.name == "expired" {
				var typed *QueryFailureError
				var exit *ExitError
				if !errors.As(err, &typed) || !errors.As(err, &exit) || exit.Code != ExitServerError {
					t.Fatalf("classification = %v", err)
				}
			}
		})
	}
}

func TestQueryDetailResolvesWideBlobAndAttempt(t *testing.T) {
	attempt := "attempt-1"
	task := queryTestNode("/flow/task", "task")
	task.AttemptID = &attempt
	page := queryTestPage(0, true, queryTestNode("/", "bunch"), queryTestNode("/flow", "flow"), task)
	value := "\"" + strings.Repeat("x", 300000) + "\""
	data := []byte(value)
	digest := sha256.Sum256(data)
	sha := hex.EncodeToString(digest[:])
	generation := "generation"
	detail := QueryDetail{Kind: "detail", SchemaVersion: 1, SessionID: "session", QueryID: "query", Path: "/flow/task", FlowGeneration: &generation, AttemptID: &attempt,
		SampleMode: "live", AsOf: "2026-10-08T00:00:01Z", RequestedGroups: []string{"parameters"},
		Groups:   map[string]map[string]any{"parameters": {"user": []any{map[string]any{"name": "WIDE", "value": nil}}}},
		BlobRefs: []QueryBlobRef{{Pointer: "/groups/parameters/user/0/value", BlobID: "blob", TotalBytes: len(data), SHA256: sha}}}
	replies := []any{page, detail}
	for offset := 0; offset < len(data); offset += QueryMaxChunkBytes {
		end := min(offset+QueryMaxChunkBytes, len(data))
		replies = append(replies, QueryChunk{Kind: "chunk", SchemaVersion: 1, BlobID: "blob", Offset: offset, TotalBytes: len(data), SHA256: sha,
			DataBase64: base64.StdEncoding.EncodeToString(data[offset:end]), Complete: end == len(data)})
	}
	client, transport := queryTestClient(t, replies...)
	snapshot, err := client.ReadQuerySnapshot(QuerySelection{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ReadQueryDetail(snapshot, "/flow/task", []string{"parameters"})
	if err != nil {
		t.Fatal(err)
	}
	users := queryObjectItems(result.Groups["parameters"]["user"])
	if got := users[0]["value"]; got != strings.Repeat("x", 300000) {
		t.Fatalf("value length = %d", len(fmt.Sprint(got)))
	}
	if len(result.BlobRefs) != 0 {
		t.Fatal("resolved detail still has refs")
	}
	if transport.opened != 2 || transport.closed != 2 {
		t.Fatalf("transport lifetime = %d/%d", transport.opened, transport.closed)
	}
}

func TestQueryBlobDigestAndShowFailureKeepOutputEmpty(t *testing.T) {
	page := queryTestPage(0, true, queryTestNode("/", "bunch"), queryTestNode("/flow", "flow"))
	client, _ := queryTestClient(t, page, QueryFailure{Kind: "error", SchemaVersion: 1, Code: "stale_identity", Message: "changed"})
	var output bytes.Buffer
	_, err := client.RunInitialQueryShow(QuerySelection{}, QueryShowOptions{Parameter: true}, &output)
	if err == nil || !strings.Contains(err.Error(), "stale_identity") {
		t.Fatalf("error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("partial output = %q", output.String())
	}
	if _, _, err := queryPointerParent(map[string]map[string]any{"parameters": {"user": []any{map[string]any{"value": nil}}}}, "/groups/parameters/user/-1/value"); err == nil {
		t.Fatal("negative blob index was accepted")
	}
}
