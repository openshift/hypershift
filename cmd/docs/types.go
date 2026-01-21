package docs

// FlagInfo contains metadata about a CLI flag
type FlagInfo struct {
	Name                 string
	Type                 string
	Default              string
	Description          string
	InHcp                bool
	InHypershift         bool
	RequiredInHcp        bool
	RequiredInHypershift bool
	Category             string
}

// CategoryInfo groups flags by category for template rendering
type CategoryInfo struct {
	Name        string
	Description string
	Flags       []FlagInfo
}

// TemplateData contains all data needed for template rendering
type TemplateData struct {
	Platform        string
	Command         string
	Categories      []CategoryInfo
	SharedCount     int
	DevOnlyCount    int
	HcpTotal        int
	HypershiftTotal int
}
