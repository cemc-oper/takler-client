package cmd

import (
	"fmt"
	"os"

	"github.com/cemc-oper/takler-client/common"
	"gopkg.in/yaml.v3"
)

const (
	TaklerHost        = "TAKLER_HOST"
	TaklerPort        = "TAKLER_PORT"
	TaklerName        = "TAKLER_NAME"
	TaklerConnectFile = "TAKLER_CONNECT_FILE"
	DefaultHost       = "localhost"
	DefaultPort       = "33083"
)

type Address struct {
	Hostname string `yaml:"hostname"`
	Ip       string `yaml:"ip"`
	Port     string `yaml:"port"`
}

type Server struct {
	Address Address `yaml:"address"`
}

// Security holds the subset of the connect config's security section which the
// client needs. The server only fields (certificate, key, whitelist, auth mode,
// zombie policy, audit file) are deliberately not declared here.
type Security struct {
	CaFile             string `yaml:"ca_file"`
	ServerName         string `yaml:"server_name"`
	OperatorSecretFile string `yaml:"operator_secret_file"`
}

type ConnectConfig struct {
	Server   Server   `yaml:"server"`
	Security Security `yaml:"security"`
}

// GetCaFile returns the configured client CA certificate file path,
// or an empty string when it is not configured.
func (c *ConnectConfig) GetCaFile() string {
	if c == nil {
		return ""
	}
	return c.Security.CaFile
}

// GetServerName returns the configured certificate host name override,
// or an empty string when it is not configured.
func (c *ConnectConfig) GetServerName() string {
	if c == nil {
		return ""
	}
	return c.Security.ServerName
}

// GetOperatorSecretFile returns the configured operator secret file path,
// or an empty string when it is not configured.
func (c *ConnectConfig) GetOperatorSecretFile() string {
	if c == nil {
		return ""
	}
	return c.Security.OperatorSecretFile
}

// loadConnectConfig parses the connect config file. Unknown sections, such as
// the server side only checkpoint section, are ignored rather than rejected:
// yaml.Unmarshal does not enable KnownFields.
func loadConnectConfig(filePath string) (*ConnectConfig, error) {
	buf, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	c := &ConnectConfig{}
	err = yaml.Unmarshal(buf, c)
	if err != nil {
		return nil, fmt.Errorf("in file %q: %w", filePath, err)
	}

	return c, nil
}

// serverTarget is the resolved address of the server, together with the connect
// config it was resolved from.
//
// The config is carried along because it is also the last precedence level of
// the TLS and credential settings (requirement 13.6): loading it once and
// handing both parts to the caller keeps a command from reading the same file
// twice, and keeps the two answers from disagreeing.
type serverTarget struct {
	host string
	port string

	// config is the parsed connect config, or nil when TAKLER_CONNECT_FILE is
	// not set. The ConnectConfig accessors tolerate a nil receiver.
	config *ConnectConfig
}

// resolveServerTarget resolves the server address from, in increasing order of
// precedence: the defaults, the TAKLER_HOST / TAKLER_PORT environment
// variables, the connect config, and the command's own --host / --port options.
//
// An unreadable or unparseable connect config returns an *ExitError with
// ExitRequestError rather than ending the process: it is a configuration error
// of the request, and the exit code is the cmd layer's to decide, in one place
// (requirement 15.9).
func resolveServerTarget(host string, port string) (serverTarget, error) {
	target := serverTarget{host: DefaultHost, port: DefaultPort}

	envHost := os.Getenv(TaklerHost)
	if len(envHost) > 0 {
		target.host = envHost
	}
	envPort := os.Getenv(TaklerPort)
	if len(envPort) > 0 {
		target.port = envPort
	}

	connectFilePath := os.Getenv(TaklerConnectFile)
	if len(connectFilePath) > 0 {
		connectConfig, err := loadConnectConfig(connectFilePath)
		if err != nil {
			return serverTarget{}, common.NewExitError(
				common.ExitRequestError,
				fmt.Sprintf("cannot load the connect config %s: %v", connectFilePath, err),
			)
		}
		target.config = connectConfig
		target.host = connectConfig.Server.Address.Hostname
		target.port = connectConfig.Server.Address.Port
	}

	if len(host) > 0 {
		target.host = host
	}
	if len(port) > 0 {
		target.port = port
	}

	return target, nil
}

// Deprecated: getHost is deprecated. Use getHostAndPort instead.
func getHost(host string) string {
	if len(host) > 0 {
		return host
	}
	host = os.Getenv(TaklerHost)
	if len(host) > 0 {
		return host
	}
	return DefaultHost
}

// Deprecated: getPort is deprecated. Use getHostAndPort instead.
func getPort(port string) string {
	if len(port) > 0 {
		return port
	}
	port = os.Getenv(TaklerPort)
	if len(port) > 0 {
		return port
	}
	return DefaultPort
}

func getNodePath(nodePath string) string {
	if len(nodePath) > 0 {
		return nodePath
	}
	nodePath = os.Getenv(TaklerName)
	if len(nodePath) > 0 {
		return nodePath
	}
	return ""
}
