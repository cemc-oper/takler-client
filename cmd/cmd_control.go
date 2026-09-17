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

/*********************************************
	force
 *********************************************/

type forceCommand struct {
	BaseCommand

	host      string
	port      string
	recursive bool
}

func newForceCommand() *forceCommand {
	c := &forceCommand{}
	forceCmd := &cobra.Command{
		Use:   "force state path...",
		Short: "[control] change the node's state force, ignore whatever state it is now.",
		Long:  "force given nodes to the given state",
		Args:  cobra.MinimumNArgs(2),
		RunE:  c.runCommand,
	}

	forceCmd.Flags().StringVar(&c.host, "host", "", "takler service host")
	forceCmd.Flags().StringVar(&c.port, "port", "", "takler service port")
	forceCmd.Flags().BoolVar(&c.recursive, "recursive", true, "recursive")

	c.cmd = forceCmd
	return c
}

func (mc *forceCommand) runCommand(cmd *cobra.Command, args []string) error {
	client, err := newClient(mc.host, mc.port)
	if err != nil {
		return err
	}

	state := args[0]
	nodePaths := args[1:]
	fmt.Printf("%s:%s force: %s %s\n", client.Host, client.Port, state, nodePaths)

	response, err := client.RunCommandForce(nodePaths, state, mc.recursive)
	if err != nil {
		return err
	}

	return reportCommandResponse(response)
}

/*********************************************
	free-dep
 *********************************************/

type freeDepCommand struct {
	BaseCommand

	host    string
	port    string
	depType string
}

func newFreeDepCommand() *freeDepCommand {
	c := &freeDepCommand{}
	freeDepCmd := &cobra.Command{
		Use:   "free-dep path...",
		Short: "[control] free dependencies for the node(s).",
		Long:  "free dependencies for given nodes",
		Args:  cobra.MinimumNArgs(1),
		RunE:  c.runCommand,
	}

	freeDepCmd.Flags().StringVar(&c.host, "host", "", "takler service host")
	freeDepCmd.Flags().StringVar(&c.port, "port", "", "takler service port")
	freeDepCmd.Flags().StringVar(&c.depType, "dep-type", "all", "dependency type, [all, time, trigger]")

	c.cmd = freeDepCmd
	return c
}

func (mc *freeDepCommand) runCommand(cmd *cobra.Command, args []string) error {
	client, err := newClient(mc.host, mc.port)
	if err != nil {
		return err
	}

	nodePaths := args
	fmt.Printf("%s:%s free-dep: %s %s\n", client.Host, client.Port, mc.depType, nodePaths)

	response, err := client.RunCommandFreeDep(nodePaths, mc.depType)
	if err != nil {
		return err
	}

	return reportCommandResponse(response)
}

/*********************************************
	load
 *********************************************/

type loadCommand struct {
	BaseCommand

	host     string
	port     string
	flowType string
}

func newLoadCommand() *loadCommand {
	c := &loadCommand{}
	loadCmd := &cobra.Command{
		Use:   "load flow_file_path",
		Short: "[control] load flow from file to server.",
		Long:  "load flow from file to server",
		Args:  cobra.ExactArgs(1),
		RunE:  c.runCommand,
	}

	loadCmd.Flags().StringVar(&c.host, "host", "", "takler service host")
	loadCmd.Flags().StringVar(&c.port, "port", "", "takler service port")
	loadCmd.Flags().StringVar(&c.flowType, "flow-type", "json", "flow file type, [json]")

	c.cmd = loadCmd
	return c
}

func (mc *loadCommand) runCommand(cmd *cobra.Command, args []string) error {
	client, err := newClient(mc.host, mc.port)
	if err != nil {
		return err
	}

	flowFilePath := args[0]
	fmt.Printf("%s:%s load: %s\n", client.Host, client.Port, flowFilePath)

	response, err := client.RunCommandLoad(mc.flowType, flowFilePath)
	if err != nil {
		return err
	}

	return reportCommandResponse(response)
}

/*********************************************
	begin
 *********************************************/

type beginCommand struct {
	BaseCommand

	host  string
	port  string
	force bool
}

func newBeginCommand() *beginCommand {
	c := &beginCommand{}
	beginCmd := &cobra.Command{
		Use:   "begin [flow_name]",
		Short: "[control] begin the flow(s): start the calendar and reset the node tree.",
		Long:  "begin the named flow, or every flow when FLOW_NAME is omitted",
		Args:  cobra.MaximumNArgs(1),
		RunE:  c.runCommand,
	}

	beginCmd.Flags().StringVar(&c.host, "host", "", "takler service host")
	beginCmd.Flags().StringVar(&c.port, "port", "", "takler service port")
	beginCmd.Flags().BoolVar(&c.force, "force", false, "begin an already begun flow again")

	c.cmd = beginCmd
	return c
}

func (mc *beginCommand) runCommand(cmd *cobra.Command, args []string) error {
	client, err := newClient(mc.host, mc.port)
	if err != nil {
		return err
	}

	// An omitted flow name is the empty string, which the protocol reads as
	// "all flows" -- the same convention the Python CLI documents.
	flowName := ""
	if len(args) > 0 {
		flowName = args[0]
	}
	fmt.Printf("%s:%s begin: %s\n", client.Host, client.Port, flowName)

	response, err := client.RunCommandBegin(flowName, mc.force)
	if err != nil {
		return err
	}

	return reportCommandResponse(response)
}
