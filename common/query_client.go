package common

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const QueryClientViewBudget = 128 * 1024 * 1024
const QueryBlobFieldBudget = 16 * 1024 * 1024

// QuerySelection identifies an initial summary scope. Empty strings mean unset.
type QuerySelection struct {
	ScopePath string
	FlowName  string
	Depth     *int
}

// QuerySnapshot is published only after every page passes validation.
// Its node maps are private so callers cannot change the committed view.
type QuerySnapshot struct {
	SessionID       string
	QueryID         string
	SnapshotID      string
	AsOf            string
	ExpiresAt       string
	RootName        string
	ScopePath       string
	Depth           *int
	EstimatedBytes  int
	nodes           map[string]QueryNode
	order           []string
	flowGenerations map[string]string
}

func (s *QuerySnapshot) Len() int {
	if s == nil {
		return 0
	}
	return len(s.order)
}

func (s *QuerySnapshot) Node(path string) (QueryNode, bool) {
	if s == nil {
		return QueryNode{}, false
	}
	node, ok := s.nodes[path]
	if node.AttemptID != nil {
		attempt := *node.AttemptID
		node.AttemptID = &attempt
	}
	return node, ok
}

func (s *QuerySnapshot) Paths() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.order...)
}

// QueryFailureError preserves the typed QueryDocument failure and its CLI exit class.
type QueryFailureError struct {
	Code       string
	Message    string
	ResetScope *string
}

func (e *QueryFailureError) Error() string { return e.Code + ": " + e.Message }
func (e *QueryFailureError) Unwrap() error {
	code := ExitServerError
	switch e.Code {
	case "invalid_request", "unsupported_version", "unsupported_capability", "permission_denied", "stale_identity", "field_too_large":
		code = ExitRequestError
	}
	return NewExitError(code, e.Error())
}

type queryDocumentTransport interface {
	QueryDocument(context.Context, string, []byte) ([]byte, error)
}

func queryProtocolError(message string) error { return NewExitError(ExitServerError, message) }
func queryRequestError(message string) error  { return NewExitError(ExitRequestError, message) }

func (c *TaklerServiceClient) queryDocument(transport queryDocumentTransport, kind string, request any, expected string) (any, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, queryRequestError("cannot encode query request")
	}
	if _, err = DecodeQueryV1(raw, kind); err != nil {
		return nil, queryRequestError("invalid query request: " + err.Error())
	}
	answer, err := Call(c, context.Background(), "query "+kind, KindQuery, raw,
		func(ctx context.Context, payload []byte) ([]byte, error) {
			return transport.QueryDocument(ctx, kind, payload)
		})
	if err != nil {
		return nil, err
	}
	decoded, err := DecodeQueryV1(answer, "")
	if err != nil {
		return nil, queryProtocolError("invalid query response: " + err.Error())
	}
	if failure, ok := decoded.(*QueryFailure); ok {
		return nil, &QueryFailureError{Code: failure.Code, Message: failure.Message, ResetScope: failure.ResetScope}
	}
	if got := queryKind(decoded); got != expected {
		return nil, queryProtocolError("unexpected query response kind")
	}
	return decoded, nil
}

func queryKind(value any) string {
	switch value.(type) {
	case *QueryCapabilities:
		return "capabilities"
	case *QueryPage:
		return "page"
	case *QueryDetail:
		return "detail"
	case *QueryChunk:
		return "chunk"
	}
	return ""
}

func (c *TaklerServiceClient) withQueryTransport(fn func(queryDocumentTransport) error) error {
	return c.withTransport(func(transport Transport) error {
		query, ok := transport.(queryDocumentTransport)
		if !ok {
			return queryRequestError("selected transport has no QueryDocument v1 support")
		}
		return fn(query)
	})
}

func (c *TaklerServiceClient) QueryCapabilities() (*QueryCapabilities, error) {
	var result *QueryCapabilities
	err := c.withQueryTransport(func(transport queryDocumentTransport) error {
		value, err := c.queryDocument(transport, "capabilities_request", QueryCapabilitiesRequest{Kind: "capabilities_request", SchemaVersion: 1}, "capabilities")
		if err == nil {
			result = value.(*QueryCapabilities)
		}
		return err
	})
	return result, err
}

// CurrentQuerySnapshot returns the last complete view; failures do not replace it.
func (c *TaklerServiceClient) CurrentQuerySnapshot() *QuerySnapshot {
	c.queryMu.Lock()
	defer c.queryMu.Unlock()
	return c.queryCurrent
}

func (c *TaklerServiceClient) ReadQuerySnapshot(selection QuerySelection) (*QuerySnapshot, error) {
	c.queryMu.Lock()
	defer c.queryMu.Unlock()
	var result *QuerySnapshot
	err := c.withQueryTransport(func(transport queryDocumentTransport) error {
		var err error
		result, err = c.readQuerySnapshot(transport, selection)
		return err
	})
	return result, err
}

func (c *TaklerServiceClient) readQuerySnapshot(transport queryDocumentTransport, selection QuerySelection) (*QuerySnapshot, error) {
	request := QueryPageRequest{Kind: "page_request", SchemaVersion: 1, FieldGroups: []string{"summary"}}
	scope := "/"
	if selection.ScopePath != "" {
		request.ScopePath = &selection.ScopePath
		scope = selection.ScopePath
	}
	if selection.FlowName != "" {
		request.FlowName = &selection.FlowName
		scope = "/" + selection.FlowName
	}
	request.Depth = selection.Depth
	if request.ScopePath != nil && request.FlowName != nil {
		return nil, queryRequestError("scope and flow cannot both be set")
	}
	if !validQueryPath(scope) || selection.Depth != nil && *selection.Depth < 0 {
		return nil, queryRequestError("invalid query selection")
	}
	oldSize := 0
	if c.queryCurrent != nil {
		oldSize = c.queryCurrent.EstimatedBytes
	}
	snapshot := &QuerySnapshot{ScopePath: scope, Depth: selection.Depth, EstimatedBytes: 1024,
		nodes: map[string]QueryNode{}, order: []string{}, flowGenerations: map[string]string{}}
	var total *int
	for index := 0; ; index++ {
		value, err := c.queryDocument(transport, "page_request", request, "page")
		if err != nil {
			return nil, err
		}
		page := value.(*QueryPage)
		if page.PageIndex != index {
			return nil, queryProtocolError("query page order changed")
		}
		if index == 0 {
			snapshot.SessionID, snapshot.QueryID, snapshot.SnapshotID = page.SessionID, page.QueryID, page.SnapshotID
			snapshot.AsOf, snapshot.ExpiresAt, snapshot.RootName = page.AsOf, page.ExpiresAt, page.RootName
		} else if snapshot.SessionID != page.SessionID || snapshot.QueryID != page.QueryID || snapshot.SnapshotID != page.SnapshotID || snapshot.AsOf != page.AsOf || snapshot.ExpiresAt != page.ExpiresAt || snapshot.RootName != page.RootName {
			return nil, queryProtocolError("query snapshot identity changed")
		}
		if page.TotalNodes != nil {
			if total != nil && *total != *page.TotalNodes {
				return nil, queryProtocolError("query node count changed")
			}
			total = page.TotalNodes
		}
		for path, generation := range page.FlowGenerations {
			if earlier, exists := snapshot.flowGenerations[path]; exists && earlier != generation {
				return nil, queryProtocolError("query flow identity changed")
			}
			snapshot.flowGenerations[path] = generation
		}
		for _, node := range page.Nodes {
			if _, exists := snapshot.nodes[node.Path]; exists {
				return nil, queryProtocolError("duplicate query node")
			}
			if node.Path != scope && !strings.HasPrefix(node.Path, strings.TrimSuffix(scope, "/")+"/") {
				return nil, queryProtocolError("query node outside requested scope")
			}
			depth := strings.Count(node.Path, "/") - strings.Count(scope, "/")
			if scope == "/" {
				depth++
			}
			if node.Path == "/" {
				depth = 0
			}
			if selection.Depth != nil && depth > *selection.Depth {
				return nil, queryProtocolError("query node outside requested depth")
			}
			if node.Path != scope {
				parent := node.Path[:strings.LastIndex(node.Path, "/")]
				if parent == "" {
					parent = "/"
				}
				if _, exists := snapshot.nodes[parent]; !exists {
					return nil, queryProtocolError("query parent is missing")
				}
			}
			snapshot.nodes[node.Path] = node
			snapshot.order = append(snapshot.order, node.Path)
			snapshot.EstimatedBytes += 512 + len(node.Path) + len(node.NodeKind) + len(node.TypeID) + len(node.Status)
			if node.AttemptID != nil {
				snapshot.EstimatedBytes += len(*node.AttemptID)
			}
			if oldSize+snapshot.EstimatedBytes > QueryClientViewBudget {
				return nil, &QueryFailureError{Code: "resource_exhausted", Message: "query view memory budget exceeded"}
			}
		}
		if page.Complete {
			break
		}
		request.Cursor = page.NextCursor
	}
	if len(snapshot.order) == 0 || snapshot.order[0] != scope {
		return nil, queryProtocolError("query scope root is missing")
	}
	if total != nil && len(snapshot.order) != *total {
		return nil, queryProtocolError("query node count mismatch")
	}
	c.queryCurrent = snapshot
	return snapshot, nil
}

func sameQueryString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (c *TaklerServiceClient) ReadQueryDetail(snapshot *QuerySnapshot, path string, groups []string) (*QueryDetail, error) {
	var result *QueryDetail
	err := c.withQueryTransport(func(transport queryDocumentTransport) error {
		var err error
		result, err = c.readQueryDetail(transport, snapshot, path, groups)
		return err
	})
	return result, err
}

func (c *TaklerServiceClient) readQueryDetail(transport queryDocumentTransport, snapshot *QuerySnapshot, path string, groups []string) (*QueryDetail, error) {
	if snapshot == nil || !validQueryPath(path) {
		return nil, queryRequestError("invalid detail path")
	}
	node, exists := snapshot.nodes[path]
	if !exists {
		return nil, queryRequestError("path is not in the query view")
	}
	if !validQueryGroups(groups, true) {
		return nil, queryRequestError("invalid detail groups")
	}
	var generation *string
	if path != "/" {
		flowPath := "/" + strings.Split(path[1:], "/")[0]
		value, exists := snapshot.flowGenerations[flowPath]
		if !exists {
			return nil, queryProtocolError("missing query flow generation")
		}
		generation = &value
	}
	request := QueryDetailRequest{Kind: "detail_request", SchemaVersion: 1, SessionID: snapshot.SessionID,
		QueryID: snapshot.QueryID, Path: path, FlowGeneration: generation, AttemptID: node.AttemptID, FieldGroups: groups}
	value, err := c.queryDocument(transport, "detail_request", request, "detail")
	if err != nil {
		return nil, err
	}
	detail := value.(*QueryDetail)
	if detail.SessionID != snapshot.SessionID || detail.QueryID != snapshot.QueryID || detail.Path != path || !sameQueryString(detail.FlowGeneration, generation) || !sameQueryString(detail.AttemptID, node.AttemptID) || len(detail.RequestedGroups) != len(groups) {
		return nil, queryProtocolError("query detail identity changed")
	}
	for index, group := range groups {
		if detail.RequestedGroups[index] != group {
			return nil, queryProtocolError("query detail groups changed")
		}
	}
	seen := map[string]bool{}
	for _, ref := range detail.BlobRefs {
		if seen[ref.Pointer] {
			return nil, queryProtocolError("duplicate query blob pointer")
		}
		seen[ref.Pointer] = true
		parent, key, err := queryPointerParent(detail.Groups, ref.Pointer)
		if err != nil {
			return nil, err
		}
		resolved, err := c.readQueryBlob(transport, ref)
		if err != nil {
			return nil, err
		}
		switch object := parent.(type) {
		case map[string]any:
			object[key] = resolved
		case []any:
			offset, _ := strconv.Atoi(key)
			object[offset] = resolved
		}
	}
	if len(detail.BlobRefs) > 0 {
		detail.BlobRefs = nil
		for _, group := range groups {
			if !validQueryGroup(group, detail.Groups[group]) {
				return nil, queryProtocolError("invalid resolved query detail")
			}
		}
	}
	return detail, nil
}

func queryPointerParent(groups map[string]map[string]any, pointer string) (any, string, error) {
	parts := strings.Split(pointer, "/")
	if len(parts) < 4 || parts[0] != "" || parts[1] != "groups" {
		return nil, "", queryProtocolError("invalid query blob pointer")
	}
	var current any = groups
	for _, part := range parts[2 : len(parts)-1] {
		key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch object := current.(type) {
		case map[string]map[string]any:
			value, ok := object[key]
			if !ok {
				return nil, "", queryProtocolError("invalid query blob pointer")
			}
			current = value
		case map[string]any:
			value, ok := object[key]
			if !ok {
				return nil, "", queryProtocolError("invalid query blob pointer")
			}
			current = value
		case []any:
			index, err := queryPointerIndex(key, len(object))
			if err != nil {
				return nil, "", err
			}
			current = object[index]
		default:
			return nil, "", queryProtocolError("invalid query blob pointer")
		}
	}
	key := strings.ReplaceAll(strings.ReplaceAll(parts[len(parts)-1], "~1", "/"), "~0", "~")
	switch object := current.(type) {
	case map[string]any:
		if _, ok := object[key]; !ok {
			return nil, "", queryProtocolError("invalid query blob pointer")
		}
	case []any:
		if _, err := queryPointerIndex(key, len(object)); err != nil {
			return nil, "", err
		}
	default:
		return nil, "", queryProtocolError("invalid query blob pointer")
	}
	return current, key, nil
}

func queryPointerIndex(key string, size int) (int, error) {
	if key == "" {
		return 0, queryProtocolError("invalid query blob pointer")
	}
	for _, ch := range key {
		if ch < '0' || ch > '9' {
			return 0, queryProtocolError("invalid query blob pointer")
		}
	}
	index, err := strconv.Atoi(key)
	if err != nil || index >= size {
		return 0, queryProtocolError("invalid query blob pointer")
	}
	return index, nil
}

func (c *TaklerServiceClient) readQueryBlob(transport queryDocumentTransport, ref QueryBlobRef) (any, error) {
	if ref.TotalBytes > QueryBlobFieldBudget {
		return nil, queryProtocolError("query blob exceeds field budget")
	}
	data := make([]byte, 0, ref.TotalBytes)
	for len(data) < ref.TotalBytes {
		request := QueryChunkRequest{Kind: "chunk_request", SchemaVersion: 1, BlobID: ref.BlobID, Offset: len(data)}
		value, err := c.queryDocument(transport, "chunk_request", request, "chunk")
		if err != nil {
			return nil, err
		}
		chunk := value.(*QueryChunk)
		if chunk.BlobID != ref.BlobID || chunk.Offset != len(data) || chunk.TotalBytes != ref.TotalBytes || chunk.SHA256 != ref.SHA256 {
			return nil, queryProtocolError("query chunk identity changed")
		}
		raw, err := base64.StdEncoding.Strict().DecodeString(chunk.DataBase64)
		if err != nil || len(raw) == 0 || len(data)+len(raw) > ref.TotalBytes {
			return nil, queryProtocolError("invalid query chunk")
		}
		data = append(data, raw...)
		if chunk.Complete != (len(data) == ref.TotalBytes) {
			return nil, queryProtocolError("query chunk completion mismatch")
		}
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != ref.SHA256 {
		return nil, queryProtocolError("query blob digest mismatch")
	}
	value, err := strictWireJSON(data)
	if err != nil {
		return nil, queryProtocolError("invalid query blob JSON")
	}
	return value, nil
}

func (s *QuerySnapshot) String() string {
	return fmt.Sprintf("query %s (%d nodes)", s.QueryID, len(s.order))
}
