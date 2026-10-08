package common

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// QueryShowOptions selects details sampled after the summary.
type QueryShowOptions struct {
	Parameter bool
	Trigger   bool
	Limit     bool
	Event     bool
	Meter     bool
}

// RunInitialQueryShow writes no output until the whole read and render succeed.
func (c *TaklerServiceClient) RunInitialQueryShow(selection QuerySelection, options QueryShowOptions, output io.Writer) (*QuerySnapshot, error) {
	c.queryMu.Lock()
	defer c.queryMu.Unlock()
	if output == nil {
		output = os.Stdout
	}
	var result *QuerySnapshot
	err := c.withQueryTransport(func(transport queryDocumentTransport) error {
		snapshot, err := c.readQuerySnapshot(transport, selection)
		if err != nil {
			return err
		}
		file, err := os.CreateTemp("", "takler-query-show-*.tmp")
		if err != nil {
			return NewExitError(ExitServerError, "cannot stage query output: "+err.Error())
		}
		defer os.Remove(file.Name())
		defer file.Close()
		if err = c.renderQueryShow(transport, snapshot, options, file); err != nil {
			return err
		}
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return NewExitError(ExitServerError, "cannot read staged query output: "+err.Error())
		}
		if _, err = io.Copy(output, file); err != nil {
			return NewExitError(ExitServerError, "cannot write query output: "+err.Error())
		}
		result = snapshot
		return nil
	})
	return result, err
}

func (c *TaklerServiceClient) renderQueryShow(transport queryDocumentTransport, snapshot *QuerySnapshot, options QueryShowOptions, output io.Writer) error {
	groups := []string{}
	if options.Parameter {
		groups = append(groups, "parameters")
	}
	if options.Trigger || options.Limit || options.Event || options.Meter {
		groups = append(groups, "definition")
	}
	if options.Limit || options.Event || options.Meter {
		groups = append(groups, "runtime")
	}
	targets := len(snapshot.order)
	if snapshot.ScopePath == "/" {
		targets--
	}
	if len(groups) > 0 && targets > 4096 {
		return &QueryFailureError{Code: "resource_exhausted", Message: "select a smaller scope for detailed show"}
	}
	if _, err := fmt.Fprintf(output, "# summary as_of=%s\n", snapshot.AsOf); err != nil {
		return err
	}
	if len(groups) > 0 {
		if _, err := fmt.Fprintln(output, "# details are sampled live after the summary"); err != nil {
			return err
		}
	}
	if options.Parameter && snapshot.ScopePath == "/" {
		detail, err := c.readQueryDetail(transport, snapshot, "/", []string{"parameters"})
		if err != nil {
			return err
		}
		if err := queryPrintParameters(output, "", detail.Groups["parameters"]); err != nil {
			return err
		}
	}
	base := 0
	if snapshot.ScopePath == "/" {
		base = 1
	}
	for _, path := range snapshot.order {
		if path == "/" {
			continue
		}
		node := snapshot.nodes[path]
		depth := strings.Count(path, "/") - strings.Count(snapshot.ScopePath, "/")
		if snapshot.ScopePath == "/" {
			depth++
		}
		prefix := strings.Repeat("  ", max(0, depth-base))
		state := node.Status
		if node.Suspended {
			state = "suspend (" + state + ")"
		}
		if _, err := fmt.Fprintf(output, "%s|- %s [%s]\n", prefix, path[strings.LastIndex(path, "/")+1:], state); err != nil {
			return err
		}
		if len(groups) == 0 {
			continue
		}
		detail, err := c.readQueryDetail(transport, snapshot, path, groups)
		if err != nil {
			return err
		}
		spaces := strings.Repeat(" ", len(prefix)+4)
		definition := detail.Groups["definition"]
		runtime := detail.Groups["runtime"]
		if repeat, ok := definition["repeat"].(map[string]any); ok {
			if err := queryShowLine(output, spaces, "repeat %v [%v, %v]", repeat["name"], repeat["start_date"], repeat["end_date"]); err != nil {
				return err
			}
		}
		if options.Trigger {
			if trigger, ok := definition["trigger"].(string); ok && trigger != "" {
				if err := queryShowLine(output, spaces, "trigger %s", trigger); err != nil {
					return err
				}
			}
			for _, item := range queryObjectItems(definition["times"]) {
				if err := queryShowLine(output, spaces, "time %v", item["time"]); err != nil {
					return err
				}
			}
		}
		if options.Parameter {
			if err := queryPrintParameters(output, spaces, detail.Groups["parameters"]); err != nil {
				return err
			}
		}
		if options.Limit {
			limits := map[string]any{}
			for _, item := range queryObjectItems(definition["limits"]) {
				limits[fmt.Sprint(item["name"])] = item["limit"]
			}
			for _, item := range queryObjectItems(runtime["limits"]) {
				name := fmt.Sprint(item["name"])
				if err := queryShowLine(output, spaces, "limit %s [%v/%v]", name, item["value"], limits[name]); err != nil {
					return err
				}
			}
		}
		if options.Event {
			for _, item := range queryObjectItems(runtime["events"]) {
				state := "unset"
				if item["value"] == true {
					state = "set"
				}
				if err := queryShowLine(output, spaces, "event %v [%s]", item["name"], state); err != nil {
					return err
				}
			}
		}
		if options.Meter {
			meters := map[string]map[string]any{}
			for _, item := range queryObjectItems(definition["meters"]) {
				meters[fmt.Sprint(item["name"])] = item
			}
			for _, item := range queryObjectItems(runtime["meters"]) {
				name := fmt.Sprint(item["name"])
				meta := meters[name]
				if err := queryShowLine(output, spaces, "meter %s %v %v [%v]", name, meta["min_value"], meta["max_value"], item["value"]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func queryShowLine(output io.Writer, indent string, format string, args ...any) error {
	_, err := fmt.Fprintf(output, indent+" "+format+"\n", args...)
	return err
}

func queryObjectItems(raw any) []map[string]any {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	items := make([]map[string]any, 0, len(list))
	for _, rawItem := range list {
		if item, ok := rawItem.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items
}

func queryPrintParameters(output io.Writer, indent string, group map[string]any) error {
	for _, item := range queryObjectItems(group["user"]) {
		value := ""
		if item["redacted"] == true {
			value = "<redacted>"
		} else if item["value"] != nil {
			value = fmt.Sprint(item["value"])
		}
		if err := queryShowLine(output, indent, "param %v '%s'", item["name"], value); err != nil {
			return err
		}
	}
	return nil
}
