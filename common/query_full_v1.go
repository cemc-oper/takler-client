package common

import "encoding/json"

// Full snapshots share QueryDocument v1's strict JSON envelope.
type FullSnapshotRequest struct {
	Kind           string   `json:"kind"`
	SchemaVersion  int      `json:"schema_version"`
	ScopePath      string   `json:"scope_path"`
	Depth          *int     `json:"depth"`
	FieldGroups    []string `json:"field_groups"`
	FullSnapshotID *string  `json:"full_snapshot_id,omitempty"`
	Cursor         *string  `json:"cursor,omitempty"`
}
type FullSnapshotPage struct {
	Kind            string            `json:"kind"`
	SchemaVersion   int               `json:"schema_version"`
	SessionID       string            `json:"session_id"`
	FullSnapshotID  string            `json:"full_snapshot_id"`
	StateVersion    int64             `json:"state_version"`
	AsOf            string            `json:"as_of"`
	ScopePath       string            `json:"scope_path"`
	Depth           *int              `json:"depth"`
	FieldGroups     []string          `json:"field_groups"`
	SelectionHash   string            `json:"selection_hash"`
	Encoding        string            `json:"encoding"`
	PageIndex       int               `json:"page_index"`
	Complete        bool              `json:"complete"`
	NextCursor      *string           `json:"next_cursor"`
	Nodes           []json.RawMessage `json:"nodes"`
	AncestorContext []map[string]any  `json:"ancestor_context"`
	Service         map[string]any    `json:"service"`
	TotalNodes      *int              `json:"total_nodes"`
	TotalBytes      *int64            `json:"total_bytes"`
	SHA256          *string           `json:"sha256"`
}
type FullSnapshotChunkRequest struct {
	Kind           string `json:"kind"`
	SchemaVersion  int    `json:"schema_version"`
	SessionID      string `json:"session_id"`
	FullSnapshotID string `json:"full_snapshot_id"`
	BlobID         string `json:"blob_id"`
	Offset         int    `json:"offset"`
}
type FullSnapshotChunk struct {
	Kind           string `json:"kind"`
	SchemaVersion  int    `json:"schema_version"`
	SessionID      string `json:"session_id"`
	FullSnapshotID string `json:"full_snapshot_id"`
	StateVersion   int64  `json:"state_version"`
	AsOf           string `json:"as_of"`
	BlobID         string `json:"blob_id"`
	Offset         int    `json:"offset"`
	TotalBytes     int    `json:"total_bytes"`
	SHA256         string `json:"sha256"`
	DataBase64     string `json:"data_base64"`
	Complete       bool   `json:"complete"`
}
type CancelFullSnapshotRequest struct {
	Kind           string `json:"kind"`
	SchemaVersion  int    `json:"schema_version"`
	SessionID      string `json:"session_id"`
	FullSnapshotID string `json:"full_snapshot_id"`
}
type CancelFullSnapshotReply struct {
	Kind          string `json:"kind"`
	SchemaVersion int    `json:"schema_version"`
	Cancelled     bool   `json:"cancelled"`
}
