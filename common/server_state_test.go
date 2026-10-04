package common

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
)

func TestServerStatusHTTPCommandsAndNullableFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*HttpTransport) (*pb.ServerStatusResponse, error)
	}{
		{"server-status", func(h *HttpTransport) (*pb.ServerStatusResponse, error) {
			return h.RunRequestServerStatus(context.Background(), &pb.ServerStatusRequest{})
		}},
		{"server-halt", func(h *HttpTransport) (*pb.ServerStatusResponse, error) {
			return h.RunCommandServerHalt(context.Background(), &pb.ServerHaltCommand{})
		}},
		{"server-resume", func(h *HttpTransport) (*pb.ServerStatusResponse, error) {
			return h.RunCommandServerResume(context.Background(), &pb.ServerResumeCommand{})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				if r.URL.Path != CommandURLPrefix+tc.name {
					t.Errorf("path = %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"version":"1","trace_id":"%s","command":"%s","payload":{"flag":33,"message":"checkpoint failure","status":"halted","status_reason":"checkpoint failure","halt_causes":["checkpoint_failure"],"last_checkpoint_at":null,"restore_summary":null}}`, requestTrace(t, r), tc.name)
			}))
			defer server.Close()
			transport := &HttpTransport{client: server.Client(), baseURL: server.URL}
			response, err := tc.invoke(transport)
			if err != nil {
				t.Fatal(err)
			}
			if attempts != 1 || response.GetFlag() != 33 || response.GetStatus() != "halted" || response.GetStatusReason() != "checkpoint failure" || len(response.GetHaltCauses()) != 1 {
				t.Fatalf("response=%v attempts=%d", response, attempts)
			}
			formatted, err := FormatServerStatus(response)
			if err != nil || formatted == "" {
				t.Fatalf("format=%s err=%v", formatted, err)
			}
		})
	}
}

func requestTrace(t *testing.T, r *http.Request) string {
	t.Helper()
	var request struct {
		Trace string `json:"trace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Fatal(err)
	}
	return request.Trace
}
