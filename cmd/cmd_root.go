package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

const appCommand = "takler_client"

type commandsBuilder struct {
	commands    []Command
	rootCommand *cobra.Command
}

func (b *commandsBuilder) getCommand() *cobra.Command {
	return b.rootCommand
}

func (b *commandsBuilder) addCommands(commands ...Command) *commandsBuilder {
	b.commands = append(b.commands, commands...)
	return b
}

func (b *commandsBuilder) addAll() *commandsBuilder {
	b.addCommands(
		// child
		newInitCommand(),
		newCompleteCommand(),
		newAbortCommand(),
		newEventCommand(),
		newMeterCommand(),

		// control
		newRequeueCommand(),
		newSuspendCommand(),
		newResumeCommand(),
		newRunCommand(),

		// query
		newShowCommand(),
		newPingCommand(),
	)
	return b
}

func (b *commandsBuilder) build() *commandsBuilder {
	for _, command := range b.commands {
		b.rootCommand.AddCommand(command.getCommand())
	}
	return b
}

func newCommandsBuilder() *commandsBuilder {
	rootCommand := &cobra.Command{
		Use:   appCommand,
		Short: "A CLI client for Takler.",
		Long:  "A CLI client for Takler.",
		Run: func(cmd *cobra.Command, args []string) {
		},
	}

	// The TLS and credential options are persistent flags of the root command,
	// so every subcommand accepts them (requirements 13.4, 13.5, 13.11).
	globalFlags.register(rootCommand)

	return &commandsBuilder{
		rootCommand: rootCommand,
	}
}

func Execute() {
	consumerCommand := newCommandsBuilder().addAll().build()
	rootCmd := consumerCommand.getCommand()

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

type Command interface {
	getCommand() *cobra.Command
}

type BaseCommand struct {
	cmd *cobra.Command
}

func (c *BaseCommand) getCommand() *cobra.Command {
	return c.cmd
}
