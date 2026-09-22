// The Go client of the takler server.
//
// TaklerServiceClient is the command surface: it carries the server address
// for diagnostics, the Credentials every call is authenticated with, and the
// Transport the calls travel over. Everything about the wire -- the target
// spelling, the transport credentials, the connect / close pair -- is the
// transport's, behind the interface of transport.go. Requirements: 13.1,
// 13.2, 13.3.
package common

import (
	"fmt"
)

// SecurityLevels is the pair of precedence levels the cmd layer supplies to the
// common layer: the command line options and the connect config's security
// section. The environment variable level sits between them and is read inside
// ResolveCaFile / ResolveServerName / ResolveSecretFile, so it does not appear
// here.
//
// It is declared in common rather than in cmd because the client is constructed
// with it: common must not import cmd, which parses the connect config and
// already imports common. The field names are the ones cmd's securityLevels
// already uses, so cmd can hand its bundle over as is.
type SecurityLevels struct {
	TLSFlags   TLSSettings
	TLSConfig  TLSSettings
	CredFlags  CredentialSettings
	CredConfig CredentialSettings
}

// NewTaklerServiceClient returns a client of the server at host:port speaking
// the transport named transportName, with credentials built from security.
//
// transportName is one of the Transport* constants; an empty string means the
// default, gRPC. It typically comes from ResolveTransport, which also degrades
// unparseable values with a warning, so a name reaching this constructor
// unrecognized is a programming error and is reported as such. Selecting the
// HTTP transport is a clear error until its implementation lands (M3 task 10).
//
// Nothing is connected or read from disk here: the connection is created per
// call, in the transport's Open, so a client value can be built before it is
// known whether a call happens at all (the NO_TAKLER short circuit is decided
// in cmd).
func NewTaklerServiceClient(
	host string,
	port string,
	transportName string,
	security SecurityLevels,
) (*TaklerServiceClient, error) {
	transport, err := newTransport(transportName, host, port, security)
	if err != nil {
		return nil, err
	}
	return &TaklerServiceClient{
		Host:        host,
		Port:        port,
		credentials: NewCredentials(security.CredFlags, security.CredConfig),
		transport:   transport,
	}, nil
}

type TaklerServiceClient struct {
	Host string
	Port string

	// credentials builds the Credential_Metadata of a call. Never nil.
	credentials *Credentials

	// transport is how the calls reach the server. Never nil.
	transport Transport
}

// Credentials returns the Credentials of this client, which the Call_Wrapper
// asks for the Credential_Metadata of a call (requirement 13.7).
func (c *TaklerServiceClient) Credentials() *Credentials {
	return c.credentials
}

// withTransport opens the transport, runs call with it, and closes it
// afterwards.
//
// This is the single place the open / defer close pair lives: every command
// method is a request built and a response read inside this closure, so a
// change to how the connection is made -- credentials, target spelling, a
// future connection reuse -- is a change to one function rather than to
// sixteen copies of the same three lines.
func (c *TaklerServiceClient) withTransport(call func(transport Transport) error) error {
	if err := c.transport.Open(); err != nil {
		return err
	}
	defer c.transport.Close()

	return call(c.transport)
}

// getServerAddress returns the "host:port" the client talks to, as it appears
// in diagnostics.
func (c *TaklerServiceClient) getServerAddress() string {
	return fmt.Sprintf("%s:%s", c.Host, c.Port)
}
