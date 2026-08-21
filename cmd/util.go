package cmd

import (
	"fmt"
	"log"
	"os"

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

func getHostAndPort(host string, port string) (string, string) {
	resultHost := DefaultHost
	resultPort := DefaultPort

	envHost := os.Getenv(TaklerHost)
	if len(envHost) > 0 {
		resultHost = envHost
	}
	envPort := os.Getenv(TaklerPort)
	if len(envPort) > 0 {
		resultPort = envPort
	}

	connectFilePath := os.Getenv(TaklerConnectFile)
	if len(connectFilePath) > 0 {
		connectConfig, err := loadConnectConfig(connectFilePath)
		if err != nil {
			log.Fatalf("load connect config filed: %s", connectFilePath)
		}
		resultHost = connectConfig.Server.Address.Hostname
		resultPort = connectConfig.Server.Address.Port
	}

	if len(host) > 0 {
		resultHost = host
	}
	if len(port) > 0 {
		resultPort = port
	}

	return resultHost, resultPort
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
