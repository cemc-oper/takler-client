package common

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	pb "github.com/cemc-oper/takler-client/takler_protocol"
	"io"
)

func isBatchCommand(name string) bool {
	switch name {
	case "requeue", "suspend", "resume", "run", "force", "free-dep", "begin":
		return true
	}
	return false
}

func validateBatch(req any, response *pb.BatchResponse) error {
	invalid := func() error { return NewExitError(ExitServerError, "invalid batch response") }
	if response == nil {
		return invalid()
	}
	if len(response.Results) == 0 && response.Flag != 0 && response.Flag != 16 {
		return nil
	}
	var targets []string
	switch r := req.(type) {
	case interface{ GetNodePath() []string }:
		targets = r.GetNodePath()
	case interface{ GetPath() []string }:
		targets = r.GetPath()
	case *pb.BeginCommand:
		if r.FlowName != "" {
			targets = []string{"/" + r.FlowName}
		}
	}
	if targets != nil && len(targets) != len(response.Results) {
		return invalid()
	}
	failed := false
	for i, r := range response.Results {
		if r == nil || r.Index != uint32(i) || (targets != nil && r.Target != targets[i]) {
			return invalid()
		}
		switch r.Effect {
		case "none", "applied", "partial", "unknown":
		default:
			return invalid()
		}
		if r.Flag != 0 {
			failed = true
		}
	}
	expected := int32(0)
	if failed {
		expected = 16
	}
	if response.Flag != expected {
		return invalid()
	}
	return nil
}

func (t *HttpTransport) batchCall(ctx context.Context, command string, payload map[string]any) (*pb.BatchResponse, error) {
	raw, err := t.post(ctx, command, payload)
	if err != nil {
		return nil, err
	}
	// Pointers distinguish required zero values from missing/null fields.
	var body struct {
		Flag    *int32  `json:"flag"`
		Message *string `json:"message"`
		Results *[]struct {
			Index   *uint32 `json:"index"`
			Target  *string `json:"target"`
			Flag    *int32  `json:"flag"`
			Message *string `json:"message"`
			Effect  *string `json:"effect"`
		} `json:"results"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&body); err != nil {
		return nil, &httpResponseError{err: err}
	}
	if decoder.Decode(new(any)) != io.EOF || body.Flag == nil || body.Message == nil || body.Results == nil {
		return nil, &httpResponseError{err: fmt.Errorf("invalid batch fields")}
	}
	response := &pb.BatchResponse{Flag: *body.Flag, Message: *body.Message, Results: []*pb.BatchItemResult{}}
	for _, r := range *body.Results {
		if r.Index == nil || r.Target == nil || r.Flag == nil || r.Message == nil || r.Effect == nil {
			return nil, &httpResponseError{err: fmt.Errorf("missing batch item fields")}
		}
		response.Results = append(response.Results, &pb.BatchItemResult{Index: *r.Index, Target: *r.Target, Flag: *r.Flag, Message: *r.Message, Effect: *r.Effect})
	}
	return response, nil
}
