// The cmd layer's two shared steps of running a command: building the client,
// and reading what came back.
//
// A command implementation is a RunE that resolves its own options, calls one
// common method and returns. Everything else -- where the address comes from,
// which security levels the client carries, how a response's Error_Code becomes
// output or an exit code -- is here, once, so the sixteen commands cannot drift
// apart on any of it.
//
// Nothing in this file ends the process: a failure is an *ExitError returned up
// through RunE to Execute, which is the single exit point (requirement 15.9).
package cmd

import (
	"fmt"
	"os"

	"github.com/cemc-oper/takler-client/common"
	pb "github.com/cemc-oper/takler-client/takler_protocol"
)

// NoTakler is the environment variable which, when set, makes a Child_Command a
// no-op (requirement 15.10).
const NoTakler = "NO_TAKLER"

// noTaklerIsSet reports whether the NO_TAKLER short circuit applies.
//
// Only the presence of the variable is tested, its value included the empty
// string is irrelevant, which is the M1 behaviour a job script relies on when it
// exports NO_TAKLER=1 to run outside the server.
func noTaklerIsSet() bool {
	_, ok := os.LookupEnv(NoTakler)
	return ok
}

// newClient returns the client of the server a command talks to, with the
// address, the transport and the security levels resolved (requirements 13.4,
// 13.5, 13.6, 13.11).
//
// host and port are the command's own --host / --port options, which win over
// every other level; an empty string means the option was not given.
//
// No connection is made and no certificate is read here: that happens per call,
// inside common, so a command that short circuits on NO_TAKLER never touches the
// network even if it built a client first.
func newClient(host string, port string) (*common.TaklerServiceClient, error) {
	target, err := resolveServerTarget(host, port)
	if err != nil {
		return nil, err
	}

	levels := common.SecurityLevels(newSecurityLevels(globalFlags, target.config))
	return common.NewTaklerServiceClient(target.host, target.port, target.transport, levels)
}

// reportCommandResponse turns the response of a command RPC into this process's
// outcome.
//
// A zero flag prints the Error_Code classification name rather than the raw
// integer (requirement 15.8), which is the same "received: <name>" line the
// Python client writes, so a script may grep either client's output the same
// way.
//
// A non zero flag is a business failure the server reported on a successful RPC
// (requirement 14.8): it becomes an *ExitError whose code is the contract's
// mapping of that Error_Code (requirements 15.2, 15.3) and whose message names
// the classification, so the operator reads "node_not_found: ..." instead of a
// bare number.
func reportCommandResponse(response interface {
	GetFlag() int32
	GetMessage() string
}) error {
	if response == nil {
		// Unreachable while common returns either a response or an error; kept
		// so a future change there cannot make this print "received: success"
		// for a call that produced nothing.
		return common.NewExitError(common.ExitServerError, "the server returned no response")
	}

	if service, ok := response.(*pb.ServiceResponse); ok && service == nil {
		return common.NewExitError(common.ExitServerError, "the server returned no response")
	}
	if batch, ok := response.(*pb.BatchResponse); ok {
		if batch == nil {
			return common.NewExitError(common.ExitServerError, "the server returned no response")
		}
		for _, item := range batch.Results {
			fmt.Printf("[%d] %s %s effect=%s: %s\n", item.Index, item.Target, common.ErrorName(item.Flag), item.Effect, item.Message)
		}
		fmt.Println(batch.Message)
	}
	flag := response.GetFlag()
	if flag == common.ErrorCodeSuccess {
		fmt.Printf("received: %s\n", common.ErrorName(flag))
		return nil
	}

	return common.NewExitError(
		common.ExitCodeForErrorCode(flag),
		fmt.Sprintf("%s: %s", common.ErrorName(flag), response.GetMessage()),
	)
}
