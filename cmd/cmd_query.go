// The Query_Commands, i.e. the subcommands that only read.
//
// Unlike a command response, a query response carries no flag, so there is
// nothing to classify: the common method prints the payload the operator asked
// for and a failure comes back as an *ExitError that RunE hands to Execute
// (requirement 15.9).
package cmd

import (
	"fmt"
	"os"

	"github.com/cemc-oper/takler-client/common"
	"github.com/spf13/cobra"
)

/*
********************************************

	show

********************************************
*/
type showCommand struct {
	BaseCommand

	host  string
	port  string
	scope string
	flow  string
	depth int

	showParameter bool
	showTrigger   bool
	showLimit     bool
	showEvent     bool
	showMeter     bool
	showAll       bool
}

func newShowCommand() *showCommand {
	c := &showCommand{}
	showCmd := &cobra.Command{
		Use:   "show",
		Short: "[query] print a compact workflow summary",
		Long:  "read a complete QueryDocument v1 summary in bounded pages. Scope, flow and depth limit the tree; detail flags sample selected nodes live (at most 4096). A changing state can reject a large capture; retry or narrow the scope. Incremental since queries are not supported.",
		RunE:  c.runCommand,
	}

	showCmd.Flags().StringVar(&c.host, "host", "", "takler service host")
	showCmd.Flags().StringVar(&c.port, "port", "", "takler service port")
	showCmd.Flags().StringVar(&c.scope, "scope", "", "subtree path")
	showCmd.Flags().StringVar(&c.flow, "flow", "", "flow name")
	showCmd.Flags().IntVar(&c.depth, "depth", 0, "relative tree depth")
	showCmd.Flags().BoolVar(&c.showTrigger, "show-trigger", false, "show trigger")
	showCmd.Flags().BoolVar(&c.showParameter, "show-parameter", false, "show parameters")
	showCmd.Flags().BoolVar(&c.showLimit, "show-limit", false, "show limits")
	showCmd.Flags().BoolVar(&c.showEvent, "show-event", false, "show events")
	showCmd.Flags().BoolVar(&c.showMeter, "show-meter", false, "show meters")
	showCmd.Flags().BoolVar(&c.showAll, "show-all", false, "show all items, ignore other show options")

	c.cmd = showCmd
	return c
}

func (mc *showCommand) runCommand(cmd *cobra.Command, args []string) error {
	client, err := newClient(mc.host, mc.port)
	if err != nil {
		return err
	}

	if mc.showAll {
		mc.showTrigger = true
		mc.showParameter = true
		mc.showLimit = true
		mc.showEvent = true
		mc.showMeter = true
	}

	selection := common.QuerySelection{ScopePath: mc.scope, FlowName: mc.flow}
	if cmd != nil && cmd.Flags().Changed("depth") {
		selection.Depth = &mc.depth
	}
	_, err = client.RunInitialQueryShow(selection, common.QueryShowOptions{
		Trigger: mc.showTrigger, Parameter: mc.showParameter,
		Limit: mc.showLimit, Event: mc.showEvent, Meter: mc.showMeter,
	}, os.Stdout)
	return err
}

/*
********************************************

	ping

********************************************
*/
type pingCommand struct {
	BaseCommand

	host string
	port string
}

func newPingCommand() *pingCommand {
	c := &pingCommand{}
	pingCmd := &cobra.Command{
		Use:   "ping",
		Short: "[query] check the server is running with given host and port.",
		Long:  "ping server",
		RunE:  c.runCommand,
	}

	pingCmd.Flags().StringVar(&c.host, "host", "", "takler service host")
	pingCmd.Flags().StringVar(&c.port, "port", "", "takler service port")

	c.cmd = pingCmd
	return c
}

func (mc *pingCommand) runCommand(cmd *cobra.Command, args []string) error {
	client, err := newClient(mc.host, mc.port)
	if err != nil {
		return err
	}

	fmt.Printf("%s:%s ping\n", client.Host, client.Port)

	_, err = client.RunQueryPing()
	return err
}

/*
********************************************

	coroutine

********************************************
*/
type coroutineCommand struct {
	BaseCommand

	host string
	port string
}

func newCoroutineCommand() *coroutineCommand {
	c := &coroutineCommand{}
	coroutineCmd := &cobra.Command{
		Use:   "coroutine",
		Short: "[query] print current coroutine in server. for debug.",
		Long:  "print the coroutines the server is running",
		RunE:  c.runCommand,
	}

	coroutineCmd.Flags().StringVar(&c.host, "host", "", "takler service host")
	coroutineCmd.Flags().StringVar(&c.port, "port", "", "takler service port")

	c.cmd = coroutineCmd
	return c
}

func (mc *coroutineCommand) runCommand(cmd *cobra.Command, args []string) error {
	client, err := newClient(mc.host, mc.port)
	if err != nil {
		return err
	}

	fmt.Printf("%s:%s coroutine\n", client.Host, client.Port)

	_, err = client.RunQueryCoroutine()
	return err
}
