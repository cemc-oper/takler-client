// The Go half of the command surface drift guard.
//
// tests/client/test_cross_language_contract.py in the takler repository
// restates, by hand, the same three tables: the sixteen RPCs with their
// request and response types, and the ForceState / DepType name to number
// mappings. Both halves read the tables from their own generated code and
// compare against the hand written contract, so a proto or enum change merged
// on only one side fails that side's test rather than silently diverging the
// two clients.
//
// Every expected value below is therefore a literal, mirroring the Python
// file's rule: a table read back from the generated code it is meant to
// police could never detect a wrong edit to that code.
package common

import (
	"testing"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// contractRPCSurface is the RPC name -> (request, response) table of the
// contract's sixteen commands. RunCommandResume taking its own ResumeCommand
// (rather than reusing SuspendCommand) and RunCommandBegin existing at all are
// both pinned here.
var contractRPCSurface = map[string][2]string{
	"RunCommandInit":     {"InitCommand", "ServiceResponse"},
	"RunCommandComplete": {"CompleteCommand", "ServiceResponse"},
	"RunCommandAbort":    {"AbortCommand", "ServiceResponse"},
	"RunCommandEvent":    {"EventCommand", "ServiceResponse"},
	"RunCommandMeter":    {"MeterCommand", "ServiceResponse"},
	"RunCommandRequeue":  {"RequeueCommand", "BatchResponse"},
	"RunCommandSuspend":  {"SuspendCommand", "BatchResponse"},
	"RunCommandResume":   {"ResumeCommand", "BatchResponse"},
	"RunCommandRun":      {"RunCommand", "BatchResponse"},
	"RunCommandForce":    {"ForceCommand", "BatchResponse"},
	"RunCommandFreeDep":  {"FreeDepCommand", "BatchResponse"},
	"RunCommandLoad":     {"LoadCommand", "ServiceResponse"},
	"RunCommandBegin":    {"BeginCommand", "BatchResponse"},
	"RunRequestShow":     {"ShowRequest", "ShowResponse"},
	"RunRequestPing":     {"PingRequest", "PingResponse"},
	"QueryCoroutine":     {"CoroutineRequest", "CoroutineResponse"},
}

// contractForceStates is the ForceCommand.ForceState name -> number table: the
// values the force command's state argument may take. Both clients translate
// the name on their own side, so the tables must agree exactly.
var contractForceStates = map[string]int32{
	"unknown":   0,
	"complete":  1,
	"queued":    2,
	"submitted": 3,
	"active":    4,
	"aborted":   5,
	"clear":     6,
	"set":       7,
}

// contractDepTypes is the FreeDepCommand.DepType name -> number table, the
// free-dep command's --dep-type values.
var contractDepTypes = map[string]int32{
	"all":     0,
	"trigger": 1,
	"time":    2,
}

func TestRPCSurfaceMatchesContract(t *testing.T) {
	service := pb.File_takler_protocol_takler_proto.Services().ByName("TaklerServer")
	if service == nil {
		t.Fatal("the descriptor has no TaklerServer service")
	}

	methods := service.Methods()
	if got, want := methods.Len(), len(contractRPCSurface); got != want {
		t.Errorf("RPC count = %d, want %d", got, want)
	}
	for name, want := range contractRPCSurface {
		method := methods.ByName(protoreflect.Name(name))
		if method == nil {
			t.Errorf("RPC %s is missing from the descriptor", name)
			continue
		}
		if got := string(method.Input().Name()); got != want[0] {
			t.Errorf("RPC %s request = %s, want %s", name, got, want[0])
		}
		if got := string(method.Output().Name()); got != want[1] {
			t.Errorf("RPC %s response = %s, want %s", name, got, want[1])
		}
	}
}

// enumTable reads a nested enum's name -> number mapping out of the
// descriptor, e.g. enumTable("ForceCommand", "ForceState").
func enumTable(t *testing.T, message, enum string) map[string]int32 {
	t.Helper()

	descriptor := pb.File_takler_protocol_takler_proto.Messages().ByName(protoreflect.Name(message))
	if descriptor == nil {
		t.Fatalf("the descriptor has no %s message", message)
	}
	enumDescriptor := descriptor.Enums().ByName(protoreflect.Name(enum))
	if enumDescriptor == nil {
		t.Fatalf("the %s message has no %s enum", message, enum)
	}

	table := make(map[string]int32, enumDescriptor.Values().Len())
	for i := 0; i < enumDescriptor.Values().Len(); i++ {
		value := enumDescriptor.Values().Get(i)
		table[string(value.Name())] = int32(value.Number())
	}
	return table
}

func TestForceStatesMatchContract(t *testing.T) {
	actual := enumTable(t, "ForceCommand", "ForceState")
	if len(actual) != len(contractForceStates) {
		t.Errorf("ForceState count = %d, want %d", len(actual), len(contractForceStates))
	}
	for name, want := range contractForceStates {
		if got, ok := actual[name]; !ok || got != want {
			t.Errorf("ForceState[%q] = %d, %t, want %d", name, got, ok, want)
		}
	}
}

func TestDepTypesMatchContract(t *testing.T) {
	actual := enumTable(t, "FreeDepCommand", "DepType")
	if len(actual) != len(contractDepTypes) {
		t.Errorf("DepType count = %d, want %d", len(actual), len(contractDepTypes))
	}
	for name, want := range contractDepTypes {
		if got, ok := actual[name]; !ok || got != want {
			t.Errorf("DepType[%q] = %d, %t, want %d", name, got, ok, want)
		}
	}
}
