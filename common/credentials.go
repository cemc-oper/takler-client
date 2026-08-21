// Credential resolution and gRPC metadata assembly for the Go client.
//
// This file holds everything the client needs to answer "which credentials does
// this call carry", deliberately separated from the RPC plumbing: the
// Call_Wrapper asks BuildMetadata once per call and attaches the result with
// metadata.NewOutgoingContext, so no command implementation contains credential
// code (requirement 13.7).
//
// The metadata keys, the environment variable names and the per command kind
// classification are the cross-language contract shared with the Python
// client's takler/client/credentials.py; any change here must be applied there
// as well.
//
// Requirements: 13.8, 13.9, 13.10, 13.11, 13.12, 13.13.
package common

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"

	"google.golang.org/grpc/metadata"
)

// Environment variables carrying the client's credentials (requirements 13.8,
// 13.11). The names are shared with the Python client, so one job script can
// export them once and call either client.
const (
	// EnvJobPassword holds the Job_Password of the running job, exported by the
	// job script from the TAKLER_PASS generated parameter.
	EnvJobPassword = "TAKLER_PASS"

	// EnvSecretFile holds the path of the Operator_Secret_File.
	EnvSecretFile = "TAKLER_SECRET_FILE"
)

// gRPC metadata keys of the Credential_Metadata. All lowercase as the gRPC
// specification requires, and none ends in "-bin" because all three values are
// ASCII text.
const (
	// MetadataPass carries the Job_Password of a Child_Command.
	MetadataPass = "takler-pass"

	// MetadataSecret carries the Operator_Secret of an Operator_Command.
	MetadataSecret = "takler-secret"

	// MetadataUser carries the caller's OS user name of an Operator_Command.
	MetadataUser = "takler-user"
)

// CredentialSettings is one precedence level's worth of credential inputs: the
// path of the Operator_Secret_File, which may be absent (an empty or
// whitespace-only string).
//
// Like TLSSettings it exists so that the connect config's value can reach this
// package as a plain argument: common must not import cmd, which parses the
// connect config and already imports common. The cmd layer fills one
// CredentialSettings from the command line options and one from the connect
// config's security section, and passes both in.
type CredentialSettings struct {
	SecretFile string
}

// ResolveSecretFile resolves the Operator_Secret_File path in the order
// "command line option > TAKLER_SECRET_FILE > the connect config's
// security.operator_secret_file > no shared secret" (requirement 13.11). An
// empty result means no secret file is configured.
func ResolveSecretFile(flags CredentialSettings, config CredentialSettings) string {
	return resolveWithEnv(flags.SecretFile, EnvSecretFile, config.SecretFile)
}

// Credentials builds the Credential_Metadata of a call from the two
// configuration levels the cmd layer supplies. A nil *Credentials is usable and
// behaves like one with both levels empty, i.e. the secret file path is then
// taken from the environment alone.
type Credentials struct {
	flags  CredentialSettings
	config CredentialSettings

	// warn is where the "configured secret file holds no secret" line goes.
	// nil means os.Stderr.
	warn io.Writer
}

// NewCredentials returns the Credentials of the command line options level and
// the connect config level.
func NewCredentials(flags CredentialSettings, config CredentialSettings) *Credentials {
	return &Credentials{flags: flags, config: config}
}

// BuildMetadata returns the Credential_Metadata a call of kind carries.
//
// A Child_Command carries takler-pass taken from TAKLER_PASS (requirement
// 13.8). Everything else is an Operator_Command, which carries takler-user and,
// when a secret file is configured and holds a secret, takler-secret
// (requirements 13.9, 13.10). KindControl and KindQuery are both Operator: ping
// is a query and so carries credentials it does not need, but the server does
// not check credentials on a PUBLIC method, and the redundancy saves the client
// a second per method classification table.
//
// A credential that is not configured is simply left out and the call goes
// ahead, letting the server decide whether to refuse it. The alternative,
// failing in the client, would break a client that talks to a server running
// with Auth_Mode=disabled.
//
// The one hard failure is an unreadable secret file: the operator asked for a
// specific file and the client cannot honour that, so it returns an *ExitError
// carrying ExitRequestError with a single line naming the path and the reason
// (requirement 13.12).
//
// No returned metadata is ever logged here, and neither the returned error nor
// the warning line contains a credential value (requirement 13.13).
func (c *Credentials) BuildMetadata(kind CommandKind) (metadata.MD, error) {
	md := metadata.MD{}

	if kind == KindChild {
		if password := os.Getenv(EnvJobPassword); !isBlank(password) {
			md.Set(MetadataPass, password)
		}
		// An unset or whitespace-only TAKLER_PASS means no takler-pass, and the
		// call goes ahead regardless.
		return md, nil
	}

	if username := osUsername(); username != "" {
		md.Set(MetadataUser, username)
	}

	secret, err := c.readOperatorSecret()
	if err != nil {
		return nil, err
	}
	if secret != "" {
		md.Set(MetadataSecret, secret)
	}

	return md, nil
}

// readOperatorSecret reads the Operator_Secret from the resolved secret file.
//
// It returns an empty secret and no error when no secret file is configured, or
// when the configured file is readable but holds no secret line; the second
// case also writes one line naming the path to standard error, because a
// configured-but-useless file is a mistake worth reporting even though the call
// goes ahead without takler-secret.
func (c *Credentials) readOperatorSecret() (string, error) {
	var flags, config CredentialSettings
	warn := io.Writer(os.Stderr)
	if c != nil {
		flags, config = c.flags, c.config
		if c.warn != nil {
			warn = c.warn
		}
	}

	path := ResolveSecretFile(flags, config)
	if path == "" {
		return "", nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", NewExitError(
			ExitRequestError,
			fmt.Sprintf("cannot read operator secret file %q: %v", path, err),
		)
	}

	secret := parseOperatorSecret(string(content))
	if secret == "" {
		fmt.Fprintf(
			warn,
			"operator secret file %q holds no secret line; continuing without %s.\n",
			path, MetadataSecret,
		)
	}
	return secret, nil
}

// parseOperatorSecret returns the Operator_Secret held in a secret file's
// content: the first line that is neither blank nor a comment, trimmed of
// surrounding whitespace (requirement 13.10). An empty result means the content
// holds no such line.
//
// The comment marker is "#" at the start of the trimmed line, so an indented
// comment is a comment, and a "#" inside a secret is part of the secret.
func parseOperatorSecret(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return trimmed
	}
	return ""
}

// osUsername returns the OS user name of the current process (requirement
// 13.9), or an empty string when it cannot be determined at all.
//
// os/user is the primary source. LOGNAME and USER are the fallback for the case
// where the process has no passwd entry, which happens in containers running
// under an arbitrary uid; Python's getpass.getuser() consults the same
// variables, so both clients agree on the name they send.
func osUsername() string {
	if current, err := user.Current(); err == nil && !isBlank(current.Username) {
		return strings.TrimSpace(current.Username)
	}
	for _, name := range []string{"LOGNAME", "USER"} {
		if value := os.Getenv(name); !isBlank(value) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
