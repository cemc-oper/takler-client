package common

import "errors"

var fullSummaryKeys = []string{"name", "node_kind", "type_id", "status", "suspended", "definition_generation", "attempt_id"}
var fullDefinitionKeys = []string{"trigger", "complete_trigger", "script_path", "times", "repeat", "events", "meters", "limits", "in_limits"}
var fullRuntimeKeys = []string{"begun", "calendar", "task_id", "try_no", "attempt_id", "submitted_at", "started_at", "ended_at", "aborted_reason", "submission_unknown_reason", "events", "meters", "limits", "repeat", "status", "event_values", "meter_values", "limit_usage", "time_free", "repeat_value", "trigger_free", "complete_trigger_free"}
var fullArtifactKeys = []string{"job_ref", "jobout_ref", "orvix_info_path", "not_applicable"}

func fullObject(keys []string, value any) (map[string]any, error) {
	items, ok := value.([]any)
	if !ok || len(items) != len(keys) {
		return nil, errors.New("invalid compact group")
	}
	result := make(map[string]any, len(keys))
	for i, key := range keys {
		result[key] = items[i]
	}
	return result, nil
}
func fullExpand(row []any, fields []string) (map[string]any, error) {
	if len(row) != 5 && len(row) != 6 {
		return nil, errors.New("invalid compact row")
	}
	path, ok := row[0].(string)
	if !ok {
		return nil, errors.New("invalid compact path")
	}
	summary, err := fullObject(fullSummaryKeys, row[3])
	if err != nil {
		return nil, err
	}
	values, ok := row[4].([]any)
	if !ok {
		return nil, errors.New("invalid compact groups")
	}
	selected := []string{}
	for _, group := range fields {
		if group != "summary" && group != "service" {
			selected = append(selected, group)
		}
	}
	if len(values) != len(selected) {
		return nil, errors.New("invalid compact group count")
	}
	groups := map[string]any{}
	for i, group := range selected {
		if group == "parameters" {
			lists, ok := values[i].([]any)
			if !ok || len(lists) != 2 {
				return nil, errors.New("invalid compact parameters")
			}
			params := map[string]any{}
			for j, name := range []string{"user", "generated"} {
				source, ok := lists[j].([]any)
				if !ok {
					return nil, errors.New("invalid compact parameter list")
				}
				items := make([]any, 0, len(source))
				for _, raw := range source {
					parts, ok := raw.([]any)
					if !ok || len(parts) != 3 {
						return nil, errors.New("invalid compact parameter")
					}
					label, ok := parts[0].(string)
					if !ok {
						return nil, errors.New("invalid compact parameter name")
					}
					redacted, ok := parts[2].(bool)
					if !ok {
						return nil, errors.New("invalid compact redaction")
					}
					item := map[string]any{"name": label, "source_path": path, "redacted": redacted}
					if !redacted {
						item["value"] = parts[1]
					}
					items = append(items, item)
				}
				params[name] = items
			}
			groups[group] = params
		} else {
			var keys []string
			switch group {
			case "definition":
				keys = fullDefinitionKeys
			case "runtime":
				keys = fullRuntimeKeys
			case "artifacts":
				keys = fullArtifactKeys
			default:
				return nil, errors.New("invalid compact group")
			}
			value, err := fullObject(keys, values[i])
			if err != nil {
				return nil, err
			}
			groups[group] = value
		}
	}
	result := map[string]any{"path": path, "parent_path": row[1], "child_paths": row[2], "summary": summary, "groups": groups}
	if len(row) == 6 {
		result["blob_refs"] = row[5]
	}
	return result, nil
}
