package main

// resourceList is the generic Kubernetes list response wrapper.
type resourceList[T any] struct {
	Items []T `json:"items"`
}

// PipelineRun holds the fields we need from a Tekton PipelineRun.
type PipelineRun struct {
	Metadata ObjectMeta        `json:"metadata"`
	Status   PipelineRunStatus `json:"status"`
}

type ObjectMeta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	CreationTimestamp string            `json:"creationTimestamp"`
	Labels            map[string]string `json:"labels"`
	Annotations       map[string]string `json:"annotations"`
}

type PipelineRunStatus struct {
	Conditions []Condition `json:"conditions"`
	Results    []Result    `json:"results"`
}

type Condition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type Result struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Release holds the fields we need from a Konflux Release CR.
type Release struct {
	Metadata ObjectMeta    `json:"metadata"`
	Spec     ReleaseSpec   `json:"spec"`
	Status   ReleaseStatus `json:"status"`
}

type ReleaseSpec struct {
	ReleasePlan string `json:"releasePlan"`
	Snapshot    string `json:"snapshot"`
}

type ReleaseStatus struct {
	Conditions []Condition `json:"conditions"`
}

// ReleasePlan holds the fields we need from a Konflux ReleasePlan CR.
type ReleasePlan struct {
	Spec struct {
		Data struct {
			Mapping *Mapping `json:"mapping"`
		} `json:"data"`
	} `json:"spec"`
	Status struct {
		ReleasePlanAdmission struct {
			Name string `json:"name"`
		} `json:"releasePlanAdmission"`
	} `json:"status"`
}

// ReleasePlanAdmission holds the mapping data from a ReleasePlanAdmission CR.
type ReleasePlanAdmission struct {
	Spec struct {
		Data struct {
			Mapping *Mapping `json:"mapping"`
		} `json:"data"`
	} `json:"spec"`
}

// Mapping describes where container images are published.
type Mapping struct {
	Components []MappingComponent `json:"components"`
}

type MappingComponent struct {
	Name         string              `json:"name"`
	Repositories []MappingRepository `json:"repositories"`
}

type MappingRepository struct {
	URL string `json:"url"`
}

// Snapshot holds the component images from a Konflux Snapshot CR.
type Snapshot struct {
	Spec struct {
		Components []SnapshotComponent `json:"components"`
	} `json:"spec"`
}

type SnapshotComponent struct {
	Name           string `json:"name"`
	ContainerImage string `json:"containerImage"`
}

// pipelineRunStatus extracts the status reason from a PipelineRun.
// PipelineRuns use the Succeeded condition type, but we fall back to conditions[0]
// since Tekton typically has only one condition.
func pipelineRunStatus(pr PipelineRun) string {
	for _, c := range pr.Status.Conditions {
		if c.Type == "Succeeded" {
			return c.Reason
		}
	}
	if len(pr.Status.Conditions) > 0 {
		return pr.Status.Conditions[0].Reason
	}
	return "<none>"
}

// releaseStatus extracts the Released condition reason from a Release.
func releaseStatus(rel Release) string {
	for _, c := range rel.Status.Conditions {
		if c.Type == "Released" {
			return c.Reason
		}
	}
	return "<none>"
}

// pipelineRunImage constructs the IMAGE_URL@IMAGE_DIGEST string from results.
func pipelineRunImage(pr PipelineRun) string {
	var imageURL, imageDigest string
	for _, r := range pr.Status.Results {
		switch r.Name {
		case "IMAGE_URL":
			imageURL = r.Value
		case "IMAGE_DIGEST":
			imageDigest = r.Value
		}
	}
	if imageURL != "" && imageDigest != "" {
		return imageURL + "@" + imageDigest
	}
	if imageURL != "" {
		return imageURL
	}
	return ""
}
