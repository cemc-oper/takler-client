package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/perillaroc/takler-client/common"
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

		// cobra would print a returned error itself and, for a run time
		// failure, follow it with the whole usage text. Both are noise in a job
		// script's log: Execute writes exactly one line to standard error.
		// Setting this on the root command covers every subcommand.
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	// The TLS and credential options are persistent flags of the root command,
	// so every subcommand accepts them (requirements 13.4, 13.5, 13.11).
	globalFlags.register(rootCommand)

	return &commandsBuilder{
		rootCommand: rootCommand,
	}
}

// Execute runs the root command and is the process' single exit point
// (requirement 15.9). Nothing below it calls os.Exit or log.Fatalf: a command's
// RunE returns an *common.ExitError, and the exit code lives in that error.
func Execute() {
	consumerCommand := newCommandsBuilder().addAll().build()
	rootCmd := consumerCommand.getCommand()

	if err := rootCmd.Execute(); err != nil {
		code, message := exitStatusForError(err)
		fmt.Fprintln(os.Stderr, message)
		os.Exit(code)
	}
}

// exitStatusForError turns the error a command returned into the process' exit
// code and the single line to write to standard error (requirements 15.1 ~
// 15.5). It is separate from Execute only so it can be tested without ending
// the test process.
func exitStatusForError(err error) (int, string) {
	var exitErr *common.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code, exitErr.Message
	}

	// Anything that is not an *ExitError did not come from the command path (a
	// cobra argument or flag error, for instance). Report it with the most
	// conservative failure code, the same reading unregistered Error_Codes get
	// (requirement 15.3).
	return common.ExitServerError, err.Error()
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
