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
	"regexp"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
)

var attemptPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidateAttemptID rejects missing or non-canonical UUIDs before a child call.
func ValidateAttemptID(value string) error {
	if !attemptPattern.MatchString(value) {
		return NewExitError(ExitRequestError, "invalid or missing attempt_id")
	}
	return nil
}

func childOptions(nodePath, attemptID, source string) (*pb.ChildCommandOptions, error) {
	if err := ValidateAttemptID(attemptID); err != nil {
		return nil, err
	}
	return &pb.ChildCommandOptions{NodePath: nodePath, AttemptId: attemptID, SourceTaskPath: source}, nil
}

// RunCommandInit reports that the task at nodePath has started, under the job
// identity taskId.
//
// The returned response is the server's, flag included: a non zero flag is a
// business failure the caller turns into an exit code, not an error of the call
// (requirement 14.8).
func (c *TaklerServiceClient) RunCommandInit(nodePath string, taskId string, attemptID string) (*pb.ServiceResponse, error) {
	options, err := childOptions(nodePath, attemptID, "")
	if err != nil {
		return nil, err
	}
	return CallCommand(c, "init", KindChild, &pb.InitCommand{
		ChildOptions: options,
		TaskId:       taskId,
	}, Transport.RunCommandInit)
}

// RunCommandComplete reports that the task at nodePath finished successfully.
func (c *TaklerServiceClient) RunCommandComplete(nodePath string, attemptID string) (*pb.ServiceResponse, error) {
	options, err := childOptions(nodePath, attemptID, "")
	if err != nil {
		return nil, err
	}
	return CallCommand(c, "complete", KindChild, &pb.CompleteCommand{
		ChildOptions: options,
	}, Transport.RunCommandComplete)
}

// RunCommandAbort reports that the task at nodePath failed, with reason as the
// text shown to an operator.
func (c *TaklerServiceClient) RunCommandAbort(nodePath string, reason string, attemptID string) (*pb.ServiceResponse, error) {
	options, err := childOptions(nodePath, attemptID, "")
	if err != nil {
		return nil, err
	}
	return CallCommand(c, "abort", KindChild, &pb.AbortCommand{
		ChildOptions: options,
		Reason:       reason,
	}, Transport.RunCommandAbort)
}

// RunCommandEvent sets an event on nodePath on behalf of sourceTaskPath.
func (c *TaklerServiceClient) RunCommandEvent(nodePath string, eventName string, attemptID string, sourceTaskPath string) (*pb.ServiceResponse, error) {
	if sourceTaskPath == "" {
		sourceTaskPath = nodePath
	}
	options, err := childOptions(nodePath, attemptID, sourceTaskPath)
	if err != nil {
		return nil, err
	}
	return CallCommand(c, "event", KindChild, &pb.EventCommand{
		ChildOptions: options,
		EventName:    eventName,
	}, Transport.RunCommandEvent)
}

// RunCommandMeter sets a meter on nodePath on behalf of sourceTaskPath.
func (c *TaklerServiceClient) RunCommandMeter(nodePath string, meterName string, meterValue string, attemptID string, sourceTaskPath string) (*pb.ServiceResponse, error) {
	if sourceTaskPath == "" {
		sourceTaskPath = nodePath
	}
	options, err := childOptions(nodePath, attemptID, sourceTaskPath)
	if err != nil {
		return nil, err
	}
	return CallCommand(c, "meter", KindChild, &pb.MeterCommand{
		ChildOptions: options,
		MeterName:    meterName,
		MeterValue:   meterValue,
	}, Transport.RunCommandMeter)
}
