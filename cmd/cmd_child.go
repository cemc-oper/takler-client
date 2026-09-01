// The five Child_Commands, i.e. the subcommands a job script invokes.
//
// Every RunE here has the same shape: the NO_TAKLER short circuit first, then
// the client, then one common call, then the response. The short circuit comes
// before anything else on purpose (requirement 15.10): no connect config is
// read, no connection is made, and the process ends with exit code 0.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

type ChildCommand struct {
	BaseCommand

	childOptions struct {
		host     string
		port     string
		nodePath string
	}
}

// reportIgnored prints the line a short circuited child command leaves behind,
// so a job log shows why nothing happened.
func reportIgnored() {
	fmt.Printf("ignore because %s is set.\n", NoTakler)
}

/*********************************************
	init
 *********************************************/

type initCommand struct {
	ChildCommand

	taskId string
}

func newInitCommand() *initCommand {
	c := &initCommand{}
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "[child] init the task.",
		Long:  "mark task to active",
		RunE:  c.runCommand,
	}

	initCmd.Flags().StringVar(&c.childOptions.host, "host", "", "takler service host")
	initCmd.Flags().StringVar(&c.childOptions.port, "port", "", "takler service port")
	initCmd.Flags().StringVar(&c.childOptions.nodePath, "node-path", "", "node path")
	initCmd.Flags().StringVar(&c.taskId, "task-id", "", "task id")
	initCmd.MarkFlagRequired("task-id")

	c.cmd = initCmd
	return c
}

func (mc *initCommand) runCommand(cmd *cobra.Command, args []string) error {
	if noTaklerIsSet() {
		reportIgnored()
		return nil
	}

	client, err := newClient(mc.childOptions.host, mc.childOptions.port)
	if err != nil {
		return err
	}

	nodePath := getNodePath(mc.childOptions.nodePath)
	taskId := mc.taskId
	fmt.Printf("%s:%s init %s with %s\n", client.Host, client.Port, nodePath, taskId)

	response, err := client.RunCommandInit(nodePath, taskId)
	if err != nil {
		return err
	}

	return reportCommandResponse(response)
}

/*********************************************
	complete
 *********************************************/

type completeCommand struct {
	ChildCommand
}

func newCompleteCommand() *completeCommand {
	c := &completeCommand{}
	completeCmd := &cobra.Command{
		Use:   "complete",
		Short: "[child] complete the task.",
		Long:  "mark task to complete",
		RunE:  c.runCommand,
	}

	completeCmd.Flags().StringVar(&c.childOptions.host, "host", "", "takler service host")
	completeCmd.Flags().StringVar(&c.childOptions.port, "port", "", "takler service port")
	completeCmd.Flags().StringVar(&c.childOptions.nodePath, "node-path", "", "node path")

	c.cmd = completeCmd
	return c
}

func (mc *completeCommand) runCommand(cmd *cobra.Command, args []string) error {
	if noTaklerIsSet() {
		reportIgnored()
		return nil
	}

	client, err := newClient(mc.childOptions.host, mc.childOptions.port)
	if err != nil {
		return err
	}

	nodePath := getNodePath(mc.childOptions.nodePath)
	fmt.Printf("%s:%s complete %s\n", client.Host, client.Port, nodePath)

	response, err := client.RunCommandComplete(nodePath)
	if err != nil {
		return err
	}

	return reportCommandResponse(response)
}

/*********************************************
	abort
 *********************************************/

type abortCommand struct {
	ChildCommand

	reason string
}

func newAbortCommand() *abortCommand {
	c := &abortCommand{}
	abortCmd := &cobra.Command{
		Use:   "abort",
		Short: "[child] abort the task.",
		Long:  "mark task to aborted",
		RunE:  c.runCommand,
	}

	abortCmd.Flags().StringVar(&c.childOptions.host, "host", "", "takler service host")
	abortCmd.Flags().StringVar(&c.childOptions.port, "port", "", "takler service port")
	abortCmd.Flags().StringVar(&c.childOptions.nodePath, "node-path", "", "node path")
	abortCmd.Flags().StringVar(&c.reason, "reason", "", "abort reason")

	c.cmd = abortCmd
	return c
}

func (mc *abortCommand) runCommand(cmd *cobra.Command, args []string) error {
	if noTaklerIsSet() {
		reportIgnored()
		return nil
	}

	client, err := newClient(mc.childOptions.host, mc.childOptions.port)
	if err != nil {
		return err
	}

	nodePath := getNodePath(mc.childOptions.nodePath)
	reason := mc.reason
	fmt.Printf("%s:%s abort %s: %s\n", client.Host, client.Port, nodePath, reason)

	response, err := client.RunCommandAbort(nodePath, reason)
	if err != nil {
		return err
	}

	return reportCommandResponse(response)
}

/*********************************************
	event
 *********************************************/

type eventCommand struct {
	ChildCommand

	eventName string
}

func newEventCommand() *eventCommand {
	c := &eventCommand{}
	eventCmd := &cobra.Command{
		Use:   "event",
		Short: "[child] change Event.",
		Long:  "change event",
		RunE:  c.runCommand,
	}

	eventCmd.Flags().StringVar(&c.childOptions.host, "host", "", "takler service host")
	eventCmd.Flags().StringVar(&c.childOptions.port, "port", "", "takler service port")
	eventCmd.Flags().StringVar(&c.childOptions.nodePath, "node-path", "", "node path")
	eventCmd.Flags().StringVar(&c.eventName, "event-name", "", "event name")
	eventCmd.MarkFlagRequired("event-name")

	c.cmd = eventCmd
	return c
}

func (mc *eventCommand) runCommand(cmd *cobra.Command, args []string) error {
	if noTaklerIsSet() {
		reportIgnored()
		return nil
	}

	client, err := newClient(mc.childOptions.host, mc.childOptions.port)
	if err != nil {
		return err
	}

	nodePath := getNodePath(mc.childOptions.nodePath)
	eventName := mc.eventName
	fmt.Printf("%s:%s event %s: %s\n", client.Host, client.Port, nodePath, eventName)

	response, err := client.RunCommandEvent(nodePath, eventName)
	if err != nil {
		return err
	}

	return reportCommandResponse(response)
}

/*********************************************
	meter
 *********************************************/

type meterCommand struct {
	ChildCommand

	meterName  string
	meterValue string
}

func newMeterCommand() *meterCommand {
	c := &meterCommand{}
	meterCmd := &cobra.Command{
		Use:   "meter",
		Short: "[child] change Meter.",
		Long:  "change meter",
		RunE:  c.runCommand,
	}

	meterCmd.Flags().StringVar(&c.childOptions.host, "host", "", "takler service host")
	meterCmd.Flags().StringVar(&c.childOptions.port, "port", "", "takler service port")
	meterCmd.Flags().StringVar(&c.childOptions.nodePath, "node-path", "", "node path")
	meterCmd.Flags().StringVar(&c.meterName, "meter-name", "", "meter name")
	meterCmd.Flags().StringVar(&c.meterValue, "meter-value", "", "meter value")
	meterCmd.MarkFlagRequired("meter-name")
	meterCmd.MarkFlagRequired("meter-value")

	c.cmd = meterCmd
	return c
}

func (mc *meterCommand) runCommand(cmd *cobra.Command, args []string) error {
	if noTaklerIsSet() {
		reportIgnored()
		return nil
	}

	client, err := newClient(mc.childOptions.host, mc.childOptions.port)
	if err != nil {
		return err
	}

	nodePath := getNodePath(mc.childOptions.nodePath)
	meterName := mc.meterName
	meterValue := mc.meterValue
	fmt.Printf(
		"%s:%s meter %s: %s with %s\n",
		client.Host, client.Port, nodePath, meterName, meterValue,
	)

	response, err := client.RunCommandMeter(nodePath, meterName, meterValue)
	if err != nil {
		return err
	}

	return reportCommandResponse(response)
}
