package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConnectConfig(t *testing.T, content string) string {
	t.Helper()

	filePath := filepath.Join(t.TempDir(), "connect.yaml")
	if err := os.WriteFile(filePath, []byte(content), 0600); err != nil {
		t.Fatalf("write connect config: %v", err)
	}
	return filePath
}

// A full config file, including the server side only checkpoint section, must
// parse successfully and expose the three client side security fields.
func TestLoadConnectConfigWithSecuritySection(t *testing.T) {
	filePath := writeConnectConfig(t, `server:
  address:
    hostname: login_a06
    ip: 10.0.0.6
    port: "33083"

checkpoint:
  interval: 120
  file: /takler_home/takler.check

security:
  server_cert_file: /etc/takler/server.crt
  server_key_file: /etc/takler/server.key
  client_ca_file: null
  ca_file: /etc/takler/ca.crt
  server_name: login_a06
  auth_mode: enabled
  operator_secret_file: /etc/takler/secret
  operator_whitelist_file: /etc/takler/whitelist
  zombie_policy: fail
  audit_file: /takler_home/audit.jsonl
`)

	c, err := loadConnectConfig(filePath)
	if err != nil {
		t.Fatalf("load connect config: %v", err)
	}

	if got, want := c.Server.Address.Hostname, "login_a06"; got != want {
		t.Errorf("hostname = %q, want %q", got, want)
	}
	if got, want := c.Server.Address.Port, "33083"; got != want {
		t.Errorf("port = %q, want %q", got, want)
	}
	if got, want := c.GetCaFile(), "/etc/takler/ca.crt"; got != want {
		t.Errorf("ca file = %q, want %q", got, want)
	}
	if got, want := c.GetServerName(), "login_a06"; got != want {
		t.Errorf("server name = %q, want %q", got, want)
	}
	if got, want := c.GetOperatorSecretFile(), "/etc/takler/secret"; got != want {
		t.Errorf("operator secret file = %q, want %q", got, want)
	}
	if got := c.GetTransport(); got != "" {
		t.Errorf("transport = %q, want empty", got)
	}
	if got := c.GetHttpPort(); got != "" {
		t.Errorf("http port = %q, want empty", got)
	}
}

// The transport field and the HTTP listener subsection of the server section
// (M3 task 9) must parse; the client reads both.
func TestLoadConnectConfigWithTransportAndHttpSection(t *testing.T) {
	filePath := writeConnectConfig(t, `server:
  address:
    hostname: login_a06
    port: "33083"
  transport: http
  http:
    host: 0.0.0.0
    port: "8083"
`)

	c, err := loadConnectConfig(filePath)
	if err != nil {
		t.Fatalf("load connect config: %v", err)
	}

	if got, want := c.GetTransport(), "http"; got != want {
		t.Errorf("transport = %q, want %q", got, want)
	}
	if got, want := c.GetHttpPort(), "8083"; got != want {
		t.Errorf("http port = %q, want %q", got, want)
	}
}

// An M1 config file, which has no security section and may carry unknown
// sections, must still parse, leaving every security field unset.
func TestLoadConnectConfigWithoutSecuritySection(t *testing.T) {
	filePath := writeConnectConfig(t, `server:
  address:
    hostname: login_a06
    port: "33083"

checkpoint:
  interval: 120

unknown_section:
  whatever: 1
`)

	c, err := loadConnectConfig(filePath)
	if err != nil {
		t.Fatalf("load connect config: %v", err)
	}

	if got := c.GetCaFile(); got != "" {
		t.Errorf("ca file = %q, want empty", got)
	}
	if got := c.GetServerName(); got != "" {
		t.Errorf("server name = %q, want empty", got)
	}
	if got := c.GetOperatorSecretFile(); got != "" {
		t.Errorf("operator secret file = %q, want empty", got)
	}
	if got := c.GetTransport(); got != "" {
		t.Errorf("transport = %q, want empty", got)
	}
	if got := c.GetHttpPort(); got != "" {
		t.Errorf("http port = %q, want empty", got)
	}
}

// The accessors are used on the result of loadConnectConfig, which is nil when
// loading failed, so they must tolerate a nil receiver.
func TestConnectConfigAccessorsOnNil(t *testing.T) {
	var c *ConnectConfig

	if got := c.GetCaFile(); got != "" {
		t.Errorf("ca file = %q, want empty", got)
	}
	if got := c.GetServerName(); got != "" {
		t.Errorf("server name = %q, want empty", got)
	}
	if got := c.GetOperatorSecretFile(); got != "" {
		t.Errorf("operator secret file = %q, want empty", got)
	}
	if got := c.GetTransport(); got != "" {
		t.Errorf("transport = %q, want empty", got)
	}
	if got := c.GetHttpPort(); got != "" {
		t.Errorf("http port = %q, want empty", got)
	}
}
