package common

// QueryDocument v1 is a strict JSON document on both HTTP and gRPC. This
// module is the Go contract decoder; the query client is added in R2-10A.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
)

const QueryMaxPageNodes = 2048
const QueryMaxPageBytes = 262144
const QueryMaxChunkBytes = 65536

type QueryCapabilitiesRequest struct {
	Kind          string `json:"kind"`
	SchemaVersion int    `json:"schema_version"`
}
type QueryCapabilities struct {
	Kind                string `json:"kind"`
	SchemaVersion       int    `json:"schema_version"`
	Snapshot            bool   `json:"snapshot"`
	Detail              bool   `json:"detail"`
	Since               bool   `json:"since"`
	MaxPageNodes        int    `json:"max_page_nodes"`
	MaxDecodedPageBytes int    `json:"max_decoded_page_bytes"`
	MaxChunkBytes       int    `json:"max_chunk_bytes"`
}
type QueryPageRequest struct {
	Kind          string   `json:"kind"`
	SchemaVersion int      `json:"schema_version"`
	ScopePath     *string  `json:"scope_path"`
	FlowName      *string  `json:"flow_name"`
	Depth         *int     `json:"depth"`
	FieldGroups   []string `json:"field_groups"`
	Cursor        *string  `json:"cursor"`
}
type QueryNode struct {
	Path      string `json:"path"`
	NodeKind  string `json:"node_kind"`
	TypeID    string `json:"type_id"`
	Status    string `json:"status"`
	Suspended bool   `json:"suspended"`
}
type QueryPage struct {
	Kind            string            `json:"kind"`
	SchemaVersion   int               `json:"schema_version"`
	SessionID       string            `json:"session_id"`
	QueryID         string            `json:"query_id"`
	SnapshotID      string            `json:"snapshot_id"`
	AsOf            string            `json:"as_of"`
	ExpiresAt       string            `json:"expires_at"`
	PageIndex       int               `json:"page_index"`
	TotalNodes      *int              `json:"total_nodes"`
	Complete        bool              `json:"complete"`
	NextCursor      *string           `json:"next_cursor"`
	RootName        string            `json:"root_name"`
	FlowGenerations map[string]string `json:"flow_generations"`
	Nodes           []QueryNode       `json:"nodes"`
}
type QueryDetailRequest struct {
	Kind           string   `json:"kind"`
	SchemaVersion  int      `json:"schema_version"`
	SessionID      string   `json:"session_id"`
	QueryID        string   `json:"query_id"`
	Path           string   `json:"path"`
	FlowGeneration *string  `json:"flow_generation"`
	AttemptID      *string  `json:"attempt_id"`
	FieldGroups    []string `json:"field_groups"`
}
type QueryDetail struct {
	Kind            string                    `json:"kind"`
	SchemaVersion   int                       `json:"schema_version"`
	SessionID       string                    `json:"session_id"`
	QueryID         string                    `json:"query_id"`
	Path            string                    `json:"path"`
	FlowGeneration  *string                   `json:"flow_generation"`
	AttemptID       *string                   `json:"attempt_id"`
	SampleMode      string                    `json:"sample_mode"`
	AsOf            string                    `json:"as_of"`
	RequestedGroups []string                  `json:"requested_groups"`
	Groups          map[string]map[string]any `json:"groups"`
}
type QueryChunkRequest struct {
	Kind          string `json:"kind"`
	SchemaVersion int    `json:"schema_version"`
	BlobID        string `json:"blob_id"`
	Offset        int    `json:"offset"`
}
type QueryChunk struct {
	Kind          string `json:"kind"`
	SchemaVersion int    `json:"schema_version"`
	BlobID        string `json:"blob_id"`
	Offset        int    `json:"offset"`
	TotalBytes    int    `json:"total_bytes"`
	SHA256        string `json:"sha256"`
	DataBase64    string `json:"data_base64"`
	Complete      bool   `json:"complete"`
}
type QueryFailure struct {
	Kind          string  `json:"kind"`
	SchemaVersion int     `json:"schema_version"`
	Code          string  `json:"code"`
	Message       string  `json:"message"`
	ResetScope    *string `json:"reset_scope"`
}

var queryKinds = map[string]bool{"bunch": true, "flow": true, "container": true, "task": true}
var queryStatuses = map[string]bool{"unknown": true, "queued": true, "submitted": true, "active": true, "complete": true, "aborted": true}
var queryGroups = map[string]bool{"summary": true, "definition": true, "parameters": true, "runtime": true, "artifacts": true, "service": true}
var queryErrorCodes = map[string]bool{"invalid_request": true, "unsupported_version": true, "unsupported_capability": true, "permission_denied": true, "stale_identity": true, "snapshot_expired": true, "resource_exhausted": true, "field_too_large": true, "internal_error": true}
var queryDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var queryUTC = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z$`)

func validQueryUTC(value string) bool {
	if !queryUTC.MatchString(value) {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

var queryGroupFields = map[string]map[string]bool{
	"definition": {"trigger": true, "complete_trigger": true, "script_path": true, "times": true, "repeat": true, "events": true, "meters": true, "limits": true, "in_limits": true},
	"parameters": {"user": true, "ancestors": true},
	"runtime":    {"begun": true, "calendar_flow_time": true, "attempt_id": true, "try_no": true, "task_id": true, "submitted_at": true, "started_at": true, "ended_at": true, "aborted_reason": true, "events": true, "meters": true, "repeat": true, "limits": true},
	"artifacts":  {"job_ref": true, "jobout_ref": true, "orvix_info_path": true},
	"service":    {"status": true, "status_reason": true, "halt_causes": true, "message": true, "last_checkpoint_at": true, "restore_summary": true, "generated_defaults": true},
}

func validQueryParameter(value any) bool {
	item, ok := value.(map[string]any)
	if !ok {
		return false
	}
	name, ok := item["name"].(string)
	if !ok || name == "" {
		return false
	}
	_, hasValue := item["value"]
	redacted, hasRedacted := item["redacted"]
	if hasValue == hasRedacted {
		return false
	}
	for key := range item {
		if key != "name" && key != "value" && key != "redacted" {
			return false
		}
	}
	if hasRedacted {
		return redacted == true
	}
	switch item["value"].(type) {
	case string, json.Number, bool, nil:
	default:
		return false
	}
	return true
}

func queryOptionalString(value any) bool {
	if value == nil {
		return true
	}
	_, ok := value.(string)
	return ok
}

func queryObjectList(value any) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if _, ok := item.(map[string]any); !ok {
			return false
		}
	}
	return true
}

func queryParameterList(value any) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if !validQueryParameter(item) {
			return false
		}
	}
	return true
}

func validQueryGroup(name string, fields map[string]any) bool {
	allowed := queryGroupFields[name]
	if allowed == nil || fields == nil {
		return false
	}
	for field := range fields {
		if !allowed[field] {
			return false
		}
	}
	switch name {
	case "parameters":
		if !queryParameterList(fields["user"]) {
			return false
		}
		if raw, ok := fields["ancestors"]; ok {
			ancestors, ok := raw.([]any)
			if !ok {
				return false
			}
			for _, item := range ancestors {
				ancestor, ok := item.(map[string]any)
				if !ok || len(ancestor) != 2 {
					return false
				}
				path, ok := ancestor["source_path"].(string)
				if !ok || !validQueryPath(path) || !queryParameterList(ancestor["user"]) {
					return false
				}
			}
		}
	case "definition", "runtime":
		for field, value := range fields {
			switch field {
			case "times", "events", "meters", "limits", "in_limits":
				if !queryObjectList(value) {
					return false
				}
			case "repeat":
				if value != nil {
					if _, ok := value.(map[string]any); !ok {
						return false
					}
				}
			case "begun":
				if value != nil {
					if _, ok := value.(bool); !ok {
						return false
					}
				}
			case "try_no":
				if value != nil {
					number, ok := value.(json.Number)
					if !ok {
						return false
					}
					n, err := strconv.ParseInt(string(number), 10, 64)
					if err != nil || n < 0 {
						return false
					}
				}
			default:
				if !queryOptionalString(value) {
					return false
				}
			}
		}
	case "artifacts":
		for field, value := range fields {
			if field == "orvix_info_path" {
				if !queryOptionalString(value) {
					return false
				}
				continue
			}
			if value == nil {
				continue
			}
			ref, ok := value.(map[string]any)
			if !ok || len(ref) != 2 {
				return false
			}
			if _, ok := ref["path"].(string); !ok {
				return false
			}
			if ref["origin"] != "current" && ref["origin"] != "anticipated" {
				return false
			}
		}
	case "service":
		for _, required := range []string{"status", "halt_causes", "last_checkpoint_at", "restore_summary", "generated_defaults"} {
			if _, ok := fields[required]; !ok {
				return false
			}
		}
		if fields["status"] != "running" && fields["status"] != "halted" {
			return false
		}
		for _, optional := range []string{"status_reason", "message"} {
			if value, ok := fields[optional]; ok && !queryOptionalString(value) {
				return false
			}
		}
		causes, ok := fields["halt_causes"].([]any)
		if !ok {
			return false
		}
		for _, cause := range causes {
			if _, ok := cause.(string); !ok {
				return false
			}
		}
		if !queryOptionalString(fields["last_checkpoint_at"]) {
			return false
		}
		if value := fields["restore_summary"]; value != nil {
			if _, ok := value.(map[string]any); !ok {
				return false
			}
		}
		if !queryParameterList(fields["generated_defaults"]) {
			return false
		}
	}
	return true
}

func validQueryPath(path string) bool {
	if path == "/" {
		return true
	}
	if !strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return false
	}
	for _, segment := range strings.Split(path[1:], "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, ch := range segment {
			if ch < 32 {
				return false
			}
		}
	}
	return true
}
func validQueryGroups(groups []string, detail bool) bool {
	if len(groups) == 0 {
		return false
	}
	seen := map[string]bool{}
	for i, group := range groups {
		if !queryGroups[group] || seen[group] || (detail && group == "summary") {
			return false
		}
		if !detail && i == 0 && group != "summary" {
			return false
		}
		seen[group] = true
	}
	return true
}
func queryDecode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err == io.EOF {
		return nil
	}
	return errors.New("trailing data")
}
func DecodeQueryV1(raw []byte, expected string) (any, error) {
	if len(raw) > QueryMaxPageBytes {
		return nil, errors.New("query document too large")
	}
	value, err := strictWireJSON(raw)
	if err != nil {
		return nil, err
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("query document must be object")
	}
	kind, ok := obj["kind"].(string)
	if !ok || (expected != "" && kind != expected) {
		return nil, errors.New("query kind mismatch")
	}
	version, ok := obj["schema_version"].(json.Number)
	if !ok || string(version) != "1" {
		return nil, errors.New("unsupported query version")
	}
	required := map[string][]string{
		"capabilities_request": {"kind", "schema_version"},
		"capabilities":         {"kind", "schema_version", "snapshot", "detail", "since", "max_page_nodes", "max_decoded_page_bytes", "max_chunk_bytes"},
		"page_request":         {"kind", "schema_version", "field_groups"},
		"page":                 {"kind", "schema_version", "session_id", "query_id", "snapshot_id", "as_of", "expires_at", "page_index", "complete", "next_cursor", "root_name", "flow_generations", "nodes"},
		"detail_request":       {"kind", "schema_version", "session_id", "query_id", "path", "flow_generation", "attempt_id", "field_groups"},
		"detail":               {"kind", "schema_version", "session_id", "query_id", "path", "flow_generation", "attempt_id", "sample_mode", "as_of", "requested_groups", "groups"},
		"chunk_request":        {"kind", "schema_version", "blob_id", "offset"},
		"chunk":                {"kind", "schema_version", "blob_id", "offset", "total_bytes", "sha256", "data_base64", "complete"},
		"error":                {"kind", "schema_version", "code", "message"},
	}[kind]
	if required == nil {
		return nil, errors.New("unknown query kind")
	}
	for _, name := range required {
		if _, exists := obj[name]; !exists {
			return nil, errors.New("missing query field")
		}
	}
	var out any
	switch kind {
	case "capabilities_request":
		out = &QueryCapabilitiesRequest{}
	case "capabilities":
		out = &QueryCapabilities{}
	case "page_request":
		out = &QueryPageRequest{}
	case "page":
		out = &QueryPage{}
	case "detail_request":
		out = &QueryDetailRequest{}
	case "detail":
		out = &QueryDetail{}
	case "chunk_request":
		out = &QueryChunkRequest{}
	case "chunk":
		out = &QueryChunk{}
	case "error":
		out = &QueryFailure{}
	default:
		return nil, errors.New("unknown query kind")
	}
	if err = queryDecode(raw, out); err != nil {
		return nil, err
	}
	switch v := out.(type) {
	case *QueryCapabilities:
		if _, ok := obj["since"].(bool); !ok {
			return nil, errors.New("invalid capabilities since")
		}
		if !v.Snapshot || !v.Detail || v.Since || v.MaxPageNodes != QueryMaxPageNodes || v.MaxDecodedPageBytes != QueryMaxPageBytes || v.MaxChunkBytes != QueryMaxChunkBytes {
			return nil, errors.New("invalid capabilities")
		}
	case *QueryPageRequest:
		if v.ScopePath != nil && v.FlowName != nil {
			return nil, errors.New("conflicting scope")
		}
		if v.ScopePath != nil && !validQueryPath(*v.ScopePath) {
			return nil, errors.New("invalid scope")
		}
		if v.FlowName != nil && (*v.FlowName == "" || !validQueryPath("/"+*v.FlowName) || strings.Contains(*v.FlowName, "/")) {
			return nil, errors.New("invalid flow")
		}
		if v.Depth != nil && *v.Depth < 0 || !validQueryGroups(v.FieldGroups, false) || v.Cursor != nil && *v.Cursor == "" {
			return nil, errors.New("invalid page request")
		}
	case *QueryPage:
		if _, ok := obj["complete"].(bool); !ok {
			return nil, errors.New("invalid page complete")
		}
		nodesRaw, ok := obj["nodes"].([]any)
		if !ok {
			return nil, errors.New("invalid page nodes")
		}
		for _, rawNode := range nodesRaw {
			node, ok := rawNode.(map[string]any)
			if !ok {
				return nil, errors.New("invalid page node")
			}
			for _, field := range []string{"path", "node_kind", "type_id", "status", "suspended"} {
				if _, has := node[field]; !has {
					return nil, errors.New("missing node field")
				}
			}
			if _, ok := node["suspended"].(bool); !ok {
				return nil, errors.New("invalid node suspended")
			}
		}
		if v.SessionID == "" || v.QueryID == "" || v.SnapshotID == "" || !validQueryUTC(v.AsOf) || !validQueryUTC(v.ExpiresAt) || v.PageIndex < 0 || v.TotalNodes != nil && *v.TotalNodes < 0 || len(v.Nodes) > QueryMaxPageNodes || v.Complete != (v.NextCursor == nil) || !v.Complete && (v.NextCursor == nil || *v.NextCursor == "" || len(v.Nodes) == 0) {
			return nil, errors.New("invalid page")
		}
		seen := map[string]bool{}
		for path, generation := range v.FlowGenerations {
			if !validQueryPath(path) || strings.Count(path, "/") != 1 || generation == "" {
				return nil, errors.New("invalid generation")
			}
		}
		for _, node := range v.Nodes {
			if !validQueryPath(node.Path) || !queryKinds[node.NodeKind] || !queryStatuses[node.Status] || node.TypeID == "" || seen[node.Path] {
				return nil, errors.New("invalid node")
			}
			seen[node.Path] = true
			if node.Path != "/" && v.FlowGenerations["/"+strings.Split(node.Path[1:], "/")[0]] == "" {
				return nil, errors.New("missing flow generation")
			}
		}
	case *QueryDetailRequest:
		if v.SessionID == "" || v.QueryID == "" || !validQueryPath(v.Path) || v.Path == "/" && v.FlowGeneration != nil || v.Path != "/" && (v.FlowGeneration == nil || *v.FlowGeneration == "") || !validQueryGroups(v.FieldGroups, true) {
			return nil, errors.New("invalid detail request")
		}
	case *QueryDetail:
		if !validQueryPath(v.Path) || v.Path == "/" && v.FlowGeneration != nil || v.Path != "/" && (v.FlowGeneration == nil || *v.FlowGeneration == "") || v.SessionID == "" || v.QueryID == "" || !validQueryUTC(v.AsOf) || v.SampleMode != "live" || !validQueryGroups(v.RequestedGroups, true) || len(v.Groups) != len(v.RequestedGroups) {
			return nil, errors.New("invalid detail")
		}
		for _, group := range v.RequestedGroups {
			if value, ok := v.Groups[group]; !ok || !validQueryGroup(group, value) {
				return nil, errors.New("missing detail group")
			}
		}
	case *QueryChunkRequest:
		if v.BlobID == "" || v.Offset < 0 {
			return nil, errors.New("invalid chunk request")
		}
	case *QueryChunk:
		if _, ok := obj["complete"].(bool); !ok {
			return nil, errors.New("invalid chunk complete")
		}
		data, err := base64.StdEncoding.Strict().DecodeString(v.DataBase64)
		if err != nil || v.BlobID == "" || v.Offset < 0 || v.TotalBytes < 0 || v.TotalBytes > 16*1024*1024 || len(data) > QueryMaxChunkBytes || v.Offset+len(data) > v.TotalBytes || v.Complete != (v.Offset+len(data) == v.TotalBytes) || !queryDigestPattern.MatchString(v.SHA256) {
			return nil, errors.New("invalid chunk")
		}
	case *QueryFailure:
		if !queryErrorCodes[v.Code] || v.ResetScope != nil && !validQueryPath(*v.ResetScope) {
			return nil, errors.New("invalid query error")
		}
	}
	return out, nil
}
func QueryPayload(raw []byte, expected string) (*pb.QueryDocumentPayload, error) {
	if _, err := DecodeQueryV1(raw, expected); err != nil {
		return nil, fmt.Errorf("query payload: %w", err)
	}
	return &pb.QueryDocumentPayload{Json: raw}, nil
}
func DecodeQueryPayload(payload *pb.QueryDocumentPayload, expected string) (any, error) {
	if payload == nil {
		return nil, errors.New("missing query payload")
	}
	return DecodeQueryV1(payload.Json, expected)
}
