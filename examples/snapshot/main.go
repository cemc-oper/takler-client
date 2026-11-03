// Read public fields from one fixed version. Require successful completion
// before publishing streamed output; node callbacks can precede later errors.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/cemc-oper/takler-client/common"
)

func writeSnapshot(client *common.TaklerServiceClient, scope string, stream bool, output io.Writer) error {
	selection := common.FullSnapshotSelection{ScopePath: scope}
	encoder := json.NewEncoder(output)
	if stream {
		complete, err := client.IterFullSnapshot(selection, func(node map[string]any) error {
			return encoder.Encode(map[string]any{"node": node})
		})
		if err != nil {
			return err
		}
		return encoder.Encode(map[string]any{"complete": complete})
	}
	view, err := client.ReadFullSnapshot(selection)
	if err != nil {
		return err
	}
	return encoder.Encode(map[string]any{"completion": view.Complete, "nodes_by_path": view.NodesByPath})
}

func main() {
	host := flag.String("host", "localhost", "server host")
	port := flag.String("port", "33083", "server port")
	transport := flag.String("transport", "grpc", "grpc or http")
	scope := flag.String("scope", "/", "subtree path; / reads the whole tree")
	stream := flag.Bool("stream", false, "JSON lines with final completion")
	flag.Parse()
	client, err := common.NewTaklerServiceClient(*host, *port, *transport, common.SecurityLevels{})
	if err == nil {
		err = writeSnapshot(client, *scope, *stream, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
