package common

import (
	pb "github.com/cemc-oper/takler-client/takler_protocol"
	"google.golang.org/grpc/codes"
	"testing"
)

func TestBatchMutationsNeverRetry(t *testing.T) {
	for _, wire := range []string{"grpc", "http"} {
		for _, name := range []string{"requeue", "suspend", "resume", "run", "force", "free-dep", "begin"} {
			t.Run(wire+"/"+name, func(t *testing.T) {
				t.Setenv(EnvRetryWindow, "600")
				var client *TaklerServiceClient
				var count func() int
				if wire == "grpc" {
					c, s := newTCPClient(t, fakeAlwaysFail(codes.Unavailable))
					client = c
					count = s.callCount
				} else {
					s := newFakeHTTPServer(t, httpFailFirst(-1, 503))
					client = newHTTPClient(t, s)
					count = s.callCount
				}
				var err error
				switch name {
				case "requeue":
					_, err = client.RunCommandRequeue([]string{"/f/a"})
				case "suspend":
					_, err = client.RunCommandSuspend([]string{"/f/a"})
				case "resume":
					_, err = client.RunCommandResume([]string{"/f/a"})
				case "run":
					_, err = client.RunCommandRun([]string{"/f/a"}, false)
				case "force":
					_, err = client.RunCommandForce([]string{"/f/a"}, "complete", true)
				case "free-dep":
					_, err = client.RunCommandFreeDep([]string{"/f/a"}, "all")
				case "begin":
					_, err = client.RunCommandBegin("f", false)
				}
				assertExitError(t, err, ExitUnreachable)
				if count() != 1 {
					t.Fatalf("attempts=%d", count())
				}
			})
		}
	}
}

func TestBatchResponseAssociation(t *testing.T) {
	req := &pb.RunCommand{NodePath: []string{"/f/a", "/missing", "/f/b"}}
	makeResponse := func() *pb.BatchResponse {
		return &pb.BatchResponse{Flag: 16, Results: []*pb.BatchItemResult{
			{Index: 0, Target: "/f/a", Effect: "applied"}, {Index: 1, Target: "/missing", Flag: 10, Effect: "none"}, {Index: 2, Target: "/f/b", Effect: "applied"},
		}}
	}
	if err := validateBatch(req, makeResponse()); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*pb.BatchResponse){
		func(r *pb.BatchResponse) { r.Results[0].Index = 8 }, func(r *pb.BatchResponse) { r.Results[0].Target = "/wrong" },
		func(r *pb.BatchResponse) { r.Results = r.Results[:1] }, func(r *pb.BatchResponse) { r.Flag = 0 },
		func(r *pb.BatchResponse) { r.Results[0].Effect = "bad" },
	} {
		r := makeResponse()
		change(r)
		if validateBatch(req, r) == nil {
			t.Fatal("accepted malformed result")
		}
	}
}

func TestHTTPBatchMixedAndMalformedFields(t *testing.T) {
	good := map[string]any{"flag": 16, "message": "mixed", "results": []any{
		map[string]any{"index": 0, "target": "/f/a", "flag": 0, "message": "ok", "effect": "applied"},
		map[string]any{"index": 1, "target": "/missing", "flag": 10, "message": "missing", "effect": "none"},
	}}
	server := newFakeHTTPServer(t, httpWithPayloads(map[string]any{"run": good}))
	response, err := newHTTPClient(t, server).RunCommandRun([]string{"/f/a", "/missing"}, false)
	if err != nil || response.Flag != 16 || len(response.Results) != 2 {
		t.Fatalf("response=%v err=%v", response, err)
	}
	for _, body := range []map[string]any{
		{"flag": 0, "message": "ok"}, {"flag": 0, "message": "ok", "results": nil},
		{"flag": 0, "message": "ok", "results": []any{map[string]any{"target": "/f/a"}}},
		{"flag": 0, "message": "ok", "results": []any{}, "extra": true},
	} {
		s := newFakeHTTPServer(t, httpWithPayloads(map[string]any{"run": body}))
		_, err := newHTTPClient(t, s).RunCommandRun([]string{"/f/a"}, false)
		assertExitError(t, err, ExitServerError)
	}
}
