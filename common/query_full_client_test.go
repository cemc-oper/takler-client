package common

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func fullTestPage(t *testing.T, fields []string, row []any) FullSnapshotPage {
	t.Helper()
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	count := 1
	page := FullSnapshotPage{Kind: "full_snapshot_page", SchemaVersion: 1, SessionID: "session", FullSnapshotID: "snapshot", StateVersion: 5, AsOf: "2026-10-10T00:00:00Z", ScopePath: "/", FieldGroups: fields, SelectionHash: strings.Repeat("a", 64), Encoding: "compact_v1", PageIndex: 0, Complete: true, Nodes: []json.RawMessage{raw}, AncestorContext: []map[string]any{}, TotalNodes: &count}
	fullTestSeal(t, &page)
	return page
}
func fullTestSeal(t *testing.T, page *FullSnapshotPage) {
	t.Helper()
	metadata, err := fullMetadataCanonical(page.AncestorContext, page.Service)
	if err != nil {
		t.Fatal(err)
	}
	content := append(append([]byte{}, metadata...), '\n')
	size := int64(len(metadata))
	for _, raw := range page.Nodes {
		canonical, _, err := fullCanonical(raw)
		if err != nil {
			t.Fatal(err)
		}
		content = append(content, canonical...)
		content = append(content, '\n')
		size += int64(len(canonical))
	}
	digest := sha256.Sum256(content)
	hexDigest := hex.EncodeToString(digest[:])
	page.TotalBytes = &size
	page.SHA256 = &hexDigest
}
func fullTestCaps() QueryCapabilities {
	return QueryCapabilities{Kind: "capabilities", SchemaVersion: 1, Snapshot: true, Detail: true, Since: true, FullSnapshotV1: true, MaxPageNodes: QueryMaxPageNodes, MaxDecodedPageBytes: QueryMaxPageBytes, MaxChunkBytes: QueryMaxChunkBytes}
}
func fullTestRow() []any {
	return []any{"/", nil, []any{}, []any{"root", "bunch", "takler.bunch", "unknown", false, nil, nil}, []any{}}
}
func TestFullCanonicalMatchesPythonUnicodeEscaping(t *testing.T) {
	want := "[\"x" + "\u2028" + "y" + "\u2029" + "z\",\"\\\\u2028\"]"
	got, _, err := fullCanonical(json.RawMessage(want))
	if err != nil || string(got) != want {
		t.Fatalf("canonical = %q, %v; want %q", got, err, want)
	}
}
func TestFullSnapshotReadChecksCompletionAndReleasesLease(t *testing.T) {
	page := fullTestPage(t, []string{"summary"}, fullTestRow())
	cancel := CancelFullSnapshotReply{Kind: "cancel_full_snapshot_reply", SchemaVersion: 1, Cancelled: true}
	client, transport := queryTestClient(t, fullTestCaps(), page, cancel)
	view, err := client.ReadFullSnapshot(FullSnapshotSelection{FieldGroups: []string{"summary"}})
	if err != nil {
		t.Fatal(err)
	}
	if view.Complete.TotalNodes != 1 || view.NodesByPath["/"]["summary"].(map[string]any)["type_id"] != "takler.bunch" {
		t.Fatalf("view = %+v", view)
	}
	if strings.Join(transport.requests, ",") != "capabilities_request,full_snapshot_request,cancel_full_snapshot_request" {
		t.Fatalf("requests = %v", transport.requests)
	}
	if transport.opened != 1 || transport.closed != 1 {
		t.Fatalf("transport lifecycle = %d/%d", transport.opened, transport.closed)
	}
	page.SHA256 = nil
	client, _ = queryTestClient(t, fullTestCaps(), page, cancel)
	if _, err = client.ReadFullSnapshot(FullSnapshotSelection{FieldGroups: []string{"summary"}}); err == nil {
		t.Fatal("missing completion digest accepted")
	}
}
func TestFullSnapshotReadRejectsDigestAndUnsupportedCapability(t *testing.T) {
	page := fullTestPage(t, []string{"summary"}, fullTestRow())
	bad := strings.Repeat("0", 64)
	page.SHA256 = &bad
	cancel := CancelFullSnapshotReply{Kind: "cancel_full_snapshot_reply", SchemaVersion: 1, Cancelled: true}
	client, _ := queryTestClient(t, fullTestCaps(), page, cancel)
	if _, err := client.ReadFullSnapshot(FullSnapshotSelection{FieldGroups: []string{"summary"}}); err == nil || !strings.Contains(err.Error(), "completion mismatch") {
		t.Fatalf("digest error = %v", err)
	}
	page = fullTestPage(t, []string{"summary"}, fullTestRow())
	page.Service = map[string]any{"message": "tampered"}
	client, _ = queryTestClient(t, fullTestCaps(), page, cancel)
	if _, err := client.ReadFullSnapshot(FullSnapshotSelection{FieldGroups: []string{"summary"}}); err == nil || !strings.Contains(err.Error(), "completion mismatch") {
		t.Fatalf("metadata error = %v", err)
	}
	caps := fullTestCaps()
	caps.FullSnapshotV1 = false
	client, _ = queryTestClient(t, caps)
	if _, err := client.ReadFullSnapshot(FullSnapshotSelection{FieldGroups: []string{"summary"}}); err == nil || !strings.Contains(err.Error(), "unsupported_capability") {
		t.Fatalf("capability error = %v", err)
	}
}
func TestFullSnapshotReadResolvesVerifiedBlob(t *testing.T) {
	content := "long public value"
	digest := sha256.Sum256([]byte(content))
	hexDigest := hex.EncodeToString(digest[:])
	ref := map[string]any{"pointer": "/groups/parameters/user/0/value", "blob_id": "blob", "total_bytes": len(content), "sha256": hexDigest}
	row := []any{"/", nil, []any{}, []any{"root", "bunch", "takler.bunch", "unknown", false, nil, nil}, []any{[]any{[]any{[]any{"PAYLOAD", map[string]any{"blob_id": "blob"}, false}}, []any{}}}, []any{ref}}
	page := fullTestPage(t, []string{"summary", "parameters"}, row)
	chunk := FullSnapshotChunk{Kind: "full_snapshot_chunk", SchemaVersion: 1, SessionID: "session", FullSnapshotID: "snapshot", StateVersion: 5, AsOf: page.AsOf, BlobID: "blob", Offset: 0, TotalBytes: len(content), SHA256: hexDigest, DataBase64: base64.StdEncoding.EncodeToString([]byte(content)), Complete: true}
	cancel := CancelFullSnapshotReply{Kind: "cancel_full_snapshot_reply", SchemaVersion: 1, Cancelled: true}
	client, _ := queryTestClient(t, fullTestCaps(), page, chunk, cancel)
	view, err := client.ReadFullSnapshot(FullSnapshotSelection{FieldGroups: []string{"summary", "parameters"}})
	if err != nil {
		t.Fatal(err)
	}
	params := view.NodesByPath["/"]["groups"].(map[string]any)["parameters"].(map[string]any)["user"].([]any)
	if params[0].(map[string]any)["value"] != content {
		t.Fatalf("parameters = %v", params)
	}
	chunk.DataBase64 = base64.StdEncoding.EncodeToString([]byte("tampered"))
	client, _ = queryTestClient(t, fullTestCaps(), page, chunk, cancel)
	if _, err := client.ReadFullSnapshot(FullSnapshotSelection{FieldGroups: []string{"summary", "parameters"}}); err == nil {
		t.Fatal("tampered blob accepted")
	}
}
func TestFullSnapshotReadResolvesAncestorContextBlob(t *testing.T) {
	content := strings.Repeat("ancestor", 100)
	digest := sha256.Sum256([]byte(content))
	hexDigest := hex.EncodeToString(digest[:])
	ref := map[string]any{"pointer": "/ancestor_context/0/user/0/value", "blob_id": "ancestor-blob", "total_bytes": len(content), "sha256": hexDigest}
	row := append(fullTestRow(), []any{ref})
	page := fullTestPage(t, []string{"summary"}, row)
	page.AncestorContext = []map[string]any{{"source_path": "/parent", "user": []any{map[string]any{"name": "KEY", "value": map[string]any{"blob_id": "ancestor-blob"}}}, "generated": []any{}}}
	fullTestSeal(t, &page)
	chunk := FullSnapshotChunk{Kind: "full_snapshot_chunk", SchemaVersion: 1, SessionID: "session", FullSnapshotID: "snapshot", StateVersion: 5, AsOf: page.AsOf, BlobID: "ancestor-blob", Offset: 0, TotalBytes: len(content), SHA256: hexDigest, DataBase64: base64.StdEncoding.EncodeToString([]byte(content)), Complete: true}
	cancel := CancelFullSnapshotReply{Kind: "cancel_full_snapshot_reply", SchemaVersion: 1, Cancelled: true}
	client, _ := queryTestClient(t, fullTestCaps(), page, chunk, cancel)
	view, err := client.ReadFullSnapshot(FullSnapshotSelection{FieldGroups: []string{"summary"}})
	if err != nil {
		t.Fatal(err)
	}
	users := view.Complete.AncestorContext[0]["user"].([]any)
	if users[0].(map[string]any)["value"] != content {
		t.Fatalf("ancestor context = %v", users)
	}
}
func TestFullSnapshotReadResolvesServiceBlob(t *testing.T) {
	content := strings.Repeat("message", 100)
	digest := sha256.Sum256([]byte(content))
	hexDigest := hex.EncodeToString(digest[:])
	ref := map[string]any{"pointer": "/service/message", "blob_id": "service-blob", "total_bytes": len(content), "sha256": hexDigest}
	row := append(fullTestRow(), []any{ref})
	page := fullTestPage(t, []string{"summary", "service"}, row)
	page.Service = map[string]any{"message": map[string]any{"blob_id": "service-blob"}}
	fullTestSeal(t, &page)
	chunk := FullSnapshotChunk{Kind: "full_snapshot_chunk", SchemaVersion: 1, SessionID: "session", FullSnapshotID: "snapshot", StateVersion: 5, AsOf: page.AsOf, BlobID: "service-blob", Offset: 0, TotalBytes: len(content), SHA256: hexDigest, DataBase64: base64.StdEncoding.EncodeToString([]byte(content)), Complete: true}
	cancel := CancelFullSnapshotReply{Kind: "cancel_full_snapshot_reply", SchemaVersion: 1, Cancelled: true}
	client, _ := queryTestClient(t, fullTestCaps(), page, chunk, cancel)
	view, err := client.ReadFullSnapshot(FullSnapshotSelection{FieldGroups: []string{"summary", "service"}})
	if err != nil {
		t.Fatal(err)
	}
	if view.Complete.Service["message"] != content {
		t.Fatalf("service = %v", view.Complete.Service)
	}
}

func TestR3FullSnapshotPageFaultsDoNotPublishPartialView(t *testing.T) {
	cancel := CancelFullSnapshotReply{Kind: "cancel_full_snapshot_reply", SchemaVersion: 1, Cancelled: true}
	for _, tc := range []struct {
		name   string
		change func(*FullSnapshotPage)
		want   string
	}{
		{"duplicate page", func(page *FullSnapshotPage) { page.PageIndex = 0 }, "page order"},
		{"skipped page", func(page *FullSnapshotPage) { page.PageIndex = 2 }, "page order"},
		{"changed version", func(page *FullSnapshotPage) { page.PageIndex = 1; page.StateVersion++ }, "identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := fullTestPage(t, []string{"summary"}, fullTestRow())
			first.Complete = false
			cursor := "next"
			first.NextCursor = &cursor
			first.TotalNodes, first.TotalBytes, first.SHA256 = nil, nil, nil
			second := fullTestPage(t, []string{"summary"}, fullTestRow())
			second.AncestorContext = nil
			second.Service = nil
			tc.change(&second)
			client, transport := queryTestClient(t, fullTestCaps(), first, second, cancel)
			if _, err := client.ReadFullSnapshot(FullSnapshotSelection{FieldGroups: []string{"summary"}}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("fault result = %v", err)
			}
			if strings.Join(transport.requests, ",") != "capabilities_request,full_snapshot_request,full_snapshot_request,cancel_full_snapshot_request" {
				t.Fatalf("lease was not cancelled: %v", transport.requests)
			}
		})
	}
}
