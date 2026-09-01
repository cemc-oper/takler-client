package cmd

import (
	"io"
	"testing"

	"github.com/cemc-oper/takler-client/common"
	"github.com/spf13/cobra"
)

// The three options must reach every subcommand, so they have to be registered
// as persistent flags rather than per command flags.
func TestGlobalFlagsAreInheritedBySubcommands(t *testing.T) {
	rootCmd := newCommandsBuilder().addAll().build().getCommand()

	names := []string{"tls-ca", "tls-server-name", "secret-file"}
	for _, name := range names {
		if rootCmd.PersistentFlags().Lookup(name) == nil {
			t.Errorf("root command has no persistent flag %q", name)
		}
	}

	subCommands := rootCmd.Commands()
	if len(subCommands) == 0 {
		t.Fatal("root command has no subcommands")
	}
	for _, subCommand := range subCommands {
		for _, name := range names {
			if subCommand.InheritedFlags().Lookup(name) == nil {
				t.Errorf("subcommand %q does not inherit flag %q", subCommand.Name(), name)
			}
		}
	}
}

// Parsing a subcommand's arguments must fill the option values, which is what
// makes them readable by the command implementation afterwards. The command
// tree is built locally here, both to keep the package level globalFlags out of
// the test and to avoid running a real command's connection code.
func TestGlobalFlagsAreParsedFromSubcommandArgs(t *testing.T) {
	options := &globalOptions{}

	rootCmd := &cobra.Command{Use: appCommand}
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	options.register(rootCmd)
	rootCmd.AddCommand(&cobra.Command{
		Use:  "ping",
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	})

	rootCmd.SetArgs([]string{
		"ping",
		"--tls-ca", "/etc/takler/ca.crt",
		"--tls-server-name", "login_a06",
		"--secret-file", "/etc/takler/secret",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got, want := options.tlsCaFile, "/etc/takler/ca.crt"; got != want {
		t.Errorf("tls ca file = %q, want %q", got, want)
	}
	if got, want := options.tlsServerName, "login_a06"; got != want {
		t.Errorf("tls server name = %q, want %q", got, want)
	}
	if got, want := options.secretFile, "/etc/takler/secret"; got != want {
		t.Errorf("secret file = %q, want %q", got, want)
	}
}

// Unset options are absent rather than a path of "", so that the next
// precedence level is used.
func TestSettingsOfUnsetOptionsAreEmpty(t *testing.T) {
	options := &globalOptions{}

	if got := options.tlsSettings(); got != (common.TLSSettings{}) {
		t.Errorf("tls settings = %+v, want zero", got)
	}
	if got := options.credentialSettings(); got != (common.CredentialSettings{}) {
		t.Errorf("credential settings = %+v, want zero", got)
	}
}

// The two levels newSecurityLevels collects are exactly what the common layer's
// resolvers take, and the connect config level comes from the security section.
func TestNewSecurityLevels(t *testing.T) {
	options := &globalOptions{
		tlsCaFile:     "/flag/ca.crt",
		tlsServerName: "flag_host",
		secretFile:    "/flag/secret",
	}
	config := &ConnectConfig{Security: Security{
		CaFile:             "/config/ca.crt",
		ServerName:         "config_host",
		OperatorSecretFile: "/config/secret",
	}}

	levels := newSecurityLevels(options, config)

	if got, want := levels.TLSFlags.CaFile, "/flag/ca.crt"; got != want {
		t.Errorf("flag ca file = %q, want %q", got, want)
	}
	if got, want := levels.TLSFlags.ServerName, "flag_host"; got != want {
		t.Errorf("flag server name = %q, want %q", got, want)
	}
	if got, want := levels.CredFlags.SecretFile, "/flag/secret"; got != want {
		t.Errorf("flag secret file = %q, want %q", got, want)
	}
	if got, want := levels.TLSConfig.CaFile, "/config/ca.crt"; got != want {
		t.Errorf("config ca file = %q, want %q", got, want)
	}
	if got, want := levels.TLSConfig.ServerName, "config_host"; got != want {
		t.Errorf("config server name = %q, want %q", got, want)
	}
	if got, want := levels.CredConfig.SecretFile, "/config/secret"; got != want {
		t.Errorf("config secret file = %q, want %q", got, want)
	}
}

// There may be no connect config file, in which case there is no config level.
func TestNewSecurityLevelsWithoutConnectConfig(t *testing.T) {
	levels := newSecurityLevels(&globalOptions{}, nil)

	if levels.TLSConfig != (common.TLSSettings{}) {
		t.Errorf("config tls settings = %+v, want zero", levels.TLSConfig)
	}
	if levels.CredConfig != (common.CredentialSettings{}) {
		t.Errorf("config credential settings = %+v, want zero", levels.CredConfig)
	}
}

// An option's value wins over both the environment variable and the connect
// config, which is the whole point of collecting the levels this way
// (requirements 13.4, 13.5, 13.11).
func TestOptionsTakePrecedenceOverEnvAndConnectConfig(t *testing.T) {
	t.Setenv(common.TaklerTlsCaFile, "/env/ca.crt")
	t.Setenv(common.TaklerTlsServerName, "env_host")
	t.Setenv(common.EnvSecretFile, "/env/secret")

	options := &globalOptions{
		tlsCaFile:     "/flag/ca.crt",
		tlsServerName: "flag_host",
		secretFile:    "/flag/secret",
	}
	config := &ConnectConfig{Security: Security{
		CaFile:             "/config/ca.crt",
		ServerName:         "config_host",
		OperatorSecretFile: "/config/secret",
	}}

	levels := newSecurityLevels(options, config)

	if got, want := common.ResolveCaFile(levels.TLSFlags, levels.TLSConfig), "/flag/ca.crt"; got != want {
		t.Errorf("resolved ca file = %q, want %q", got, want)
	}
	if got, want := common.ResolveServerName(levels.TLSFlags, levels.TLSConfig), "flag_host"; got != want {
		t.Errorf("resolved server name = %q, want %q", got, want)
	}
	if got, want := common.ResolveSecretFile(levels.CredFlags, levels.CredConfig), "/flag/secret"; got != want {
		t.Errorf("resolved secret file = %q, want %q", got, want)
	}
}

// Without the options the environment variable is used, and the connect config
// is the last level; this pins that cmd does not shadow either of them with an
// empty option value.
func TestEnvAndConnectConfigApplyWithoutOptions(t *testing.T) {
	t.Setenv(common.TaklerTlsCaFile, "/env/ca.crt")

	config := &ConnectConfig{Security: Security{
		CaFile:             "/config/ca.crt",
		ServerName:         "config_host",
		OperatorSecretFile: "/config/secret",
	}}

	levels := newSecurityLevels(&globalOptions{}, config)

	if got, want := common.ResolveCaFile(levels.TLSFlags, levels.TLSConfig), "/env/ca.crt"; got != want {
		t.Errorf("resolved ca file = %q, want %q", got, want)
	}
	if got, want := common.ResolveServerName(levels.TLSFlags, levels.TLSConfig), "config_host"; got != want {
		t.Errorf("resolved server name = %q, want %q", got, want)
	}
	if got, want := common.ResolveSecretFile(levels.CredFlags, levels.CredConfig), "/config/secret"; got != want {
		t.Errorf("resolved secret file = %q, want %q", got, want)
	}
}
