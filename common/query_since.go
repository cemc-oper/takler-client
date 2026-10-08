package common

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"
)

const queryDeltaBatchBudget = 8 * 1024 * 1024

// ReadQuerySince applies one complete summary batch to the current view.
func (c *TaklerServiceClient) ReadQuerySince(snapshot *QuerySnapshot) (*QuerySnapshot, error) {
	c.queryMu.Lock()
	defer c.queryMu.Unlock()
	var result *QuerySnapshot
	err := c.withQueryTransport(func(transport queryDocumentTransport) error {
		var err error
		result, err = c.readQuerySince(transport, snapshot)
		return err
	})
	return result, err
}

func (c *TaklerServiceClient) markQueryStale(snapshot *QuerySnapshot, scope string) {
	if c.queryCurrent != snapshot {
		return
	}
	copy := *snapshot
	copy.staleScopes = append(append([]string(nil), snapshot.staleScopes...), scope)
	sort.Strings(copy.staleScopes)
	copy.staleScopes = compactStrings(copy.staleScopes)
	c.queryCurrent = &copy
}

func compactStrings(items []string) []string {
	if len(items) == 0 {
		return items
	}
	result := items[:1]
	for _, item := range items[1:] {
		if item != result[len(result)-1] {
			result = append(result, item)
		}
	}
	return result
}

func queryPathInView(snapshot *QuerySnapshot, path string) bool {
	if path != snapshot.ScopePath && !strings.HasPrefix(path, strings.TrimSuffix(snapshot.ScopePath, "/")+"/") {
		return false
	}
	depth := strings.Count(path, "/") - strings.Count(snapshot.ScopePath, "/")
	if snapshot.ScopePath == "/" && path != "/" {
		depth++
	}
	return snapshot.Depth == nil || depth <= *snapshot.Depth
}

func queryNodeSize(node QueryNode) int {
	size := 512 + len(node.Path) + len(node.NodeKind) + len(node.TypeID) + len(node.Status)
	if node.AttemptID != nil {
		size += len(*node.AttemptID)
	}
	return size
}

func (c *TaklerServiceClient) readQuerySince(transport queryDocumentTransport, snapshot *QuerySnapshot) (result *QuerySnapshot, err error) {
	if snapshot == nil || c.queryCurrent != snapshot {
		return nil, queryRequestError("query view is not current")
	}
	if snapshot.Revision < 1 {
		return nil, &QueryFailureError{Code: "unsupported_capability", Message: "query has no revision"}
	}
	if snapshot.IsStale(snapshot.ScopePath) {
		return nil, &QueryFailureError{Code: "stale_identity", Message: "query root needs rebuilding"}
	}
	defer func() {
		if err != nil && c.queryCurrent == snapshot {
			c.markQueryStale(snapshot, snapshot.ScopePath)
		}
	}()
	value, err := c.queryDocument(transport, "capabilities_request", QueryCapabilitiesRequest{Kind: "capabilities_request", SchemaVersion: 1}, "capabilities")
	if err != nil {
		return nil, err
	}
	if !value.(*QueryCapabilities).Since {
		return nil, &QueryFailureError{Code: "unsupported_capability", Message: "server has no since"}
	}
	scope := snapshot.ScopePath
	request := QuerySinceRequest{Kind: "since_request", SchemaVersion: 1, ScopePath: &scope,
		Depth: snapshot.Depth, FieldGroups: []string{"summary"}, SessionID: snapshot.SessionID,
		QueryID: snapshot.QueryID, SinceRevision: snapshot.Revision}
	changes := map[string]QueryNode{}
	removed := map[string]bool{}
	stale := append([]string(nil), snapshot.staleScopes...)
	seen := map[string]bool{}
	var first *QuerySince
	var previous *QuerySince
	replays := 0
	batchSize := 0
	for index := 0; ; {
		value, err := c.queryDocument(transport, "since_request", request, "since")
		if err != nil {
			return nil, err
		}
		if reset, ok := value.(*QueryReset); ok {
			if reset.SessionID != snapshot.SessionID || reset.QueryID != snapshot.QueryID || reset.SinceRevision != snapshot.Revision {
				return nil, queryProtocolError("query reset identity changed")
			}
			c.markQueryStale(snapshot, reset.ResetScope)
			return nil, &QueryFailureError{Code: "reset_required", Message: reset.Reason, ResetScope: &reset.ResetScope}
		}
		page := value.(*QuerySince)
		if previous != nil && page.PageIndex == index-1 {
			replays++
			if !reflect.DeepEqual(page, previous) || replays > 2 {
				return nil, queryProtocolError("conflicting query delta replay")
			}
			continue
		}
		replays = 0
		if page.SessionID != snapshot.SessionID || page.QueryID != snapshot.QueryID || page.FromRevision != snapshot.Revision || page.PageIndex != index {
			return nil, queryProtocolError("query delta identity or page order changed")
		}
		if first == nil {
			first = page
		} else if page.BatchID != first.BatchID || page.TargetRevision != first.TargetRevision || page.AsOf != first.AsOf || page.ExpiresAt != first.ExpiresAt {
			return nil, queryProtocolError("query delta batch identity changed")
		}
		encodedPage, _ := json.Marshal(page)
		batchSize += len(encodedPage) + 1024
		if batchSize > queryDeltaBatchBudget {
			return nil, &QueryFailureError{Code: "resource_exhausted", Message: "query delta batch budget exceeded"}
		}
		for _, op := range page.Operations {
			identity := op.Op + "\x00" + op.Path
			if seen[identity] {
				return nil, queryProtocolError("duplicate query delta operation")
			}
			seen[identity] = true
			switch op.Op {
			case "upsert_summary":
				if _, exists := snapshot.Node(op.Path); !exists || !queryPathInView(snapshot, op.Path) {
					return nil, queryProtocolError("query delta added an unknown node")
				}
				if op.Path != "/" {
					flow := "/" + strings.Split(op.Path[1:], "/")[0]
					if op.FlowGeneration == nil || *op.FlowGeneration != snapshot.flowGenerations[flow] {
						return nil, queryProtocolError("query delta flow identity changed")
					}
				}
				changes[op.Path] = *op.Node
			case "invalidate_scope":
				if !queryPathInView(snapshot, op.Path) {
					return nil, queryProtocolError("query invalidation outside view")
				}
				stale = append(stale, op.Path)
			case "remove_subtree":
				if !queryPathInView(snapshot, op.Path) {
					return nil, queryProtocolError("query removal outside view")
				}
				flow := "/" + strings.Split(op.Path[1:], "/")[0]
				if op.FlowGeneration == nil || *op.FlowGeneration != snapshot.flowGenerations[flow] {
					return nil, queryProtocolError("query removal flow identity changed")
				}
				removed[op.Path] = true
			default:
				return nil, queryProtocolError("unexpected detail delta in summary view")
			}
		}
		if page.Complete {
			break
		}
		previous = page
		request.Cursor = page.NextCursor
		index++
	}
	if c.queryCurrent != snapshot {
		return nil, queryRequestError("superseded query")
	}
	if first.TargetRevision == snapshot.Revision {
		return snapshot, nil
	}
	copy := *snapshot
	copy.Revision = first.TargetRevision
	copy.AsOf = first.AsOf
	if len(removed) == 0 {
		copy.overrides = make(map[string]QueryNode, len(snapshot.overrides)+len(changes))
		for path, node := range snapshot.overrides {
			copy.overrides[path] = node
		}
		for path, node := range changes {
			copy.overrides[path] = node
		}
		extra := 0
		for _, node := range copy.overrides {
			extra += queryNodeSize(node)
		}
		copy.EstimatedBytes = snapshot.baseBytes + extra
		if copy.EstimatedBytes+extra > QueryClientViewBudget {
			return nil, &QueryFailureError{Code: "resource_exhausted", Message: "query view memory budget exceeded"}
		}
	} else {
		if snapshot.EstimatedBytes*2 > QueryClientViewBudget {
			return nil, &QueryFailureError{Code: "resource_exhausted", Message: "query view memory budget exceeded"}
		}
		copy.nodes = make(map[string]QueryNode, len(snapshot.nodes))
		copy.order = make([]string, 0, len(snapshot.order))
		copy.overrides = nil
		copy.flowGenerations = make(map[string]string, len(snapshot.flowGenerations))
		copy.EstimatedBytes = 1024
		for _, path := range snapshot.order {
			deletePath := false
			for root := range removed {
				if path == root || strings.HasPrefix(path, root+"/") {
					deletePath = true
					break
				}
			}
			if deletePath {
				if _, conflict := changes[path]; conflict {
					return nil, queryProtocolError("query delta updates removed subtree")
				}
				continue
			}
			node, _ := snapshot.Node(path)
			if changed, exists := changes[path]; exists {
				node = changed
			}
			copy.nodes[path] = node
			copy.order = append(copy.order, path)
			copy.EstimatedBytes += queryNodeSize(node)
		}
		if len(copy.order) == 0 || copy.order[0] != snapshot.ScopePath {
			return nil, queryProtocolError("query root was removed")
		}
		if snapshot.EstimatedBytes+copy.EstimatedBytes > QueryClientViewBudget {
			return nil, &QueryFailureError{Code: "resource_exhausted", Message: "query view memory budget exceeded"}
		}
		for path, generation := range snapshot.flowGenerations {
			if _, exists := copy.nodes[path]; exists {
				copy.flowGenerations[path] = generation
			}
		}
		copy.baseBytes = copy.EstimatedBytes
	}
	sort.Strings(stale)
	stale = compactStrings(stale)
	if len(removed) != 0 {
		filtered := stale[:0]
		for _, path := range stale {
			deleted := false
			for root := range removed {
				if path == root || strings.HasPrefix(path, root+"/") {
					deleted = true
					break
				}
			}
			if !deleted {
				filtered = append(filtered, path)
			}
		}
		stale = filtered
	}
	copy.staleScopes = stale
	c.queryCurrent = &copy
	return &copy, nil
}

// SyncQuery reads a snapshot and a fixed number of since batches. A reset or
// local structural invalidation rebuilds the selected view.
func (c *TaklerServiceClient) SyncQuery(selection QuerySelection, polls int, interval time.Duration) (*QuerySnapshot, error) {
	if polls < 1 || interval < 0 {
		return nil, queryRequestError("polls must be positive and interval nonnegative")
	}
	view, err := c.ReadQuerySnapshot(selection)
	if err != nil {
		return nil, err
	}
	for index := 0; index < polls; index++ {
		if index > 0 {
			time.Sleep(interval)
		}
		view, err = c.ReadQuerySince(view)
		if err != nil {
			var reset *QueryFailureError
			if !errors.As(err, &reset) || reset.Code != "reset_required" {
				return nil, err
			}
		}
		if err != nil || len(view.StaleScopes()) != 0 {
			view, err = c.ReadQuerySnapshot(selection)
			if err != nil {
				return nil, err
			}
		}
	}
	return view, nil
}
