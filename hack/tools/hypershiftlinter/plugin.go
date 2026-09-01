package hypershiftlinter

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/contextbackground"
	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/guestcluster"
	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/ipv6url"
	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/sippyannotation"
	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/testcasename"
	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/testfuncname"
	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/vacuouspass"

	"golang.org/x/tools/go/analysis"
)

type settings struct {
	Analyzers *analyzerSettings `json:"analyzers"`
}

type analyzerSettings struct {
	Enable []string `json:"enable"`
}

func BuildAnalyzers(rawSettings any) ([]*analysis.Analyzer, error) {
	all := allAnalyzers()

	if rawSettings == nil {
		return all, nil
	}

	s, err := decodeSettings(rawSettings)
	if err != nil {
		return nil, fmt.Errorf("invalid hypershiftlinter settings: %w", err)
	}

	if s.Analyzers == nil || len(s.Analyzers.Enable) == 0 {
		return all, nil
	}

	known := make(map[string]*analysis.Analyzer, len(all))
	for _, a := range all {
		known[a.Name] = a
	}

	var filtered []*analysis.Analyzer
	for _, name := range s.Analyzers.Enable {
		a, ok := known[name]
		if !ok {
			return nil, fmt.Errorf("unknown hypershiftlinter analyzer %q", name)
		}
		filtered = append(filtered, a)
	}
	return filtered, nil
}

func allAnalyzers() []*analysis.Analyzer {
	return []*analysis.Analyzer{
		testcasename.Analyzer,
		testfuncname.Analyzer,
		sippyannotation.Analyzer,
		guestcluster.Analyzer,
		contextbackground.Analyzer,
		vacuouspass.Analyzer,
		ipv6url.Analyzer,
	}
}

func decodeSettings(raw any) (settings, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return settings{}, err
	}
	// Reject unknown fields so that a typo such as "enbale" surfaces as an error
	// instead of silently leaving Enable empty and enabling every analyzer.
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s settings
	if err := dec.Decode(&s); err != nil {
		return settings{}, err
	}
	return s, nil
}
