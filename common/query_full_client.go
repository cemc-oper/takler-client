package common

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"strconv"
	"strings"
	"time"
)

const FullSnapshotAssemblyBudget = 640 * 1024 * 1024
const FullSnapshotEncodedBudget = 512 * 1024 * 1024

type FullSnapshotSelection struct {
	ScopePath   string
	Depth       *int
	FieldGroups []string
}
type FullSnapshotComplete struct {
	SessionID       string           `json:"session_id"`
	FullSnapshotID  string           `json:"full_snapshot_id"`
	StateVersion    int64            `json:"state_version"`
	AsOf            string           `json:"as_of"`
	ScopePath       string           `json:"scope_path"`
	TotalNodes      int              `json:"total_nodes"`
	TotalBytes      int64            `json:"total_bytes"`
	SHA256          string           `json:"sha256"`
	AncestorContext []map[string]any `json:"ancestor_context"`
	Service         map[string]any   `json:"service"`
}
type FullSnapshot struct {
	Complete    FullSnapshotComplete
	NodesByPath map[string]map[string]any
}

func fullCanonical(raw json.RawMessage) ([]byte, []any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var record []any
	if err := decoder.Decode(&record); err != nil {
		return nil, nil, err
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(record); err != nil {
		return nil, nil, err
	}
	// encoding/json always escapes U+2028/U+2029. The Python server's
	// ensure_ascii=False canonical form leaves these code points as UTF-8.
	canonical := bytes.TrimSuffix(out.Bytes(), []byte("\n"))
	var normalized bytes.Buffer
	for i := 0; i < len(canonical); {
		if canonical[i] != '\\' {
			normalized.WriteByte(canonical[i])
			i++
			continue
		}
		start := i
		for i < len(canonical) && canonical[i] == '\\' {
			i++
		}
		slashes := i - start
		if slashes%2 == 1 && i+5 <= len(canonical) &&
			(bytes.Equal(canonical[i:i+5], []byte("u2028")) || bytes.Equal(canonical[i:i+5], []byte("u2029"))) {
			normalized.Write(canonical[start : i-1])
			if canonical[i+4] == '8' {
				normalized.WriteString("\u2028")
			} else {
				normalized.WriteString("\u2029")
			}
			i += 5
		} else {
			normalized.Write(canonical[start:i])
		}
	}
	return normalized.Bytes(), record, nil
}
func fullMetadataCanonical(context []map[string]any, service map[string]any) ([]byte, error) {
	raw, err := json.Marshal([]any{map[string]any{"ancestor_context": context, "service": service}})
	if err != nil {
		return nil, err
	}
	canonical, _, err := fullCanonical(raw)
	if err != nil {
		return nil, err
	}
	return canonical[1 : len(canonical)-1], nil
}
func fullPointer(record map[string]any, pointer string) (any, string, error) {
	parts := strings.Split(pointer, "/")
	if len(parts) < 3 || parts[0] != "" || (parts[1] != "groups" && parts[1] != "ancestor_context" && parts[1] != "service") {
		return nil, "", errors.New("invalid full blob pointer")
	}
	var current any = record
	for _, part := range parts[1 : len(parts)-1] {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch value := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = value[part]
			if !ok {
				return nil, "", errors.New("invalid full blob pointer")
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(value) {
				return nil, "", errors.New("invalid full blob pointer")
			}
			current = value[index]
		case []map[string]any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(value) {
				return nil, "", errors.New("invalid full blob pointer")
			}
			current = value[index]
		default:
			return nil, "", errors.New("invalid full blob pointer")
		}
	}
	key := strings.ReplaceAll(strings.ReplaceAll(parts[len(parts)-1], "~1", "/"), "~0", "~")
	return current, key, nil
}
func (c *TaklerServiceClient) fullBlob(transport queryDocumentTransport, page *FullSnapshotPage, ref map[string]any, deadline time.Time) (string, error) {
	id, ok := ref["blob_id"].(string)
	if !ok {
		return "", queryProtocolError("invalid full blob id")
	}
	size, err := intNumber(ref["total_bytes"])
	if err != nil || size <= 0 || size > 16*1024*1024 {
		return "", queryProtocolError("invalid full blob size")
	}
	digest, ok := ref["sha256"].(string)
	if !ok {
		return "", queryProtocolError("invalid full blob digest")
	}
	data := make([]byte, 0, size)
	for len(data) < size {
		if time.Now().After(deadline) {
			return "", queryProtocolError("full snapshot total deadline exceeded")
		}
		value, err := c.queryDocument(transport, "full_snapshot_chunk_request", FullSnapshotChunkRequest{
			Kind: "full_snapshot_chunk_request", SchemaVersion: 1, SessionID: page.SessionID,
			FullSnapshotID: page.FullSnapshotID, BlobID: id, Offset: len(data)}, "full_snapshot_chunk")
		if err != nil {
			return "", err
		}
		chunk := value.(*FullSnapshotChunk)
		if chunk.SessionID != page.SessionID || chunk.FullSnapshotID != page.FullSnapshotID || chunk.StateVersion != page.StateVersion || chunk.AsOf != page.AsOf || chunk.BlobID != id || chunk.Offset != len(data) || chunk.TotalBytes != size || chunk.SHA256 != digest {
			return "", queryProtocolError("full blob identity changed")
		}
		part, err := base64.StdEncoding.DecodeString(chunk.DataBase64)
		if err != nil || len(part) == 0 || len(part) > 65536 || len(data)+len(part) > size {
			return "", queryProtocolError("invalid full blob chunk")
		}
		data = append(data, part...)
		if chunk.Complete != (len(data) == size) {
			return "", queryProtocolError("full blob completion mismatch")
		}
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != digest {
		return "", queryProtocolError("full blob digest mismatch")
	}
	return string(data), nil
}
func intNumber(value any) (int, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, errors.New("not a number")
	}
	n, err := number.Int64()
	return int(n), err
}

// IterFullSnapshot calls consume once per validated node and returns only after
// the final page digest verifies. A callback may have seen incomplete data if
// an error is returned; it must not publish that data as complete.
func (c *TaklerServiceClient) IterFullSnapshot(selection FullSnapshotSelection, consume func(map[string]any) error) (*FullSnapshotComplete, error) {
	c.queryMu.Lock()
	defer c.queryMu.Unlock()
	var complete *FullSnapshotComplete
	err := c.withQueryTransport(func(transport queryDocumentTransport) error {
		var err error
		complete, err = c.iterFullSnapshot(transport, selection, consume)
		return err
	})
	return complete, err
}
func (c *TaklerServiceClient) iterFullSnapshot(transport queryDocumentTransport, selection FullSnapshotSelection, consume func(map[string]any) error) (*FullSnapshotComplete, error) {
	scope := selection.ScopePath
	if scope == "" {
		scope = "/"
	}
	groups := selection.FieldGroups
	if len(groups) == 0 {
		groups = []string{"summary", "definition", "parameters", "runtime", "artifacts", "service"}
	}
	req := FullSnapshotRequest{Kind: "full_snapshot_request", SchemaVersion: 1, ScopePath: scope, Depth: selection.Depth, FieldGroups: groups}
	capsValue, err := c.queryDocument(transport, "capabilities_request", QueryCapabilitiesRequest{Kind: "capabilities_request", SchemaVersion: 1}, "capabilities")
	if err != nil {
		return nil, err
	}
	if !capsValue.(*QueryCapabilities).FullSnapshotV1 {
		return nil, &QueryFailureError{Code: "unsupported_capability", Message: "full snapshot unavailable"}
	}
	var first *FullSnapshotPage
	var digest hash.Hash = sha256.New()
	var total int64
	var payload int64
	count := 0
	seen := map[string]bool{}
	deadline := time.Now().Add(15 * time.Minute)
	defer func() {
		if first != nil {
			_, _ = c.queryDocument(transport, "cancel_full_snapshot_request", CancelFullSnapshotRequest{Kind: "cancel_full_snapshot_request", SchemaVersion: 1, SessionID: first.SessionID, FullSnapshotID: first.FullSnapshotID}, "cancel_full_snapshot_reply")
		}
	}()
	for index := 0; index < 1000000; index++ {
		if time.Now().After(deadline) {
			return nil, queryProtocolError("full snapshot total deadline exceeded")
		}
		value, err := c.queryDocument(transport, "full_snapshot_request", req, "full_snapshot_page")
		if err != nil {
			return nil, err
		}
		page := value.(*FullSnapshotPage)
		if page.PageIndex != index {
			return nil, queryProtocolError("full snapshot page order changed")
		}
		if first == nil {
			first = page
			if page.ScopePath != scope || !equalGroups(page.FieldGroups, groups) || !equalDepth(page.Depth, selection.Depth) || page.AncestorContext == nil {
				return nil, queryProtocolError("full snapshot selection changed")
			}
			metadata, err := fullMetadataCanonical(page.AncestorContext, page.Service)
			if err != nil {
				return nil, queryProtocolError("invalid full snapshot metadata")
			}
			total += int64(len(metadata))
			payload += int64(len(metadata))
			if payload > FullSnapshotEncodedBudget {
				return nil, queryProtocolError("full snapshot encoded budget")
			}
			_, _ = digest.Write(metadata)
			_, _ = digest.Write([]byte("\n"))
		} else if page.SessionID != first.SessionID || page.FullSnapshotID != first.FullSnapshotID || page.StateVersion != first.StateVersion || page.AsOf != first.AsOf || page.ScopePath != first.ScopePath || page.SelectionHash != first.SelectionHash || !equalGroups(page.FieldGroups, first.FieldGroups) || !equalDepth(page.Depth, first.Depth) || page.AncestorContext != nil || page.Service != nil {
			return nil, queryProtocolError("full snapshot identity changed")
		}
		for _, raw := range page.Nodes {
			if time.Now().After(deadline) {
				return nil, queryProtocolError("full snapshot total deadline exceeded")
			}
			canonical, row, err := fullCanonical(raw)
			if err != nil {
				return nil, queryProtocolError("invalid full snapshot node")
			}
			record, err := fullExpand(row, page.FieldGroups)
			if err != nil {
				return nil, queryProtocolError("invalid compact full snapshot row")
			}
			if page.PageIndex == 0 {
				record["ancestor_context"] = page.AncestorContext
				record["service"] = page.Service
			}
			path, ok := record["path"].(string)
			if !ok || !validQueryPath(path) || seen[path] || scope != "/" && path != scope && !strings.HasPrefix(path, scope+"/") {
				return nil, queryProtocolError("invalid full snapshot path")
			}
			if count == 0 && path != scope {
				return nil, queryProtocolError("full snapshot scope root is missing")
			}
			parent, _ := record["parent_path"].(string)
			expectedParent := ""
			if path != "/" {
				expectedParent = path[:strings.LastIndex(path, "/")]
				if expectedParent == "" {
					expectedParent = "/"
				}
			}
			if parent != expectedParent || path != scope && !seen[parent] {
				return nil, queryProtocolError("full snapshot parent mismatch")
			}
			seen[path] = true
			total += int64(len(canonical))
			payload += int64(len(canonical))
			if payload > FullSnapshotEncodedBudget {
				return nil, queryProtocolError("full snapshot encoded budget")
			}
			_, _ = digest.Write(canonical)
			_, _ = digest.Write([]byte("\n"))
			if refs, ok := record["blob_refs"].([]any); ok {
				for _, item := range refs {
					ref, ok := item.(map[string]any)
					if !ok {
						return nil, queryProtocolError("invalid full blob ref")
					}
					size, err := intNumber(ref["total_bytes"])
					if err != nil || size <= 0 || size > 16*1024*1024 {
						return nil, queryProtocolError("invalid full blob size")
					}
					payload += int64(size)
					if payload > FullSnapshotEncodedBudget {
						return nil, queryProtocolError("full snapshot encoded budget")
					}
					pointer, ok := ref["pointer"].(string)
					if !ok {
						return nil, queryProtocolError("invalid full blob pointer")
					}
					parent, key, err := fullPointer(record, pointer)
					if err != nil {
						return nil, queryProtocolError(err.Error())
					}
					var marker map[string]any
					switch value := parent.(type) {
					case map[string]any:
						marker, _ = value[key].(map[string]any)
					case []any:
						index, err := strconv.Atoi(key)
						if err != nil || index < 0 || index >= len(value) {
							return nil, queryProtocolError("invalid full blob pointer")
						}
						marker, _ = value[index].(map[string]any)
					default:
						return nil, queryProtocolError("invalid full blob pointer")
					}
					ok = marker != nil
					if !ok || marker["blob_id"] != ref["blob_id"] {
						return nil, queryProtocolError("full blob marker mismatch")
					}
					content, err := c.fullBlob(transport, page, ref, deadline)
					if err != nil {
						return nil, err
					}
					switch value := parent.(type) {
					case map[string]any:
						value[key] = content
					case []any:
						index, _ := strconv.Atoi(key)
						value[index] = content
					}
				}
			}
			delete(record, "ancestor_context")
			delete(record, "service")
			count++
			if err := consume(record); err != nil {
				return nil, err
			}
		}
		if page.Complete {
			if time.Now().After(deadline) {
				return nil, queryProtocolError("full snapshot total deadline exceeded")
			}
			if page.TotalNodes == nil || *page.TotalNodes != count || page.TotalBytes == nil || *page.TotalBytes != total || page.SHA256 == nil || *page.SHA256 != hex.EncodeToString(digest.Sum(nil)) {
				return nil, queryProtocolError("full snapshot completion mismatch")
			}
			return &FullSnapshotComplete{SessionID: page.SessionID, FullSnapshotID: page.FullSnapshotID, StateVersion: page.StateVersion, AsOf: page.AsOf, ScopePath: page.ScopePath, TotalNodes: count, TotalBytes: total, SHA256: *page.SHA256, AncestorContext: first.AncestorContext, Service: first.Service}, nil
		}
		if page.NextCursor == nil || len(page.Nodes) == 0 {
			return nil, queryProtocolError("full snapshot has no progress")
		}
		req.FullSnapshotID = &page.FullSnapshotID
		req.Cursor = page.NextCursor
	}
	return nil, queryProtocolError("full snapshot page limit")
}
func equalGroups(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func equalDepth(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (c *TaklerServiceClient) ReadFullSnapshot(selection FullSnapshotSelection) (*FullSnapshot, error) {
	nodes := map[string]map[string]any{}
	used := 0
	complete, err := c.IterFullSnapshot(selection, func(node map[string]any) error {
		raw, err := json.Marshal(node)
		if err != nil {
			return err
		}
		used += len(raw) * 3
		if used > FullSnapshotAssemblyBudget {
			return &QueryFailureError{Code: "resource_exhausted", Message: "assembly budget; use IterFullSnapshot"}
		}
		nodes[node["path"].(string)] = node
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &FullSnapshot{Complete: *complete, NodesByPath: nodes}, nil
}
