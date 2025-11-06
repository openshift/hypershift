package plugins

// registeredPlugins holds the global registry of plugins.
// This variable is not thread-safe and should only be modified during
// application initialization before any concurrent access occurs.
// Reads are safe after initialization phase since plugins are typically
// registered once at startup and then accessed read-only.
var registeredPlugins = []Plugin{}

// RegisterPlugin adds a plugin to the global registry.
// This function is not thread-safe and should only be called during
// application initialization.
func RegisterPlugin(plugin Plugin) {
	registeredPlugins = append(registeredPlugins, plugin)
}

// GetAllPlugins returns a copy of all registered plugins.
// The returned slice is a defensive copy to prevent external modification
// of the internal registry.
func GetAllPlugins() []Plugin {
	// Return a defensive copy to prevent external modification
	plugins := make([]Plugin, len(registeredPlugins))
	copy(plugins, registeredPlugins)
	return plugins
}

// GetEnabledPlugins returns only the enabled plugins.
// The returned slice is a new slice containing only enabled plugins.
func GetEnabledPlugins() []Plugin {
	var enabled []Plugin
	for _, plugin := range registeredPlugins {
		if plugin.IsEnabled() {
			enabled = append(enabled, plugin)
		}
	}
	return enabled
}
