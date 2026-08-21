// Global command line options of the TLS and credential settings.
//
// The three options are registered as persistent flags on the root command, so
// every subcommand accepts them without repeating the registration, and they
// are read back as the command line options level of the precedence chains the
// common layer resolves (requirements 13.4, 13.5, 13.11).
//
// No precedence logic lives here: common.ResolveCaFile, ResolveServerName and
// ResolveSecretFile already fall back to the environment variables and then to
// the connect config's security section. This file only fills the two levels
// they take as arguments.
package cmd

import (
	"github.com/perillaroc/takler-client/common"
	"github.com/spf13/cobra"
)

// globalOptions holds the values of the persistent flags.
//
// A command implementation cannot be handed these through its constructor, as
// the constructors take no arguments and cobra binds a flag to an address at
// registration time, so the values live in the package level globalFlags that
// newCommandsBuilder binds the root command's persistent flags to.
type globalOptions struct {
	tlsCaFile     string
	tlsServerName string
	secretFile    string
}

// globalFlags holds the values of the root command's persistent flags. It is
// written by cobra during flag parsing and read by the command implementations
// after that.
var globalFlags = &globalOptions{}

// register declares the three options as persistent flags of cmd, which makes
// them available to cmd itself and to all of its subcommands.
func (o *globalOptions) register(cmd *cobra.Command) {
	flags := cmd.PersistentFlags()
	flags.StringVar(&o.tlsCaFile, "tls-ca", "",
		"CA certificate file to trust as the root of trust (overrides "+common.TaklerTlsCaFile+")")
	flags.StringVar(&o.tlsServerName, "tls-server-name", "",
		"name to verify the server certificate against (overrides "+common.TaklerTlsServerName+")")
	flags.StringVar(&o.secretFile, "secret-file", "",
		"operator secret file to authenticate with (overrides "+common.EnvSecretFile+")")
}

// tlsSettings returns the command line options level of the TLS settings.
func (o *globalOptions) tlsSettings() common.TLSSettings {
	if o == nil {
		return common.TLSSettings{}
	}
	return common.TLSSettings{
		CaFile:     o.tlsCaFile,
		ServerName: o.tlsServerName,
	}
}

// credentialSettings returns the command line options level of the credential
// settings.
func (o *globalOptions) credentialSettings() common.CredentialSettings {
	if o == nil {
		return common.CredentialSettings{}
	}
	return common.CredentialSettings{SecretFile: o.secretFile}
}

// securityLevels is the pair of precedence levels the cmd layer supplies to the
// common layer: the command line options and the connect config's security
// section. The environment variable level sits between them and is read inside
// common, so it does not appear here.
type securityLevels struct {
	TLSFlags   common.TLSSettings
	TLSConfig  common.TLSSettings
	CredFlags  common.CredentialSettings
	CredConfig common.CredentialSettings
}

// newSecurityLevels collects the two levels from the parsed options and the
// loaded connect config. A nil config, which is what loadConnectConfig returns
// when there is no connect config file to load, contributes empty values.
func newSecurityLevels(options *globalOptions, config *ConnectConfig) securityLevels {
	return securityLevels{
		TLSFlags: options.tlsSettings(),
		TLSConfig: common.TLSSettings{
			CaFile:     config.GetCaFile(),
			ServerName: config.GetServerName(),
		},
		CredFlags:  options.credentialSettings(),
		CredConfig: common.CredentialSettings{SecretFile: config.GetOperatorSecretFile()},
	}
}
