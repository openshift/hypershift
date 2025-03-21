package config

const (
	// Wildcard is used to imply all linters should be enabled/disabled.
	Wildcard = "*"
)

// Linters allows the user to configure which linters should, and
// should not be enabled.
type Linters struct {
	// Enable is used to enable specific linters.
	// Use '*' to enable all known linters.
	// When using '*', it should be the only value in the list.
	// Values in this list will be added to the linters enabled by default.
	// Values should not appear in both 'enable' and 'disable'.
	Enable []string `mapstructure:"enable"`

	// Disable is used to disable specific linters.
	// Use '*' to disable all known linters. When all linters are disabled,
	// only those explicitly called out in 'Enable' will be enabled.
	// When using '*', it should be the only value in the list.
	// Values in this list will be added to the linters disabled by default.
	// Values should not appear in both 'enable' and 'disable'.
	Disable []string `mapstructure:"disable"`
}
