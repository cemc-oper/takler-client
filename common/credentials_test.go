package common

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
	"google.golang.org/grpc/metadata"
)

// Tests for the credential half of the cross-language contract: which metadata
// key a call of each CommandKind carries, how the Operator_Secret_File is
// parsed, and what happens when the configured file cannot be used
// (requirements 13.8, 13.9, 13.10, 13.13, 16.16).
//
// The expected keys are spelled as string literals rather than as the
// MetadataPass / MetadataSecret / MetadataUser constants wherever the assertion
// is about the contract itself: a test that reads the constant it is meant to
// police cannot notice a renamed key. The constants are used only where the
// assertion is about behaviour, e.g. "this key is absent".
//
// TestCredentialsOnTheWire is the one that answers requirement 16.16, because
// the wire is where the contract actually lives: BuildMetadata returning the
// right map proves nothing if gRPC drops or renames a key on the way out. The
// remaining tests work against BuildMetadata directly, which is where the
// per-kind branching and the file parsing are observable.

// Distinctive credential values. Nothing in an error message or a warning line
// may contain either of them (requirement 13.13), and both are unusual enough
// that a substring search for them cannot match incidental text such as a
// temporary directory name.
const (
	credPasswordValue = "p4ssw0rd-job-value-7c1b"
	credSecretValue   = "s3cr3t-operator-value-9f2a"
)

// credUnsetEnv removes name for the duration of the test and restores the
// original value afterwards.
//
// t.Setenv has no "unset" form, so the value is first set through it -- which
// registers the restore and the no-parallel guard -- and then removed.
func credUnsetEnv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "")
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unset %s: %v", name, err)
	}
}

// credCleanEnv removes both credential environment variables, so that a value
// inherited from the developer's shell cannot make a test pass or fail for the
// wrong reason.
func credCleanEnv(t *testing.T) {
	t.Helper()
	credUnsetEnv(t, EnvJobPassword)
	credUnsetEnv(t, EnvSecretFile)
}

// credSecretFile writes content to a file in the test's temporary directory and
// returns its path.
func credSecretFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "operator.secret")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	return path
}

// credExpectedUsername restates osUsername's sources independently: the passwd
// entry first, then the two environment variables Python's getpass.getuser()
// also consults. An empty result means the OS user name cannot be determined
// here, in which case takler-user is legitimately absent.
func credExpectedUsername(t *testing.T) string {
	t.Helper()
	if current, err := user.Current(); err == nil {
		if name := strings.TrimSpace(current.Username); name != "" {
			return name
		}
	}
	for _, name := range []string{"LOGNAME", "USER"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

// credRequireUsername returns a non-empty expected OS user name, arranging one
// through LOGNAME when the process has no passwd entry and no name in the
// environment either -- the case of a container running under an arbitrary uid.
//
// A test asserting "takler-user equals the OS user name" would pass vacuously in
// that environment, with both the expected and the actual value empty, so it
// calls this instead of credExpectedUsername.
func credRequireUsername(t *testing.T) string {
	t.Helper()
	if name := credExpectedUsername(t); name != "" {
		return name
	}
	const fallback = "cred-test-operator"
	t.Setenv("LOGNAME", fallback)
	return fallback
}

// credAssertValue asserts that key carries exactly want.
func credAssertValue(t *testing.T, md metadata.MD, key string, want string) {
	t.Helper()
	values := md.Get(key)
	if len(values) != 1 {
		t.Fatalf("%s = %v, want exactly one value %q", key, values, want)
	}
	if values[0] != want {
		t.Errorf("%s = %q, want %q", key, values[0], want)
	}
}

// credAssertAbsent asserts that none of keys is present.
func credAssertAbsent(t *testing.T, md metadata.MD, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if values := md.Get(key); len(values) != 0 {
			t.Errorf("%s = %v, want it absent", key, values)
		}
	}
}

// credAssertUsername asserts that takler-user carries the OS user name, or is
// absent when the name cannot be determined at all.
func credAssertUsername(t *testing.T, md metadata.MD) {
	t.Helper()
	expected := credExpectedUsername(t)
	if expected == "" {
		credAssertAbsent(t, md, MetadataUser)
		return
	}
	credAssertValue(t, md, "takler-user", expected)
}

// credAssertNoCredentialValue asserts that text mentions neither credential
// value (requirement 13.13). label names what the text is, for the failure
// message.
func credAssertNoCredentialValue(t *testing.T, label string, text string) {
	t.Helper()
	for _, value := range []string{credPasswordValue, credSecretValue} {
		if strings.Contains(text, value) {
			t.Errorf("%s contains the credential value %q: %q", label, value, text)
		}
	}
}

// TestBuildMetadataChild covers the Child_Command row of the contract: exactly
// one key, taken from TAKLER_PASS, and never an operator credential
// (requirement 13.8).
func TestBuildMetadataChild(t *testing.T) {
	t.Run("carries takler-pass from the environment", func(t *testing.T) {
		credCleanEnv(t)
		t.Setenv(EnvJobPassword, credPasswordValue)

		md, err := NewCredentials(CredentialSettings{}, CredentialSettings{}).BuildMetadata(KindChild)
		if err != nil {
			t.Fatalf("BuildMetadata(child): %v", err)
		}
		credAssertValue(t, md, "takler-pass", credPasswordValue)
	})

	t.Run("carries the value verbatim", func(t *testing.T) {
		// Inner whitespace and a "#" are part of a password, unlike in a secret
		// file: TAKLER_PASS is read as a whole value, not parsed.
		const password = "  pass with spaces # and a hash  "
		credCleanEnv(t)
		t.Setenv(EnvJobPassword, password)

		md, err := NewCredentials(CredentialSettings{}, CredentialSettings{}).BuildMetadata(KindChild)
		if err != nil {
			t.Fatalf("BuildMetadata(child): %v", err)
		}
		credAssertValue(t, md, "takler-pass", password)
	})

	t.Run("never carries an operator credential", func(t *testing.T) {
		// A secret file is configured and readable, so the only reason
		// takler-secret can be absent is the command kind.
		credCleanEnv(t)
		t.Setenv(EnvJobPassword, credPasswordValue)
		t.Setenv(EnvSecretFile, credSecretFile(t, credSecretValue+"\n"))

		md, err := NewCredentials(CredentialSettings{}, CredentialSettings{}).BuildMetadata(KindChild)
		if err != nil {
			t.Fatalf("BuildMetadata(child): %v", err)
		}
		credAssertValue(t, md, "takler-pass", credPasswordValue)
		credAssertAbsent(t, md, MetadataSecret, MetadataUser)
		if len(md) != 1 {
			t.Errorf("metadata = %v, want only takler-pass", md)
		}
	})

	t.Run("omits takler-pass when TAKLER_PASS is unset or blank", func(t *testing.T) {
		// The call goes ahead without the key; refusing it in the client would
		// break a server running with Auth_Mode=disabled.
		cases := []struct {
			name  string
			unset bool
			value string
		}{
			{name: "unset", unset: true},
			{name: "empty", value: ""},
			{name: "single space", value: " "},
			{name: "spaces", value: "   "},
			{name: "tab", value: "\t"},
			{name: "newline", value: "\n"},
			{name: "mixed whitespace", value: " \t\r\n "},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				credCleanEnv(t)
				if !c.unset {
					t.Setenv(EnvJobPassword, c.value)
				}

				md, err := NewCredentials(CredentialSettings{}, CredentialSettings{}).BuildMetadata(KindChild)
				if err != nil {
					t.Fatalf("BuildMetadata(child): %v", err)
				}
				credAssertAbsent(t, md, MetadataPass)
				if len(md) != 0 {
					t.Errorf("metadata = %v, want it empty", md)
				}
			})
		}
	})

	t.Run("a nil Credentials still reads the environment", func(t *testing.T) {
		credCleanEnv(t)
		t.Setenv(EnvJobPassword, credPasswordValue)

		var credentials *Credentials
		md, err := credentials.BuildMetadata(KindChild)
		if err != nil {
			t.Fatalf("BuildMetadata(child): %v", err)
		}
		credAssertValue(t, md, "takler-pass", credPasswordValue)
	})
}

// TestBuildMetadataOperator covers the Operator_Command row of the contract:
// takler-user always, takler-secret when a secret file is configured and holds
// a secret (requirements 13.9, 13.10). KindControl and KindQuery are both
// Operator, so every case runs for both.
func TestBuildMetadataOperator(t *testing.T) {
	operatorKinds := []CommandKind{KindControl, KindQuery}

	t.Run("carries takler-secret and takler-user", func(t *testing.T) {
		for _, kind := range operatorKinds {
			t.Run(kind.String(), func(t *testing.T) {
				credCleanEnv(t)
				t.Setenv(EnvSecretFile, credSecretFile(t, credSecretValue+"\n"))

				md, err := NewCredentials(CredentialSettings{}, CredentialSettings{}).BuildMetadata(kind)
				if err != nil {
					t.Fatalf("BuildMetadata(%v): %v", kind, err)
				}
				credAssertValue(t, md, "takler-secret", credSecretValue)
				credAssertUsername(t, md)
				credAssertAbsent(t, md, MetadataPass)
			})
		}
	})

	t.Run("carries takler-user alone without a secret file", func(t *testing.T) {
		for _, kind := range operatorKinds {
			t.Run(kind.String(), func(t *testing.T) {
				credCleanEnv(t)
				// TAKLER_PASS is set and still must not leak into an operator
				// call.
				t.Setenv(EnvJobPassword, credPasswordValue)

				md, err := NewCredentials(CredentialSettings{}, CredentialSettings{}).BuildMetadata(kind)
				if err != nil {
					t.Fatalf("BuildMetadata(%v): %v", kind, err)
				}
				credAssertUsername(t, md)
				credAssertAbsent(t, md, MetadataSecret, MetadataPass)
			})
		}
	})

	t.Run("takes the secret file from the command line level", func(t *testing.T) {
		credCleanEnv(t)
		flags := CredentialSettings{SecretFile: credSecretFile(t, credSecretValue+"\n")}

		md, err := NewCredentials(flags, CredentialSettings{}).BuildMetadata(KindControl)
		if err != nil {
			t.Fatalf("BuildMetadata(control): %v", err)
		}
		credAssertValue(t, md, "takler-secret", credSecretValue)
	})

	t.Run("takes the secret file from the connect config level", func(t *testing.T) {
		credCleanEnv(t)
		config := CredentialSettings{SecretFile: credSecretFile(t, credSecretValue+"\n")}

		md, err := NewCredentials(CredentialSettings{}, config).BuildMetadata(KindControl)
		if err != nil {
			t.Fatalf("BuildMetadata(control): %v", err)
		}
		credAssertValue(t, md, "takler-secret", credSecretValue)
	})

	t.Run("parses the configured file by the secret file rule", func(t *testing.T) {
		// End to end proof that BuildMetadata applies parseOperatorSecret and
		// not, say, the whole file content.
		credCleanEnv(t)
		content := "# operator secrets\n\n   \n\t" + credSecretValue + "  \nsecond-secret\n"
		t.Setenv(EnvSecretFile, credSecretFile(t, content))

		md, err := NewCredentials(CredentialSettings{}, CredentialSettings{}).BuildMetadata(KindQuery)
		if err != nil {
			t.Fatalf("BuildMetadata(query): %v", err)
		}
		credAssertValue(t, md, "takler-secret", credSecretValue)
	})
}

// TestBuildMetadataUnreadableSecretFile covers the one hard failure: the
// operator named a file the client cannot read (requirement 13.12). The error
// names the path and carries ExitRequestError, and it carries no credential
// value (requirement 13.13).
func TestBuildMetadataUnreadableSecretFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.secret")

	credCleanEnv(t)
	t.Setenv(EnvJobPassword, credPasswordValue)
	t.Setenv(EnvSecretFile, missing)

	md, err := NewCredentials(CredentialSettings{}, CredentialSettings{}).BuildMetadata(KindControl)
	if err == nil {
		t.Fatalf("BuildMetadata(control) = %v, want an error", md)
	}
	if md != nil {
		t.Errorf("metadata = %v, want nil alongside the error", md)
	}

	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error type = %T, want *ExitError", err)
	}
	if exitErr.Code != ExitRequestError {
		t.Errorf("exit code = %d, want %d", exitErr.Code, ExitRequestError)
	}
	if !strings.Contains(exitErr.Message, missing) {
		t.Errorf("message = %q, want it to name the path %q", exitErr.Message, missing)
	}
	if lines := strings.Count(strings.TrimRight(exitErr.Message, "\n"), "\n"); lines != 0 {
		t.Errorf("message = %q, want a single line", exitErr.Message)
	}
	credAssertNoCredentialValue(t, "the error message", exitErr.Message)
}

// TestBuildMetadataSecretlessFile covers the configured-but-useless file: one
// warning line to the injected writer, and the call goes ahead without
// takler-secret.
//
// The file holds the secret value inside a comment, so the warning line is also
// a check that a value the client decided not to send does not reach the output
// by another route (requirement 13.13).
func TestBuildMetadataSecretlessFile(t *testing.T) {
	contents := map[string]string{
		"empty":              "",
		"blank lines":        "\n   \n\t\n",
		"comments only":      "# " + credSecretValue + "\n#another\n",
		"indented comment":   "   # " + credSecretValue + "\n",
		"comments and blank": "\n# " + credSecretValue + "\n \n",
	}
	for name, content := range contents {
		t.Run(name, func(t *testing.T) {
			credCleanEnv(t)
			path := credSecretFile(t, content)
			t.Setenv(EnvSecretFile, path)

			var warn bytes.Buffer
			credentials := NewCredentials(CredentialSettings{}, CredentialSettings{})
			credentials.warn = &warn

			md, err := credentials.BuildMetadata(KindControl)
			if err != nil {
				t.Fatalf("BuildMetadata(control): %v", err)
			}
			credAssertAbsent(t, md, MetadataSecret)
			credAssertUsername(t, md)

			warned := warn.String()
			if !strings.HasSuffix(warned, "\n") {
				t.Errorf("warning = %q, want it to end with a newline", warned)
			}
			if lines := strings.Count(warned, "\n"); lines != 1 {
				t.Errorf("warning = %q, want exactly one line", warned)
			}
			if !strings.Contains(warned, path) {
				t.Errorf("warning = %q, want it to name the path %q", warned, path)
			}
			credAssertNoCredentialValue(t, "the warning line", warned)
		})
	}
}

// TestBuildMetadataUsableFileWarnsNothing is the counterpart of the test above:
// a file that does hold a secret produces no output at all, so the warning is a
// signal rather than noise on every operator call.
func TestBuildMetadataUsableFileWarnsNothing(t *testing.T) {
	credCleanEnv(t)
	t.Setenv(EnvSecretFile, credSecretFile(t, "# comment\n"+credSecretValue+"\n"))

	var warn bytes.Buffer
	credentials := NewCredentials(CredentialSettings{}, CredentialSettings{})
	credentials.warn = &warn

	md, err := credentials.BuildMetadata(KindControl)
	if err != nil {
		t.Fatalf("BuildMetadata(control): %v", err)
	}
	credAssertValue(t, md, "takler-secret", credSecretValue)
	if warned := warn.String(); warned != "" {
		t.Errorf("warning = %q, want none", warned)
	}
}

// TestParseOperatorSecret covers the secret file rule of requirement 13.10:
// the first line that is neither blank nor a comment, trimmed.
//
// Note the difference from the server, which accepts every line of the file as
// a valid Operator_Secret: the client has to pick one value to send, and picks
// the first usable line.
func TestParseOperatorSecret(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{name: "single line", content: "secret-one\n", want: "secret-one"},
		{name: "single line without newline", content: "secret-one", want: "secret-one"},
		{
			name:    "several lines, the first wins",
			content: "secret-one\nsecret-two\nsecret-three\n",
			want:    "secret-one",
		},
		{
			name:    "leading blank lines are skipped",
			content: "\n\n   \n\t\nsecret-one\n",
			want:    "secret-one",
		},
		{
			name:    "comment lines are skipped",
			content: "# rotated 2025-01-01\n#secret-old\nsecret-one\n",
			want:    "secret-one",
		},
		{
			name:    "an indented comment is a comment",
			content: "   # indented\n\t#tabbed\nsecret-one\n",
			want:    "secret-one",
		},
		{
			name:    "surrounding whitespace is trimmed",
			content: "  \tsecret-one \t \n",
			want:    "secret-one",
		},
		{
			name:    "carriage returns are trimmed",
			content: "\r\nsecret-one\r\nsecret-two\r\n",
			want:    "secret-one",
		},
		{
			name:    "a hash inside the value is part of the value",
			content: "secret#one\n",
			want:    "secret#one",
		},
		{
			name:    "a hash after the value is part of the value",
			content: "secret-one # not a comment\n",
			want:    "secret-one # not a comment",
		},
		{name: "empty content", content: "", want: ""},
		{name: "blank content", content: "\n \t\n\r\n   \n", want: ""},
		{name: "comments only", content: "# one\n  # two\n#\n", want: ""},
		{
			name:    "inner blank and comment lines do not stop the search",
			content: "\n# header\n \nsecret-one\n# trailer\n",
			want:    "secret-one",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseOperatorSecret(c.content); got != c.want {
				t.Errorf("parseOperatorSecret(%q) = %q, want %q", c.content, got, c.want)
			}
		})
	}
}

// TestResolveSecretFile covers the four precedence levels of requirement 13.11,
// including the blank-value rule: a whitespace-only value at one level falls
// through to the next instead of counting as a configured path.
func TestResolveSecretFile(t *testing.T) {
	cases := []struct {
		name   string
		flags  string
		env    string
		config string
		want   string
	}{
		{name: "nothing configured", want: ""},
		{name: "flags only", flags: "/from/flags", want: "/from/flags"},
		{name: "env only", env: "/from/env", want: "/from/env"},
		{name: "config only", config: "/from/config", want: "/from/config"},
		{
			name:   "flags win over env and config",
			flags:  "/from/flags",
			env:    "/from/env",
			config: "/from/config",
			want:   "/from/flags",
		},
		{
			name:   "env wins over config",
			env:    "/from/env",
			config: "/from/config",
			want:   "/from/env",
		},
		{
			name:   "a blank level falls through",
			flags:  "   ",
			env:    "\t",
			config: "/from/config",
			want:   "/from/config",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			credCleanEnv(t)
			if c.env != "" {
				t.Setenv(EnvSecretFile, c.env)
			}

			got := ResolveSecretFile(
				CredentialSettings{SecretFile: c.flags},
				CredentialSettings{SecretFile: c.config},
			)
			if got != c.want {
				t.Errorf("ResolveSecretFile() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestCredentialsOnTheWire is the requirement 16.16 test: it asserts the
// Credential_Metadata as the server received it, not as BuildMetadata returned
// it, by attaching the built metadata to the outgoing context exactly as the
// Call_Wrapper does and calling the in-process server.
func TestCredentialsOnTheWire(t *testing.T) {
	t.Run("a child command carries takler-pass", func(t *testing.T) {
		credCleanEnv(t)
		t.Setenv(EnvJobPassword, credPasswordValue)
		t.Setenv(EnvSecretFile, credSecretFile(t, credSecretValue+"\n"))

		server := newFakeServer(t)
		ctx := credOutgoingContext(t, KindChild, NewCredentials(CredentialSettings{}, CredentialSettings{}))

		_, err := server.Client.RunCommandInit(ctx, &pb.InitCommand{
			ChildOptions: &pb.ChildCommandOptions{NodePath: "/flow1/task1"},
			TaskId:       "job-1",
		})
		if err != nil {
			t.Fatalf("RunCommandInit: %v", err)
		}

		call := server.Servicer.lastCall(t)
		if got := call.value("takler-pass"); got != credPasswordValue {
			t.Errorf("takler-pass on the wire = %q, want %q", got, credPasswordValue)
		}
		for _, key := range []string{MetadataSecret, MetadataUser} {
			if got := call.value(key); got != "" {
				t.Errorf("%s on the wire = %q, want it absent", key, got)
			}
		}
	})

	t.Run("an operator command carries takler-secret and takler-user", func(t *testing.T) {
		credCleanEnv(t)
		t.Setenv(EnvSecretFile, credSecretFile(t, "# rotated\n  "+credSecretValue+"  \nolder-secret\n"))
		expectedUser := credRequireUsername(t)

		server := newFakeServer(t)
		credentials := NewCredentials(CredentialSettings{}, CredentialSettings{})

		// One control command and one query command, since both are Operator.
		controlCtx := credOutgoingContext(t, KindControl, credentials)
		if _, err := server.Client.RunCommandSuspend(controlCtx, &pb.SuspendCommand{}); err != nil {
			t.Fatalf("RunCommandSuspend: %v", err)
		}
		queryCtx := credOutgoingContext(t, KindQuery, credentials)
		if _, err := server.Client.RunRequestShow(queryCtx, &pb.ShowRequest{}); err != nil {
			t.Fatalf("RunRequestShow: %v", err)
		}

		calls := server.Servicer.receivedCalls()
		if len(calls) != 2 {
			t.Fatalf("call count = %d, want 2", len(calls))
		}
		for _, call := range calls {
			if got := call.value("takler-secret"); got != credSecretValue {
				t.Errorf("%s: takler-secret on the wire = %q, want %q", call.Method, got, credSecretValue)
			}
			if got := call.value("takler-user"); got != expectedUser {
				t.Errorf("%s: takler-user on the wire = %q, want %q", call.Method, got, expectedUser)
			}
			if got := call.value(MetadataPass); got != "" {
				t.Errorf("%s: takler-pass on the wire = %q, want it absent", call.Method, got)
			}
		}
	})
}

// credOutgoingContext builds the Credential_Metadata of kind and attaches it to
// a background context the way the Call_Wrapper does.
func credOutgoingContext(t *testing.T, kind CommandKind, credentials *Credentials) context.Context {
	t.Helper()
	md, err := credentials.BuildMetadata(kind)
	if err != nil {
		t.Fatalf("BuildMetadata(%v): %v", kind, err)
	}
	return metadata.NewOutgoingContext(context.Background(), md)
}
