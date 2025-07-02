package util

// MapsDiff compares two maps and returns a map of changed/created keys with their new values, a map of deleted keys,
// and a boolean indicating if there are any differences.
func MapsDiff(current, input map[string]string) (changed map[string]string, deleted map[string]string, different bool) {
	deleted = make(map[string]string)
	changed = make(map[string]string)
	different = false

	for k, v := range current {
		newValue, exists := input[k]
		// If the key is not in the input map, marke it as deleted
		if !exists {
			deleted[k] = v
			different = true
			continue
		}

		// If the value is different, mark it as changed
		if newValue != v {
			changed[k] = newValue
			different = true
		}
	}

	for k, v := range input {
		if _, exists := current[k]; !exists {
			changed[k] = v
			different = true
		}
	}

	return changed, deleted, different
}
