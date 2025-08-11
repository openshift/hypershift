# Konflux release pipelines

HyperShift contributors directly manage the different Control Plane Operator
releases via [Konflux](https://konflux-ci.dev).

In order to simplify release creation, we use ProjectDevelopmentStreamTemplates
and ProjectDevelopmentStreams. In this directory, you should find:

* A ProjectDevelopmentStreamTemplate for each deliverable that we manage the
releases for. The naming convention is deliverablename_development_stream_template.yaml.
* A ProjectDevelopmentStream for each of the releases of each deliverable. The
naming convention is `deliverablename_underscoredversionnumber_stream.yaml`.

## Creating a new release

For each managed deliverable, after branching happens, we should create one
ProjectDevelopmentStream that references the appropriate
ProjectDevelopmentStreamTemplate. The location for for each new
ProjectDevelopmentStream file should be the same directory that contains this
README. Once it is merged, any maintainer should be able to use the **oc** tool
to apply the newly committed `deliverablename_development_stream_template.yaml`.
