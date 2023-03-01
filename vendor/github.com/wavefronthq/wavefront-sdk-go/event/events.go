package event

// Option configuration
type Option func(map[string]interface{})

// Severity sets the event 'severity' annotation
func Severity(severity string) Option {
	return func(event map[string]interface{}) {
		annotations := event["annotations"].(map[string]string)
		annotations["severity"] = severity
	}
}

// Type sets the event 'type' annotation
func Type(t string) Option {
	return func(event map[string]interface{}) {
		annotations := event["annotations"].(map[string]string)
		annotations["type"] = t
	}
}

// Details sets the event 'details' annotation
func Details(details string) Option {
	return func(event map[string]interface{}) {
		annotations := event["annotations"].(map[string]string)
		annotations["details"] = details
	}
}

// Annotate adds an annotation with the given key and value
func Annotate(key, value string) Option {
	return func(event map[string]interface{}) {
		annotations := event["annotations"].(map[string]string)
		annotations[key] = value
	}
}
