package nodepool

import (
	"fmt"
	"strings"
	"testing"
)

const (
	shortName         = "short"
	shortName2        = "shorty"
	longName          = "longlong-longlonglonglong-longlonglonglonglonglonglonglonglonglonglonglonglong"
	longName2         = "longylongylongylongylongy-longylongylongylongylongylongylongy-longy-longy"
	singleLetterName  = "g"
	singleLetterName2 = "t"
)

func FuzzComposeValidName(f *testing.F) {
	tc := []struct {
		name1 string
		name2 string
	}{
		{
			shortName,
			shortName,
		},
		{
			shortName2,
			longName,
		},
		{
			longName,
			longName2,
		},
		{
			singleLetterName,
			singleLetterName2,
		},
		{
			longName2,
			singleLetterName,
		},
	}
	for _, tc := range tc {
		f.Add(tc.name1, tc.name2)
	}
	f.Fuzz(func(t *testing.T, name1, name2 string) {
		fmt.Printf("%s     %s", name1, name2)
		composedName := ComposeValidName(name1, name2)
		if len(composedName) > qualifiedNameMaxLength {
			t.Errorf("composed name is too long: %s, given names are: %s and %s", composedName, name1, name2)
		}
		if len(composedName) < 1 {
			t.Errorf("composed name is empty, given names are: %s and %s", name1, name2)
		}
		if !strings.Contains(composedName, "-") {
			t.Errorf("composed name does no contain a dash '-': %s, given names are: %s and %s", composedName, name1, name2)
		}
	})
}
