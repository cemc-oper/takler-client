// The five Child_Commands, i.e. the calls a job script makes.
//
// Every method here is a request built and a response handed back: the
// connection, the per-attempt timeout, the backoff retry and the Job_Password
// metadata are all CallCommand's business (requirements 14.1, 13.7). Nothing in
// this file calls log.Fatalf: a failure is an *ExitError travelling up to the
// cmd layer, which is the only place that ends the process (requirement 15.9).
//
// KindChild is what gives these calls the day long Retry_Window and the
// takler-pass metadata; a control or query call is classified differently in
// its own file.
package common

import (
	pb "github.com/cemc-oper/takler-client/takler_protocol"
)

// RunCommandInit reports that the task at nodePath has started, under the job
// identity taskId.
//
// The returned response is the server's, flag included: a non zero flag is a
// business failure the caller turns into an exit code, not an error of the call
// (requirement 14.8).
func (c *TaklerServiceClient) RunCommandInit(nodePath string, taskId string) (*pb.ServiceResponse, error) {
	return CallCommand(c, "init", KindChild, &pb.InitCommand{
		ChildOptions: &pb.ChildCommandOptions{
			NodePath: nodePath,
		},
		TaskId: taskId,
	}, Transport.RunCommandInit)
}

// RunCommandComplete reports that the task at nodePath finished successfully.
func (c *TaklerServiceClient) RunCommandComplete(nodePath string) (*pb.ServiceResponse, error) {
	return CallCommand(c, "complete", KindChild, &pb.CompleteCommand{
		ChildOptions: &pb.ChildCommandOptions{
			NodePath: nodePath,
		},
	}, Transport.RunCommandComplete)
}

// RunCommandAbort reports that the task at nodePath failed, with reason as the
// text shown to an operator.
func (c *TaklerServiceClient) RunCommandAbort(nodePath string, reason string) (*pb.ServiceResponse, error) {
	return CallCommand(c, "abort", KindChild, &pb.AbortCommand{
		ChildOptions: &pb.ChildCommandOptions{
			NodePath: nodePath,
		},
		Reason: reason,
	}, Transport.RunCommandAbort)
}

// RunCommandEvent sets the event eventName of the task at nodePath.
func (c *TaklerServiceClient) RunCommandEvent(nodePath string, eventName string) (*pb.ServiceResponse, error) {
	return CallCommand(c, "event", KindChild, &pb.EventCommand{
		ChildOptions: &pb.ChildCommandOptions{
			NodePath: nodePath,
		},
		EventName: eventName,
	}, Transport.RunCommandEvent)
}

// RunCommandMeter sets the meter meterName of the task at nodePath to
// meterValue.
func (c *TaklerServiceClient) RunCommandMeter(nodePath string, meterName string, meterValue string) (*pb.ServiceResponse, error) {
	return CallCommand(c, "meter", KindChild, &pb.MeterCommand{
		ChildOptions: &pb.ChildCommandOptions{
			NodePath: nodePath,
		},
		MeterName:  meterName,
		MeterValue: meterValue,
	}, Transport.RunCommandMeter)
}
