// Request construction tests of the Control_Commands and the coroutine query,
// against the in-process server (requirement 16.12).
//
// Where client_command_test.go proves every method is wired to the
// Call_Wrapper, this file proves what goes on the wire: the enum names are
// translated client side exactly as the Python client's ForceState.Value /
// DepType.Value do, the flow file's bytes travel in LoadCommand, an empty flow
// name reaches BeginCommand untouched, and resume speaks its own request type
// rather than reusing SuspendCommand. The five commands the M3 protocol work
// adds to this client are the main subjects.
package common

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
)

// captureStdout runs body with standard output redirected, and returns what it
// wrote there. The coroutine query prints with fmt.Printf, so this is the only
// way to read back what an operator would see. Same helper as the cmd layer's,
// duplicated because test helpers do not cross package boundaries.
func captureStdout(t *testing.T, body func()) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	saved := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = saved }()

	done := make(chan string, 1)
	go func() {
		output, _ := io.ReadAll(reader)
		done <- string(output)
	}()

	body()

	if err := writer.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	return <-done
}

// assertExitError checks that err is an *ExitError with the given code whose
// message contains every one of the fragments.
func assertExitError(t *testing.T, err error, code int, fragments ...string) {
	t.Helper()

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %v, want *ExitError", err)
	}
	if exitErr.Code != code {
		t.Errorf("exit code = %d, want %d", exitErr.Code, code)
	}
	for _, fragment := range fragments {
		if !strings.Contains(exitErr.Message, fragment) {
			t.Errorf("message = %q, want it to contain %q", exitErr.Message, fragment)
		}
	}
}

// The state name is translated to the ForceState enum on the client, together
// with the paths and the recursive flag.
func TestRunCommandForceBuildsTheRequest(t *testing.T) {
	client, server := newTCPClient(t)

	_, err := client.RunCommandForce([]string{"/flow1/task1", "/flow1/task2"}, "complete", true)
	if err != nil {
		t.Fatalf("RunCommandForce: %v", err)
	}

	call := server.lastCall(t)
	if call.Method != "RunCommandForce" {
		t.Errorf("method = %q, want %q", call.Method, "RunCommandForce")
	}
	request, ok := call.Request.(*pb.ForceCommand)
	if !ok {
		t.Fatalf("request = %T, want *pb.ForceCommand", call.Request)
	}
	if request.GetState() != pb.ForceCommand_complete {
		t.Errorf("state = %v, want %v", request.GetState(), pb.ForceCommand_complete)
	}
	if !request.GetRecursive() {
		t.Error("recursive = false, want true")
	}
	if got, want := request.GetPath(), []string{"/flow1/task1", "/flow1/task2"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("path = %v, want %v", got, want)
	}
}

// An unknown state name fails on the client, before the server is touched,
// with the exit code the Python CLI's ValueError lands on (requirement 15.5:
// not a TaklerError, so the generic mapping).
func TestRunCommandForceRejectsAnUnknownState(t *testing.T) {
	client, server := newTCPClient(t)

	_, err := client.RunCommandForce([]string{"/flow1"}, "done", false)

	assertExitError(t, err, ExitServerError, `"done"`, "complete")
	if got := server.callCount(); got != 0 {
		t.Errorf("server received %d calls, want none", got)
	}
}

// The dependency type name is translated to the DepType enum on the client.
func TestRunCommandFreeDepBuildsTheRequest(t *testing.T) {
	client, server := newTCPClient(t)

	_, err := client.RunCommandFreeDep([]string{"/flow1"}, "trigger")
	if err != nil {
		t.Fatalf("RunCommandFreeDep: %v", err)
	}

	request, ok := server.lastCall(t).Request.(*pb.FreeDepCommand)
	if !ok {
		t.Fatalf("request = %T, want *pb.FreeDepCommand", server.lastCall(t).Request)
	}
	if request.GetDepType() != pb.FreeDepCommand_trigger {
		t.Errorf("dep type = %v, want %v", request.GetDepType(), pb.FreeDepCommand_trigger)
	}
}

// Same client side rejection as the force state, for the same reason.
func TestRunCommandFreeDepRejectsAnUnknownDepType(t *testing.T) {
	client, server := newTCPClient(t)

	_, err := client.RunCommandFreeDep([]string{"/flow1"}, "everything")

	assertExitError(t, err, ExitServerError, `"everything"`, "all")
	if got := server.callCount(); got != 0 {
		t.Errorf("server received %d calls, want none", got)
	}
}

// The flow file's bytes and the flow type travel in LoadCommand.
func TestRunCommandLoadSendsTheFileBytes(t *testing.T) {
	client, server := newTCPClient(t)

	flowFile := filepath.Join(t.TempDir(), "flow1.json")
	flowBytes := []byte(`{"name": "flow1"}`)
	if err := os.WriteFile(flowFile, flowBytes, 0o600); err != nil {
		t.Fatalf("write flow file: %v", err)
	}

	_, err := client.RunCommandLoad("json", flowFile)
	if err != nil {
		t.Fatalf("RunCommandLoad: %v", err)
	}

	request, ok := server.lastCall(t).Request.(*pb.LoadCommand)
	if !ok {
		t.Fatalf("request = %T, want *pb.LoadCommand", server.lastCall(t).Request)
	}
	if request.GetFlowType() != "json" {
		t.Errorf("flow type = %q, want %q", request.GetFlowType(), "json")
	}
	if got := request.GetFlow(); string(got) != string(flowBytes) {
		t.Errorf("flow = %q, want %q", got, flowBytes)
	}
}

// An unreadable flow file fails before the server is touched, mirroring the
// Python client's FileNotFoundError, which is not a TaklerError either.
func TestRunCommandLoadReportsAnUnreadableFile(t *testing.T) {
	client, server := newTCPClient(t)

	missing := filepath.Join(t.TempDir(), "absent.json")
	_, err := client.RunCommandLoad("json", missing)

	assertExitError(t, err, ExitServerError, missing)
	if got := server.callCount(); got != 0 {
		t.Errorf("server received %d calls, want none", got)
	}
}

// The flow name and the force flag reach BeginCommand untouched; the empty
// name included, which the protocol reads as "all flows".
func TestRunCommandBeginBuildsTheRequest(t *testing.T) {
	cases := []struct {
		name     string
		flowName string
		force    bool
	}{
		{"named flow", "flow1", false},
		{"all flows", "", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, server := newTCPClient(t)

			_, err := client.RunCommandBegin(c.flowName, c.force)
			if err != nil {
				t.Fatalf("RunCommandBegin: %v", err)
			}

			request, ok := server.lastCall(t).Request.(*pb.BeginCommand)
			if !ok {
				t.Fatalf("request = %T, want *pb.BeginCommand", server.lastCall(t).Request)
			}
			if request.GetFlowName() != c.flowName {
				t.Errorf("flow name = %q, want %q", request.GetFlowName(), c.flowName)
			}
			if request.GetForce() != c.force {
				t.Errorf("force = %t, want %t", request.GetForce(), c.force)
			}
		})
	}
}

// Resume speaks its own request type. The proto used to reuse SuspendCommand
// for it; the split is what this pins.
func TestRunCommandResumeSendsAResumeCommand(t *testing.T) {
	client, server := newTCPClient(t)

	_, err := client.RunCommandResume([]string{"/flow1"})
	if err != nil {
		t.Fatalf("RunCommandResume: %v", err)
	}

	call := server.lastCall(t)
	if call.Method != "RunCommandResume" {
		t.Errorf("method = %q, want %q", call.Method, "RunCommandResume")
	}
	request, ok := call.Request.(*pb.ResumeCommand)
	if !ok {
		t.Fatalf("request = %T, want *pb.ResumeCommand", call.Request)
	}
	if got, want := request.GetNodePath(), []string{"/flow1"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("node path = %v, want %v", got, want)
	}
}

// The coroutine query prints one "name<TAB>description" line per coroutine,
// the same layout the Python client's run_query_coroutine writes.
func TestRunQueryCoroutinePrintsTheCoroutines(t *testing.T) {
	client, _ := newTCPClient(t, fakeWithCoroutines(
		&pb.Coroutine{Name: "scheduler", Description: "main loop"},
		&pb.Coroutine{Name: "checkpoint", Description: "periodic snapshot"},
	))

	var err error
	output := captureStdout(t, func() {
		_, err = client.RunQueryCoroutine()
	})
	if err != nil {
		t.Fatalf("RunQueryCoroutine: %v", err)
	}

	want := "scheduler\tmain loop\ncheckpoint\tperiodic snapshot\n"
	if output != want {
		t.Errorf("output = %q, want %q", output, want)
	}
}
