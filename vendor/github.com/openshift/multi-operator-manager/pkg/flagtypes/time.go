// pulled from https://github.com/spf13/pflag/pull/348

package flagtypes

import (
	"fmt"
	"strings"
	"time"
)

// TimeValue adapts time.Time for use as a flag.
type TimeValue struct {
	*time.Time
	formats []string
}

func NewTimeValue(val time.Time, p *time.Time, formats []string) *TimeValue {
	*p = val
	return &TimeValue{
		Time:    p,
		formats: formats,
	}
}

// Set time.Time value from string based on accepted formats.
func (d *TimeValue) Set(s string) error {
	s = strings.TrimSpace(s)
	for _, f := range d.formats {
		v, err := time.Parse(f, s)
		if err != nil {
			continue
		}
		*d.Time = v
		return nil
	}

	formatsString := ""
	for i, f := range d.formats {
		if i > 0 {
			formatsString += ", "
		}
		formatsString += fmt.Sprintf("`%s`", f)
	}

	return fmt.Errorf("invalid time format `%s` must be one of: %s", s, formatsString)
}

// Type name for time.Time flags.
func (d *TimeValue) Type() string {
	return "time"
}

func (d *TimeValue) String() string { return d.Time.Format(time.RFC3339Nano) }
