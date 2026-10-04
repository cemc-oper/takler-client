package common

import (
	"encoding/json"
	"fmt"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
)

// Server state commands share the same response and transport-independent call path.
func (c *TaklerServiceClient) RunRequestServerStatus() (*pb.ServerStatusResponse, error) {
	return CallCommand(c, "server-status", KindQuery, &pb.ServerStatusRequest{}, Transport.RunRequestServerStatus)
}

func (c *TaklerServiceClient) RunCommandServerHalt() (*pb.ServerStatusResponse, error) {
	return CallCommand(c, "server-halt", KindControl, &pb.ServerHaltCommand{}, Transport.RunCommandServerHalt)
}

func (c *TaklerServiceClient) RunCommandServerResume() (*pb.ServerStatusResponse, error) {
	return CallCommand(c, "server-resume", KindControl, &pb.ServerResumeCommand{}, Transport.RunCommandServerResume)
}

// FormatServerStatus prints the same snake_case status fields as the Python CLI.
func FormatServerStatus(response *pb.ServerStatusResponse) (string, error) {
	if response == nil {
		return "", fmt.Errorf("the server returned no response")
	}
	var restore any
	if response.RestoreSummary != nil {
		restore = response.RestoreSummary.AsMap()
	}
	var reason any
	if response.StatusReason != nil {
		reason = *response.StatusReason
	}
	var checkpoint any
	if response.LastCheckpointAt != nil {
		checkpoint = *response.LastCheckpointAt
	}
	causes := response.GetHaltCauses()
	if causes == nil {
		causes = []string{}
	}
	body, err := json.Marshal(map[string]any{
		"flag": response.GetFlag(), "message": response.GetMessage(),
		"status": response.GetStatus(), "status_reason": reason,
		"halt_causes": causes, "last_checkpoint_at": checkpoint,
		"restore_summary": restore,
	})
	return string(body), err
}
