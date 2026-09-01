// The Query_Commands, i.e. the reads an operator performs.
//
// As in client_child.go, a method here builds a request and reads the response;
// the connection, the timeout, the retry and the credentials belong to
// CallCommand (requirements 14.1, 13.7), and a failure returns as an *ExitError
// instead of ending the process (requirement 15.9).
//
// Unlike a command response, a query response carries no flag: what these two
// methods read out of the response is the payload the operator asked for, which
// is why they print it here, exactly as the Python client's run_request_show
// does.
package common

import (
	"fmt"
	"time"

	pb "github.com/perillaroc/takler-client/takler_protocol"
)

// RunQueryShow prints the server's bunch tree, with the item classes the flags
// select.
func (c *TaklerServiceClient) RunQueryShow(
	showTrigger bool,
	showParameter bool,
	showLimit bool,
	showEvent bool,
	showMeter bool,
) (*pb.ShowResponse, error) {
	response, err := CallCommand(c, "show", KindQuery, &pb.ShowRequest{
		ShowTrigger:   showTrigger,
		ShowParameter: showParameter,
		ShowLimit:     showLimit,
		ShowEvent:     showEvent,
		ShowMeter:     showMeter,
	}, pb.TaklerServerClient.RunRequestShow)
	if err != nil {
		return nil, err
	}

	fmt.Print(response.GetOutput())
	return response, nil
}

// RunQueryPing checks that the server answers, and reports how long that took.
//
// The measured duration covers building the connection and every retried
// attempt as well, as reaching the server at all is what a ping is meant to
// prove.
func (c *TaklerServiceClient) RunQueryPing() (*pb.PingResponse, error) {
	startTime := time.Now()

	response, err := CallCommand(
		c, "ping", KindQuery, &pb.PingRequest{}, pb.TaklerServerClient.RunRequestPing,
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
