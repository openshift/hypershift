package reporter

import (
	"encoding/json"
	"io"
	"time"

	"github.com/openshift/hypershift/contrib/hcp-infra/pkg/scanner"
)

// JSONReporter formats inventory output as JSON.
type JSONReporter struct{}

// jsonInventory mirrors scanner.Inventory with json tags.
type jsonInventory struct {
	AccountID string            `json:"account_id"`
	Regions   []string          `json:"regions"`
	ScannedAt time.Time         `json:"scanned_at"`
	ScanMode  string            `json:"scan_mode"`
	Groups    []*jsonInfraGroup `json:"groups"`
	Ungrouped []*jsonResource   `json:"ungrouped,omitempty"`
}

// jsonInfraGroup mirrors scanner.InfraGroup with json tags.
type jsonInfraGroup struct {
	InfraID     string          `json:"infra_id"`
	ClusterName string          `json:"cluster_name,omitempty"`
	Owner       string          `json:"owner,omitempty"`
	Category    string          `json:"category"`
	Resources   []*jsonResource `json:"resources"`
	CreatedAt   time.Time       `json:"created_at"`
	MonthlyCost float64         `json:"monthly_cost"`
	HasVPC      bool            `json:"has_vpc"`
	HasHC       bool            `json:"has_hc"`
}

// jsonResource mirrors scanner.Resource with json tags.
type jsonResource struct {
	Type        string            `json:"type"`
	ID          string            `json:"id"`
	ARN         string            `json:"arn,omitempty"`
	Name        string            `json:"name,omitempty"`
	Region      string            `json:"region"`
	InfraID     string            `json:"infra_id"`
	ClusterName string            `json:"cluster_name,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	Tags        map[string]string `json:"tags,omitempty"`
	MonthlyCost float64           `json:"monthly_cost"`
	Details     string            `json:"details,omitempty"`
}

// Report writes JSON-formatted output to w.
func (j *JSONReporter) Report(w io.Writer, inv *scanner.Inventory) error {
	out := toJSONInventory(inv)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func toJSONInventory(inv *scanner.Inventory) *jsonInventory {
	out := &jsonInventory{
		AccountID: inv.AccountID,
		Regions:   inv.Regions,
		ScannedAt: inv.ScannedAt,
		ScanMode:  inv.ScanMode,
		Groups:    make([]*jsonInfraGroup, 0, len(inv.Groups)),
	}

	for _, g := range inv.Groups {
		jg := &jsonInfraGroup{
			InfraID:     g.InfraID,
			ClusterName: g.ClusterName,
			Owner:       g.Owner,
			Category:    string(g.Category),
			Resources:   make([]*jsonResource, 0, len(g.Resources)),
			CreatedAt:   g.CreatedAt,
			MonthlyCost: g.MonthlyCost,
			HasVPC:      g.HasVPC,
			HasHC:       g.HasHC,
		}
		for i := range g.Resources {
			jg.Resources = append(jg.Resources, toJSONResource(&g.Resources[i]))
		}
		out.Groups = append(out.Groups, jg)
	}

	for i := range inv.Ungrouped {
		out.Ungrouped = append(out.Ungrouped, toJSONResource(&inv.Ungrouped[i]))
	}

	return out
}

func toJSONResource(r *scanner.Resource) *jsonResource {
	return &jsonResource{
		Type:        string(r.Type),
		ID:          r.ID,
		ARN:         r.ARN,
		Name:        r.Name,
		Region:      r.Region,
		InfraID:     r.InfraID,
		ClusterName: r.ClusterName,
		CreatedAt:   r.CreatedAt,
		Tags:        r.Tags,
		MonthlyCost: r.MonthlyCost,
		Details:     r.Details,
	}
}
