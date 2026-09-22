// The Control_Commands, i.e. the mutations an operator performs.
//
// As in client_child.go, a method here builds a request and hands the response
// back; the connection, the timeout, the retry and the Operator credentials
// belong to CallCommand (requirements 14.1, 13.7), and a failure returns as an
// *ExitError instead of ending the process (requirement 15.9).
//
// KindControl is what selects the one minute Retry_Window: an operator's
// terminal must not hang for a day, unlike a job script's child command.
package common

import (
	"fmt"
	"os"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
)

// RunCommandRequeue requeues every node in nodePaths.
//
// The returned response is the server's, flag included: a non zero flag is a
// business failure the caller turns into an exit code, not an error of the call
// (requirement 14.8).
func (c *TaklerServiceClient) RunCommandRequeue(nodePaths []string) (*pb.ServiceResponse, error) {
	return CallCommand(c, "requeue", KindControl, &pb.RequeueCommand{
		NodePath: nodePaths,
	}, Transport.RunCommandRequeue)
}

// RunCommandSuspend suspends every node in nodePaths.
func (c *TaklerServiceClient) RunCommandSuspend(nodePaths []string) (*pb.ServiceResponse, error) {
	return CallCommand(c, "suspend", KindControl, &pb.SuspendCommand{
		NodePath: nodePaths,
	}, Transport.RunCommandSuspend)
}

// RunCommandResume resumes every node in nodePaths.
func (c *TaklerServiceClient) RunCommandResume(nodePaths []string) (*pb.ServiceResponse, error) {
	return CallCommand(c, "resume", KindControl, &pb.ResumeCommand{
		NodePath: nodePaths,
	}, Transport.RunCommandResume)
}

// RunCommandRun runs every node in nodePaths, ignoring their dependencies when
// force is set.
func (c *TaklerServiceClient) RunCommandRun(nodePaths []string, force bool) (*pb.ServiceResponse, error) {
	return CallCommand(c, "run", KindControl, &pb.RunCommand{
		NodePath: nodePaths,
		Force:    force,
	}, Transport.RunCommandRun)
}

// RunCommandForce sets every node in nodePaths to the state named state,
// recursively when recursive is set.
//
// state is one of the ForceCommand.ForceState names (unknown, complete,
// queued, submitted, active, aborted, clear, set): the string is translated
// client side, exactly as the Python client's ForceState.Value does, so an
// unknown name never reaches the server. The failure is reported with
// ExitServerError, mirroring what that ValueError becomes on the Python side:
// it is not a TaklerError, so it lands on the generic exception path of the
// CLI's exit code mapping (requirement 15.5).
func (c *TaklerServiceClient) RunCommandForce(nodePaths []string, state string, recursive bool) (*pb.ServiceResponse, error) {
	forceState, ok := pb.ForceCommand_ForceState_value[state]
	if !ok {
		return nil, NewExitError(
			ExitServerError,
			fmt.Sprintf(
				"invalid force state %q, want one of: unknown, complete, queued, submitted, active, aborted, clear, set",
				state,
			),
		)
	}

	return CallCommand(c, "force", KindControl, &pb.ForceCommand{
		State:     pb.ForceCommand_ForceState(forceState),
		Recursive: recursive,
		Path:      nodePaths,
	}, Transport.RunCommandForce)
}

// RunCommandFreeDep frees the dependencies of class depType on every node in
// nodePaths.
//
// depType is one of the FreeDepCommand.DepType names (all, trigger, time) and
// is translated client side for the same reason, and with the same failure
// mapping, as the state of RunCommandForce.
func (c *TaklerServiceClient) RunCommandFreeDep(nodePaths []string, depType string) (*pb.ServiceResponse, error) {
	depTypeValue, ok := pb.FreeDepCommand_DepType_value[depType]
	if !ok {
		return nil, NewExitError(
			ExitServerError,
			fmt.Sprintf(
				"invalid dependency type %q, want one of: all, trigger, time",
				depType,
			),
		)
	}

	return CallCommand(c, "free-dep", KindControl, &pb.FreeDepCommand{
		DepType: pb.FreeDepCommand_DepType(depTypeValue),
		Path:    nodePaths,
	}, Transport.RunCommandFreeDep)
}

// RunCommandLoad loads the flow definition in flowFilePath to the server.
// flowType names the file's format; the server currently supports "json".
//
// The file is read before the connection is opened, as the Python client's
// run_command_load does, so an unreadable file fails without touching the
// network. Its exit code is ExitServerError for the same reason an invalid
// force state is: the Python side raises FileNotFoundError, which is not a
// TaklerError either and lands on the same generic path.
func (c *TaklerServiceClient) RunCommandLoad(flowType string, flowFilePath string) (*pb.ServiceResponse, error) {
	flow, err := os.ReadFile(flowFilePath)
	if err != nil {
		return nil, NewExitError(
			ExitServerError,
			fmt.Sprintf("cannot read the flow file %s: %v", flowFilePath, err),
		)
	}

	return CallCommand(c, "load", KindControl, &pb.LoadCommand{
		FlowType: flowType,
		Flow:     flow,
	}, Transport.RunCommandLoad)
}

// RunCommandBegin begins the flow named flowName, or every flow when flowName
// is empty (requirement 8.1 of the Python side, whose empty string is the wire
// form of "all flows"). force begins an already begun flow again.
func (c *TaklerServiceClient) RunCommandBegin(flowName string, force bool) (*pb.ServiceResponse, error) {
	return CallCommand(c, "begin", KindControl, &pb.BeginCommand{
		FlowName: flowName,
		Force:    force,
	}, Transport.RunCommandBegin)
}
