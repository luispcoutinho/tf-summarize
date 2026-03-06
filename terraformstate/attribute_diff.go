package terraformstate

import (
	"encoding/json"
	"fmt"
	"sort"

	tfjson "github.com/hashicorp/terraform-json"
)

// AttributeDiff represents a single attribute change for a resource.
type AttributeDiff struct {
	Key    string
	Before string
	After  string
}

// GetAttributeDiffs returns the list of changed attributes for a resource change.
// Unknown (computed) values are rendered as "(known after apply)".
// Sensitive values are rendered as "(sensitive)".
// Null values are rendered as "(null)".
func GetAttributeDiffs(rc *tfjson.ResourceChange) []AttributeDiff {
	if rc.Change == nil {
		return nil
	}

	actions := rc.Change.Actions

	// For pure creates, show all non-null after-values
	if actions.Create() && !actions.Delete() {
		return diffForCreate(rc)
	}

	// For pure deletes, show all non-null before-values
	if actions.Delete() && !actions.Create() {
		return diffForDelete(rc)
	}

	// For updates and recreates, show before → after for changed attrs
	if actions.Update() || actions.DestroyBeforeCreate() || actions.CreateBeforeDestroy() {
		return diffForUpdate(rc)
	}

	return nil
}

func diffForCreate(rc *tfjson.ResourceChange) []AttributeDiff {
	after := toMap(rc.Change.After)
	afterUnknown := toMap(rc.Change.AfterUnknown)
	afterSensitive := toMap(rc.Change.AfterSensitive)
	if after == nil && afterUnknown == nil {
		return nil
	}

	// For creates, only show attributes that:
	//   - have a concrete known value (skip "(known after apply)" — not actionable at plan time)
	//   - are not null/empty/false (skip provider defaults and unset optionals)
	//   - are sensitive (always shown — tells the reviewer something is configured there)
	keys := mergedKeys(after, afterUnknown)
	diffs := make([]AttributeDiff, 0, len(keys))
	for _, k := range keys {
		afterStr := formatValue(after[k], afterUnknown[k], afterSensitive[k])
		// Skip unset / empty / default values
		if isEmptyValue(afterStr) {
			continue
		}
		// Skip computed values that are only known post-apply
		if afterStr == "(known after apply)" {
			continue
		}
		// Before is always omitted for creates (no "(none) ->" prefix)
		diffs = append(diffs, AttributeDiff{Key: k, Before: "", After: afterStr})
	}
	return diffs
}

func diffForDelete(rc *tfjson.ResourceChange) []AttributeDiff {
	before := toMap(rc.Change.Before)
	beforeSensitive := toMap(rc.Change.BeforeSensitive)
	if before == nil {
		return nil
	}

	// For deletes, only show the identifying attribute: prefer "id", fallback to "name"
	for _, key := range []string{"id", "name"} {
		if v, ok := before[key]; ok {
			str := formatValue(v, nil, beforeSensitive[key])
			if str != "(null)" {
				return []AttributeDiff{{Key: key, Before: str, After: ""}}
			}
		}
	}
	return nil
}

func diffForUpdate(rc *tfjson.ResourceChange) []AttributeDiff {
	before := toMap(rc.Change.Before)
	after := toMap(rc.Change.After)
	afterUnknown := toMap(rc.Change.AfterUnknown)
	beforeSensitive := toMap(rc.Change.BeforeSensitive)
	afterSensitive := toMap(rc.Change.AfterSensitive)

	keys := mergedKeys(before, after, afterUnknown)
	diffs := make([]AttributeDiff, 0)

	for _, k := range keys {
		beforeStr := formatValue(before[k], nil, beforeSensitive[k])
		afterStr := formatValue(after[k], afterUnknown[k], afterSensitive[k])

		// Skip unchanged
		if beforeStr == afterStr {
			continue
		}
		// Skip null -> null
		if beforeStr == "(null)" && afterStr == "(null)" {
			continue
		}
		// Skip empty/zero -> (null): internal resets like false->(null), 0->(null), []->(null)
		if isEmptyValue(beforeStr) && afterStr == "(null)" {
			continue
		}
		diffs = append(diffs, AttributeDiff{Key: k, Before: beforeStr, After: afterStr})
	}
	return diffs
}

// isEmptyValue returns true for values that represent an unset/zero state.
func isEmptyValue(s string) bool {
	switch s {
	case "(null)", "false", "0", "[]", "{}":
		return true
	}
	return false
}

// formatValue converts a raw JSON interface value into a human-readable string,
// applying special labels for unknown and sensitive markers.
func formatValue(val interface{}, unknown interface{}, sensitive interface{}) string {
	// Sensitive takes priority
	if isTruthy(sensitive) {
		return "(sensitive)"
	}
	// Unknown (known after apply)
	if isTruthy(unknown) {
		return "(known after apply)"
	}
	if val == nil {
		return "(null)"
	}
	switch v := val.(type) {
	case string:
		return fmt.Sprintf("%q", v)
	case bool:
		return fmt.Sprintf("%v", v)
	case json.Number:
		return v.String()
	case float64:
		return fmt.Sprintf("%g", v)
	case map[string]interface{}:
		if len(v) == 0 {
			return "{}"
		}
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	case []interface{}:
		if len(v) == 0 {
			return "[]"
		}
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// isTruthy returns true if the value is boolean true, a non-empty map, or a non-empty slice.
func isTruthy(v interface{}) bool {
	if v == nil {
		return false
	}
	switch vt := v.(type) {
	case bool:
		return vt
	case map[string]interface{}:
		return len(vt) > 0
	case []interface{}:
		return len(vt) > 0
	}
	return false
}

// toMap safely type-asserts an interface{} to map[string]interface{}.
func toMap(v interface{}) map[string]interface{} {
	if v == nil {
		return nil
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	return m
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mergedKeys(maps ...map[string]interface{}) []string {
	seen := make(map[string]struct{})
	for _, m := range maps {
		for k := range m {
			seen[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
