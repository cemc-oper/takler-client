// The Control_Commands, i.e. the subcommands an operator invokes to change the
// server's state.
//
// Each RunE builds the client, makes one common call and reports the response:
// a zero flag prints the Error_Code classification name, a non zero one returns
// an *ExitError up to Execute (requirements 15.8, 15.9). NO_TAKLER is not
// consulted here, as it is defined for Child_Commands only (requirement 15.10):
// an operator typing "requeue" means it.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

/*********************************************
	requeue
 *********************************************/

type requeueCommand struct {
	BaseCommand

	host string
	port string
	//nodePaths []string
}

func newRequeueCommand() *requeueCommand {
	c := &requeueCommand{}
	requeueCmd := &cobra.Command{
		Use:   "requeue",
		Short: "[control] requeue given node(s).",
		Long:  "requeue given nodes",
		Args:  cobra.MinimumNArgs(1),
		RunE:  c.runCommand,
	}

	requeueCmd.Flags().StringVar(&c.host, "host", "", "takler service host")
	requeueCmd.Flags().StringVar(&c.port, "port", "", "takler service port")

	c.cmd = requeueCmd
	return c
}

func (mc *requeueCommand) runCommand(cmd *cobra.Command, args []string) error {
	client, err := newClient(mc.host, mc.port)
	if err != nil {
		return err
	}

	nodePaths := args
	fmt.Printf("%s:%s requeue: %s\n", client.Host, client.Port, nodePaths)

	response, err := client.RunCommandRequeue(nodePaths)
	if err != nil {
		return err
	}

	return reportCommandResponse(response)
}

/*********************************************
	suspend
 *********************************************/

type suspendCommand struct {
	BaseCommand

	host string
	port string
	//nodePaths []string
}

func newSuspendCommand() *suspendCommand {
	c := &suspendCommand{}
	suspendCmd := &cobra.Command{
		Use:   "suspend",
		Short: "[control] suspend given node(s). prevent job creation for the node and all its children nodes.",
		Long:  "suspend given nodes",
		Args:  cobra.MinimumNArgs(1),
		RunE:  c.runCommand,
	}

	suspendCmd.Flags().StringVar(&c.host, "host", "", "takler service host")
	suspendCmd.Flags().StringVar(&c.port, "port", "", "takler service port")

	c.cmd = suspendCmd
	return c
}

func (mc *suspendCommand) runCommand(cmd *cobra.Command, args []string) error {
	client, err := newClient(mc.host, mc.port)
	if err != nil {
		return err
	}

	nodePaths := args
	fmt.Printf("%s:%s suspend: %s\n", client.Host, client.Port, nodePaths)

	response, err := client.RunCommandSuspend(nodePaths)
	if err != nil {
		return err
	}

	return reportCommandResponse(response)
}

/*********************************************
	resume
 *********************************************/

type resumeCommand struct {
	BaseCommand

	host string
	port string
	//nodePaths []string
}

func newResumeCommand() *resumeCommand {
	c := &resumeCommand{}
	resumeCmd := &cobra.Command{
		Use:   "resume",
		Short: "[control] resume the node(s) from suspended status.",
		Long:  "resume given nodes",
		Args:  cobra.MinimumNArgs(1),
		RunE:  c.runCommand,
	}

	resumeCmd.Flags().StringVar(&c.host, "host", "", "takler service host")
	resumeCmd.Flags().StringVar(&c.port, "port", "", "takler service port")

	c.cmd = resumeCmd
	return c
}

func (mc *resumeCommand) runCommand(cmd *cobra.Command, args []string) error {
	client, err := newClient(mc.host, mc.port)
	if err != nil {
		return err
	}

	nodePaths := args
	fmt.Printf("%s:%s resume: %s\n", client.Host, client.Port, nodePaths)

	response, err := client.RunCommandResume(nodePaths)
	if err != nil {
		return err
	}

	return reportCommandResponse(response)
}

/*********************************************
	run
 *********************************************/

type runCommand struct {
	BaseCommand

	host  string
	port  string
	force bool
	//nodePaths []string
}

func newRunCommand() *runCommand {
	c := &runCommand{}
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "[control] run the task.",
		Long:  "run the tasks, ignore triggers",
		Args:  cobra.MinimumNArgs(1),
		RunE:  c.runCommand,
	}

	runCmd.Flags().StringVar(&c.host, "host", "", "takler service host")
	runCmd.Flags().StringVar(&c.port, "port", "", "takler service port")
	runCmd.Flags().BoolVar(&c.force, "force", false, "force run")

	c.cmd = runCmd
	return c
}

func (mc *runCommand) runCommand(cmd *cobra.Command, args []string) error {
	client, err := newClient(mc.host, mc.port)
	if err != nil {
		return err
	}

	nodePaths := args
	fmt.Printf("%s:%s run: %s\n", client.Host, client.Port, nodePaths)

	response, err := client.RunCommandRun(nodePaths, mc.force)
	if err != nil {
		return err
	}

	return reportCommandResponse(response)
}
