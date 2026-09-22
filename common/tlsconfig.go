package common

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Environment variables of the client's TLS settings (requirements 13.4,
// 13.5). The names are shared with the Python client, so one job script can
// export them once and call either client.
const (
	// TaklerTlsCaFile holds the path of the CA certificate the client trusts
	// as its root of trust.
	TaklerTlsCaFile = "TAKLER_TLS_CA_FILE"

	// TaklerTlsServerName holds the name to verify the server certificate's
	// host name against, for the case where the certificate's CN/SAN differs
	// from the host name the client connects to.
	TaklerTlsServerName = "TAKLER_TLS_SERVER_NAME"
)

// TLSSettings is one precedence level's worth of TLS inputs: a CA certificate
// file path and a certificate host name override, either of which may be
// absent (an empty or whitespace-only string).
//
// It exists so that the connect config's values can reach this package as a
// plain argument: common must not import cmd, which parses the connect config
// and already imports common. The cmd layer fills one TLSSettings from the
// command line options and one from the connect config's security section, and
// passes both in.
type TLSSettings struct {
	CaFile     string
	ServerName string
}

// isBlank reports whether a configured value counts as "not provided".
//
// Empty and whitespace-only strings are absent at every precedence level, so
// an exported-but-empty environment variable or a key written without a value
// in the connect config falls through to the next level instead of being taken
// as a path of "". This matches the Python side's _is_blank.
func isBlank(value string) bool {
	return strings.TrimSpace(value) == ""
}

// resolveWithEnv returns the first non-blank value of the three precedence
// levels, trimmed of surrounding whitespace, or an empty string when all three
// are absent. The environment variable is read from the process environment
// under envName.
func resolveWithEnv(flagValue string, envName string, configValue string) string {
	for _, value := range []string{flagValue, os.Getenv(envName), configValue} {
		if !isBlank(value) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// ResolveCaFile resolves the CA certificate file path in the order
// "command line option > TAKLER_TLS_CA_FILE > the connect config's
// security.ca_file > no CA certificate" (requirement 13.4). An empty result
// means no CA certificate is configured.
func ResolveCaFile(flags TLSSettings, config TLSSettings) string {
	return resolveWithEnv(flags.CaFile, TaklerTlsCaFile, config.CaFile)
}

// ResolveServerName resolves the certificate host name override in the order
// "command line option > TAKLER_TLS_SERVER_NAME > the connect config's
// security.server_name > no override" (requirement 13.5). An empty result
// means the host name the client connects to is verified as is.
func ResolveServerName(flags TLSSettings, config TLSSettings) string {
	return resolveWithEnv(flags.ServerName, TaklerTlsServerName, config.ServerName)
}

// BuildTransportCredentials returns the transport credentials of the client's
// connection.
//
// With a CA certificate configured the connection is TLS with that certificate
// as its root of trust (requirement 13.2); a resolved host name override is
// passed as the verification target, and an empty override leaves the
// connected host name in place (requirement 13.5). Without a CA certificate
// the connection is unencrypted, which keeps an M1 deployment working
// unchanged after an upgrade (requirement 13.3).
//
// An unreadable or unparseable CA certificate file is a configuration error of
// the request, not a server failure, so it returns an *ExitError carrying
// ExitRequestError and a single line naming the path and the reason
// (requirement 13.12).
func BuildTransportCredentials(flags TLSSettings, config TLSSettings) (credentials.TransportCredentials, error) {
	caFile := ResolveCaFile(flags, config)
	if caFile == "" {
		return insecure.NewCredentials(), nil
	}

	serverName := ResolveServerName(flags, config)
	// NewClientTLSFromFile reads and parses the file, and treats an empty
	// second argument as "no override".
	creds, err := credentials.NewClientTLSFromFile(caFile, serverName)
	if err != nil {
		return nil, NewExitError(
			ExitRequestError,
			fmt.Sprintf("cannot use CA certificate file %q: %v", caFile, err),
		)
	}

	return creds, nil
}

// BuildHTTPTLSConfig returns the TLS client configuration of the HTTP
// transport (M3 task 10), or nil when no CA certificate is configured, which
// leaves the transport on plaintext HTTP (requirement 13.3, mirrored on the
// HTTP wire).
//
// The rules are the gRPC channel's, restated for crypto/tls: the configured CA
// certificate is the root of trust (requirement 13.2), and a resolved host
// name override becomes tls.Config.ServerName (requirement 13.5) -- crypto/tls
// honors the override natively, unlike the Python client's httpx, which
// always verifies against the URL host and can only warn about an override.
//
// The failure shape mirrors BuildTransportCredentials: an unreadable or
// unparseable CA certificate file is an *ExitError carrying ExitRequestError
// with a single line naming the path and the reason (requirement 13.12).
func BuildHTTPTLSConfig(flags TLSSettings, config TLSSettings) (*tls.Config, error) {
	caFile := ResolveCaFile(flags, config)
	if caFile == "" {
		return nil, nil
	}

	content, err := os.ReadFile(caFile)
	if err != nil {
		return nil, NewExitError(
			ExitRequestError,
			fmt.Sprintf("cannot use CA certificate file %q: %v", caFile, err),
		)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(content) {
		return nil, NewExitError(
			ExitRequestError,
			fmt.Sprintf("cannot use CA certificate file %q: no PEM certificate found", caFile),
		)
	}

	tlsConfig := &tls.Config{RootCAs: pool}
	if serverName := ResolveServerName(flags, config); serverName != "" {
		tlsConfig.ServerName = serverName
	}
	return tlsConfig, nil
}
