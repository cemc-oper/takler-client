package cmd

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/cemc-oper/takler-client/common"
	"github.com/spf13/cobra"
)

type snapshotCommand struct {
	BaseCommand
	host, port, scope, fields string
	depth                     int
	stream                    bool
}

func newSnapshotCommand() *snapshotCommand {
	c := &snapshotCommand{}
	command := &cobra.Command{Use: "snapshot", Short: "[query] read a fixed full-detail snapshot", RunE: c.runCommand}
	command.Flags().StringVar(&c.host, "host", "", "takler service host")
	command.Flags().StringVar(&c.port, "port", "", "takler service port")
	command.Flags().StringVar(&c.scope, "scope", "/", "subtree path")
	command.Flags().IntVar(&c.depth, "depth", 0, "relative tree depth")
	command.Flags().StringVar(&c.fields, "fields", "summary,definition,parameters,runtime,artifacts,service", "comma-separated public field groups")
	command.Flags().BoolVar(&c.stream, "stream", false, "write JSON lines with final completion")
	c.cmd = command
	return c
}
func (mc *snapshotCommand) runCommand(cmd *cobra.Command, _ []string) error {
	client, err := newClient(mc.host, mc.port)
	if err != nil {
		return err
	}
	selection := common.FullSnapshotSelection{ScopePath: mc.scope, FieldGroups: strings.Split(mc.fields, ",")}
	if cmd != nil && cmd.Flags().Changed("depth") {
		selection.Depth = &mc.depth
	}
	out := json.NewEncoder(os.Stdout)
	out.SetEscapeHTML(false)
	if mc.stream {
		complete, err := client.IterFullSnapshot(selection, func(node map[string]any) error { return out.Encode(map[string]any{"node": node}) })
		if err != nil {
			return err
		}
		return out.Encode(map[string]any{"complete": complete})
	}
	result, err := client.ReadFullSnapshot(selection)
	if err != nil {
		return err
	}
	return out.Encode(map[string]any{"completion": result.Complete, "nodes_by_path": result.NodesByPath})
}
