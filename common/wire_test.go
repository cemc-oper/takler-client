package common

import (
	"context"
	"encoding/json"
	pb "github.com/cemc-oper/takler-client/takler_protocol"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSharedWireVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/http_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Cases []struct {
			ID          string `json:"id"`
			Direction   string `json:"direction"`
			Command     string `json:"command"`
			ContentType string `json:"content_type"`
			Body        string `json:"raw_body"`
			Valid       bool   `json:"expected_wire_valid"`
			Request     struct {
				Command string         `json:"command"`
				Trace   string         `json:"trace_id"`
				Payload map[string]any `json:"payload"`
			} `json:"request"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	for _, c := range data.Cases {
		t.Run(c.ID, func(t *testing.T) {
			command, trace := "", ""
			if c.Direction == "response" {
				command = c.Request.Command
				trace = c.Request.Trace
			}
			value, e := decodeWire([]byte(c.Body), c.Direction, command, trace)
			valid := e == nil && jsonContentType(c.ContentType)
			if valid && c.Direction == "request" {
				valid = value["command"] == c.Command
			}
			if valid && c.Direction == "response" && isBatchCommand(c.Command) {
				raw, _ := json.Marshal(value["payload"])
				var response pb.BatchResponse
				_ = json.Unmarshal(raw, &response)
				targets := []string{}
				for _, v := range c.Request.Payload["node_paths"].([]any) {
					targets = append(targets, v.(string))
				}
				valid = validateBatch(&pb.SuspendCommand{NodePath: targets}, &response) == nil
			}
			if c.Direction == "response" {
				attempts := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					attempts++
					var request struct {
						Trace string `json:"trace_id"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					w.Header().Set("Content-Type", c.ContentType)
					_, _ = w.Write([]byte(strings.ReplaceAll(c.Body, c.Request.Trace, request.Trace)))
				}))
				defer server.Close()
				transport := &HttpTransport{client: server.Client(), baseURL: server.URL}
				payload, boundaryErr := transport.post(context.Background(), c.Request.Command, c.Request.Payload)
				if boundaryErr == nil && isBatchCommand(c.Command) {
					var response pb.BatchResponse
					if err := json.Unmarshal(payload, &response); err != nil {
						t.Fatal(err)
					}
					targets := []string{}
					for _, v := range c.Request.Payload["node_paths"].([]any) {
						targets = append(targets, v.(string))
					}
					boundaryErr = validateBatch(&pb.SuspendCommand{NodePath: targets}, &response)
				}
				if (boundaryErr == nil) != c.Valid || attempts != 1 {
					t.Fatalf("boundary valid=%v attempts=%d", boundaryErr == nil, attempts)
				}
				if boundaryErr != nil && !isBatchCommand(c.Command) && classifyHttpError(boundaryErr).ExitCode != ExitServerError {
					t.Fatal(boundaryErr)
				}
			}
			if valid != c.Valid {
				t.Fatalf("valid=%v want=%v err=%v", valid, c.Valid, e)
			}
		})
	}
}

// Expand the shared seed corpus to every field of all 19 payload schemas.
func TestEveryWireFieldRejectsMissingNullAndWrongTypes(t *testing.T) {
	values := map[string]any{"s": "x", "b": false, "as": []any{"/f"}, "decimal": "0", "base64": "AA==", "state": "complete", "dep": "all", "flag": 0, "uint32": 0, "effect": "applied", "items": []any{}, "coroutines": []any{}, "ns": "x", "no": map[string]any{}, "server_status": "running"}
	for _, direction := range []string{"request", "response"} {
		var schemas map[string]map[string]string
		if err := json.Unmarshal(wireSchema[direction], &schemas); err != nil {
			t.Fatal(err)
		}
		if len(schemas) != 19 {
			t.Fatalf("schema count %d", len(schemas))
		}
		for command, schema := range schemas {
			t.Run(direction+"/"+command, func(t *testing.T) {
				payload := map[string]any{}
				for key, kind := range schema {
					payload[key] = values[kind]
				}
				envelope := map[string]any{"version": "1", "command": command, "trace_id": strings.Repeat("0", 32), "payload": payload}
				check := func(want bool) {
					raw, err := json.Marshal(envelope)
					if err != nil {
						t.Fatal(err)
					}
					_, err = decodeWire(raw, direction, "", "")
					if (err == nil) != want {
						t.Fatalf("valid=%v want=%v fields=%v", err == nil, want, schema)
					}
				}
				check(true)
				for key, kind := range schema {
					delete(payload, key)
					check(false)
					for _, bad := range []any{nil, map[string]any{}, 1.5} {
						if bad == nil && (kind == "ns" || kind == "no") {
							continue
						}
						if _, ok := bad.(map[string]any); ok && kind == "no" {
							continue
						}
						payload[key] = bad
						check(false)
					}
					payload[key] = values[kind]
				}
				payload["UNKNOWN"] = true
				check(false)
			})
		}
	}
}

func TestStrictWireRejectsInvalidUTF8AndBOM(t *testing.T) {
	for _, raw := range [][]byte{{0xff}, append([]byte{0xef, 0xbb, 0xbf}, []byte(`{}`)...), {'"', 0xff, '"'}} {
		if _, err := strictWireJSON(raw); err == nil {
			t.Fatal("invalid bytes accepted")
		}
	}
}
