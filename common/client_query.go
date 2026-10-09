// The Query_Commands, i.e. the reads an operator performs.
//
// As in client_child.go, a method here builds a request and reads the response;
// the connection, the timeout, the retry and the credentials belong to
// CallCommand (requirements 14.1, 13.7), and a failure returns as an *ExitError
// instead of ending the process (requirement 15.9).
//
// Unlike a command response, ping and coroutine query responses carry no flag.
package common

import (
	"fmt"
	"time"

	pb "github.com/cemc-oper/takler-client/takler_protocol"
)

// RunQueryPing checks that the server answers, and reports how long that took.
//
// The measured duration covers building the connection and every retried
// attempt as well, as reaching the server at all is what a ping is meant to
// prove.
func (c *TaklerServiceClient) RunQueryPing() (*pb.PingResponse, error) {
	startTime := time.Now()

	response, err := CallCommand(
		c, "ping", KindQuery, &pb.PingRequest{}, Transport.RunRequestPing,
	)
	if err != nil {
		return nil, err
	}

	fmt.Printf(
		"ping server (%s:%s) succeeded in %v\n",
		c.Host, c.Port, time.Since(startTime),
	)
	return response, nil
}

// RunQueryCoroutine prints the coroutines the server is running, one
// "name<TAB>description" line each, exactly as the Python client's
// run_query_coroutine does. It is a debugging view for the operator.
func (c *TaklerServiceClient) RunQueryCoroutine() (*pb.CoroutineResponse, error) {
	response, err := CallCommand(
		c, "coroutine", KindQuery, &pb.CoroutineRequest{}, Transport.QueryCoroutine,
	)
	if err != nil {
		return nil, err
	}

	for _, coroutine := range response.GetCoroutines() {
		fmt.Printf("%s\t%s\n", coroutine.GetName(), coroutine.GetDescription())
	}
	return response, nil
}
