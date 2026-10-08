package common

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
	"google.golang.org/protobuf/proto"
)

func TestQueryV1IncrementalDocumentBudget(t *testing.T) {
	if _, err := DecodeQueryV1(bytes.Repeat([]byte(" "), QueryMaxPageBytes+1), "since"); err == nil {
		t.Fatal("oversized incremental document was accepted")
	}
}

func TestQueryV1SharedVectors(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "query_v1_vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			ID    string `json:"id"`
			Kind  string `json:"kind"`
			Valid bool   `json:"valid"`
			Raw   string `json:"raw"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			value, err := DecodeQueryV1([]byte(tc.Raw), tc.Kind)
			if tc.Valid != (err == nil) {
				t.Fatalf("valid=%v, value=%T, err=%v", tc.Valid, value, err)
			}
			if !tc.Valid {
				return
			}
			if tc.ID == "valid_since_all_operations" {
				batch, ok := value.(*QuerySince)
				if !ok || len(batch.Operations) != 7 || batch.Operations[2].Op != "set_field" || string(batch.Operations[2].Value) != "null" {
					t.Fatalf("typed operations = %#v", value)
				}
			}
			carrier, err := QueryPayload([]byte(tc.Raw), tc.Kind)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := proto.Marshal(carrier)
			if err != nil {
				t.Fatal(err)
			}
			decoded := new(pb.QueryDocumentPayload)
			if err := proto.Unmarshal(wire, decoded); err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeQueryPayload(decoded, tc.Kind); err != nil {
				t.Fatal(err)
			}
		})
	}
}
