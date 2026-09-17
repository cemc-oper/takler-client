// Connection construction of the Go client.
//
// This file owns the answer to "how does a call reach the server": the target
// string, the transport credentials, and the one place where the connect /
// close pair lives. Requirements: 13.1, 13.2, 13.3.
package common

import (
	"fmt"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
	"google.golang.org/grpc"
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

// NewTaklerServiceClient returns a client of the server at host:port whose
// connection and credentials are built from security.
//
// Nothing is connected or read from disk here: the CA certificate is read and
// the connection is created per call, in connect, so a client value can be
// built before it is known whether a call happens at all (the NO_TAKLER short
// circuit is decided in cmd).
func NewTaklerServiceClient(host string, port string, security SecurityLevels) *TaklerServiceClient {
	return &TaklerServiceClient{
		Host:        host,
		Port:        port,
		security:    security,
		credentials: NewCredentials(security.CredFlags, security.CredConfig),
	}
}

type TaklerServiceClient struct {
	Host string
	Port string

	// security is the two configuration levels the transport credentials and
	// the Credential_Metadata are resolved from.
	security SecurityLevels

	// credentials builds the Credential_Metadata of a call. Never nil.
	credentials *Credentials

	conn   *grpc.ClientConn
	client pb.TaklerServerClient
}

// Credentials returns the Credentials of this client, which the Call_Wrapper
// asks for the Credential_Metadata of a call (requirement 13.7).
func (c *TaklerServiceClient) Credentials() *Credentials {
	return c.credentials
}

// withConnection opens the connection, runs call with the generated client
// bound to it, and closes the connection afterwards.
//
// This is the single place the connect / defer close pair lives: every command
// method is a request built and a response read inside this closure, so a
// change to how the connection is made -- credentials, target spelling, a
// future connection reuse -- is a change to one function rather than to
// fourteen copies of the same three lines.
func (c *TaklerServiceClient) withConnection(call func(client pb.TaklerServerClient) error) error {
	client, err := c.connect()
	if err != nil {
		return err
	}
	defer c.closeConnection()

	return call(client)
}

// connect creates the connection and the generated client bound to it.
//
// The transport credentials come from BuildTransportCredentials, so a
// configured CA certificate makes this a TLS connection (requirement 13.2) and
// an absent one leaves it unencrypted (requirement 13.3). An unreadable CA
// certificate file is that function's *ExitError, returned as is: it is a
// configuration error of the request, not a failure to reach the server.
//
// grpc.NewClient replaces the deprecated grpc.Dial (requirement 13.1). It does
// not connect eagerly, so returning here means only that the target and the
// options are usable; the first RPC is what actually establishes the connection
// and what reports an unreachable server, which is where the Call_Wrapper's
// retry loop belongs.
func (c *TaklerServiceClient) connect() (pb.TaklerServerClient, error) {
	creds, err := BuildTransportCredentials(c.security.TLSFlags, c.security.TLSConfig)
	if err != nil {
		return nil, err
	}

	conn, err := grpc.NewClient(c.getTarget(), grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, NewExitError(
			ExitUnreachable,
			fmt.Sprintf("cannot create a connection to %s: %v", c.getServerAddress(), err),
		)
	}

	c.conn = conn
	c.client = pb.NewTaklerServerClient(conn)
	return c.client, nil
}

func (c *TaklerServiceClient) closeConnection() {
	if c.conn == nil {
		return
	}
	// Close only fails on an already closed connection, which cannot happen
	// here: withConnection closes exactly the connection it opened.
	_ = c.conn.Close()
	c.conn = nil
	c.client = nil
}

// getServerAddress returns the "host:port" the client talks to, as it appears
// in diagnostics.
func (c *TaklerServiceClient) getServerAddress() string {
	return fmt.Sprintf("%s:%s", c.Host, c.Port)
}

// getTarget returns the gRPC target of the connection.
//
// The address is spelled with the passthrough scheme because grpc.NewClient
// resolves a scheme-less target with the dns resolver, whereas the grpc.Dial it
// replaces defaulted to passthrough. Under dns the name is parsed and resolved
// by gRPC itself, which rejects host names the operating system accepts -- an
// HPC login node called login_a06 is the common case, as an underscore is not
// valid in a DNS name -- and would turn a working M1 deployment into a client
// that cannot connect after an upgrade. Passthrough keeps the address opaque to
// gRPC and hands it to the dialer, which resolves it exactly as before, through
// /etc/hosts included.
//
// TLS is unaffected: the credentials take the host name to verify from the
// target's authority, i.e. from this same address, unless a ServerName override
// was resolved.
func (c *TaklerServiceClient) getTarget() string {
	return fmt.Sprintf("passthrough:///%s", c.getServerAddress())
}
