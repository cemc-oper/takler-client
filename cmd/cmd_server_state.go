package cmd

import (
	"fmt"

	"github.com/cemc-oper/takler-client/common"
	pb "github.com/cemc-oper/takler-client/takler_protocol"
	"github.com/spf13/cobra"
)

type serverStateCommand struct {
	BaseCommand
	host      string
	port      string
	operation string
}

func newServerStateCommand(operation string) *serverStateCommand {
	c := &serverStateCommand{operation: operation}
	help := map[string]string{
		"server-status": "[query] show service state and checkpoint recovery summary",
		"server-halt":   "[control] halt new server execution",
		"server-resume": "[control] remove manual and recovery halt causes",
	}
	c.cmd = &cobra.Command{
		Use:   operation,
		Short: help[operation],
		Args:  cobra.NoArgs,
		RunE:  c.runCommand,
	}
	c.cmd.Flags().StringVar(&c.host, "host", "", "takler service host")
	c.cmd.Flags().StringVar(&c.port, "port", "", "takler service port")
	return c
}

func newServerStatusCommand() *serverStateCommand { return newServerStateCommand("server-status") }
func newServerHaltCommand() *serverStateCommand   { return newServerStateCommand("server-halt") }
func newServerResumeCommand() *serverStateCommand { return newServerStateCommand("server-resume") }

func (c *serverStateCommand) runCommand(_ *cobra.Command, _ []string) error {
	client, err := newClient(c.host, c.port)
	if err != nil {
		return err
	}
	var response *pb.ServerStatusResponse
	switch c.operation {
	case "server-status":
		response, err = client.RunRequestServerStatus()
	case "server-halt":
		response, err = client.RunCommandServerHalt()
	case "server-resume":
		response, err = client.RunCommandServerResume()
	}
	if err != nil {
		return err
	}
	formatted, err := common.FormatServerStatus(response)
	if err != nil {
		return common.NewExitError(common.ExitServerError, err.Error())
	}
	fmt.Println(formatted)
	if response.GetFlag() != common.ErrorCodeSuccess {
		return common.NewExitError(
			common.ExitCodeForErrorCode(response.GetFlag()),
			fmt.Sprintf("%s: %s", common.ErrorName(response.GetFlag()), response.GetMessage()),
		)
	}
	return nil
}
