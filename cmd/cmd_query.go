// The Query_Commands, i.e. the subcommands that only read.
//
// Unlike a command response, a query response carries no flag, so there is
// nothing to classify: the common method prints the payload the operator asked
// for and a failure comes back as an *ExitError that RunE hands to Execute
// (requirement 15.9).
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

/*
********************************************

	show

********************************************
*/
type showCommand struct {
	BaseCommand

	host string
	port string

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
		Short: "[query] print bunch tree.",
		Long:  "print state of all flows in server",
		RunE:  c.runCommand,
	}

	showCmd.Flags().StringVar(&c.host, "host", "", "takler service host")
	showCmd.Flags().StringVar(&c.port, "port", "", "takler service port")
	showCmd.Flags().BoolVar(&c.showTrigger, "show-trigger", false, "show trigger")
	showCmd.Flags().BoolVar(&c.showParameter, "show-parameter", false, "show parameters")
	showCmd.Flags().BoolVar(&c.showLimit, "show-limit", true, "show limits")
	showCmd.Flags().BoolVar(&c.showEvent, "show-event", true, "show events")
	showCmd.Flags().BoolVar(&c.showMeter, "show-meter", true, "show meters")
	showCmd.Flags().BoolVar(&c.showAll, "show-all", false, "show all items, ignore other show options")

	c.cmd = showCmd
	return c
}

func (mc *showCommand) runCommand(cmd *cobra.Command, args []string) error {
	client, err := newClient(mc.host, mc.port)
	if err != nil {
		return err
	}

	fmt.Printf("%s:%s show\n", client.Host, client.Port)

	if mc.showAll {
		mc.showTrigger = true
		mc.showParameter = true
		mc.showLimit = true
		mc.showEvent = true
		mc.showMeter = true
	}

	_, err = client.RunQueryShow(
		mc.showTrigger,
		mc.showParameter,
		mc.showLimit,
		mc.showEvent,
		mc.showMeter)
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
		Short: "[query] check the server is running with given host and hort.",
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
