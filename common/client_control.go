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
	pb "github.com/perillaroc/takler-client/takler_protocol"
)

// RunCommandRequeue requeues every node in nodePaths.
//
// The returned response is the server's, flag included: a non zero flag is a
// business failure the caller turns into an exit code, not an error of the call
// (requirement 14.8).
func (c *TaklerServiceClient) RunCommandRequeue(nodePaths []string) (*pb.ServiceResponse, error) {
	return CallCommand(c, "requeue", KindControl, &pb.RequeueCommand{
		NodePath: nodePaths,
	}, pb.TaklerServerClient.RunCommandRequeue)
}

// RunCommandSuspend suspends every node in nodePaths.
func (c *TaklerServiceClient) RunCommandSuspend(nodePaths []string) (*pb.ServiceResponse, error) {
	return CallCommand(c, "suspend", KindControl, &pb.SuspendCommand{
		NodePath: nodePaths,
	}, pb.TaklerServerClient.RunCommandSuspend)
}

// RunCommandResume resumes every node in nodePaths.
//
// The request type is SuspendCommand, as resume is the same request with a
// different RPC in the protocol; the proto is unchanged in M2, so this stays as
// it is.
func (c *TaklerServiceClient) RunCommandResume(nodePaths []string) (*pb.ServiceResponse, error) {
	return CallCommand(c, "resume", KindControl, &pb.SuspendCommand{
		NodePath: nodePaths,
	}, pb.TaklerServerClient.RunCommandResume)
}

// RunCommandRun runs every node in nodePaths, ignoring their dependencies when
// force is set.
func (c *TaklerServiceClient) RunCommandRun(nodePaths []string, force bool) (*pb.ServiceResponse, error) {
	return CallCommand(c, "run", KindControl, &pb.RunCommand{
		NodePath: nodePaths,
		Force:    force,
	}, pb.TaklerServerClient.RunCommandRun)
}
