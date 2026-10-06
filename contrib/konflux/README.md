# Konflux release pipelines

HyperShift contributors directly manage the different Control Plane Operator
releases via [Konflux](https://konflux-ci.dev).

In order to simplify release creation, we use ProjectDevelopmentStreamTemplates
and ProjectDevelopmentStreams. In this directory, you should find:

* A ProjectDevelopmentStreamTemplate for each deliverable that we manage the
releases for. The naming convention is
`deliverablename_development_stream_template.yaml` for release streams and
`deliverablename_hotfix_stream_template.yaml` for hotfix streams.
* A ProjectDevelopmentStream for each of the releases of each deliverable. The
naming convention is `deliverablename_underscoredversionnumber_stream.yaml`.

## Interacting with the Konflux build cluster

HyperShift uses an internal RH OpenShift cluster with Konflux installed. To
perform actions like listing releases and creating ProjectDevelopmentStreams,
one needs to authenticate with it.

### Prerequisites

You must ensure to have installed:
* [oc](https://access.redhat.com/downloads/content/290/)

### Login

Authenticate with the Konflux cluster:

    oc login --web https://api.stone-prd-rh01.pg1f.p1.openshiftapps.com:6443

## Creating a new CPO release

For each managed deliverable, after branching happens, we should:

1. Create a ProjectDevelopmentStream that references the appropriate
ProjectDevelopmentStreamTemplate. The location for for each new
ProjectDevelopmentStream file should be the same directory that contains this
README.
2. Once it is merged, any maintainer should be able to use the **oc** tool
to apply the newly committed `deliverablename_development_stream_template.yaml`.
3. Merge the Konflux generated pull request that adds the `.tekton` pipeline for
the control plane operator release application.

Here you can see an example of a CPO release ProjectDevelopmentStream:

    apiVersion: projctl.konflux.dev/v1beta1
    kind: ProjectDevelopmentStream
    metadata:
      name: control-plane-operator-v4-20
    spec:
      project: crt-redhat-acm-tenant
      template:
        name: hypershift-cpo-template
        values:
        - name: version
          value: "4.20"

## Creating a new HyperShift Operator hotfix

For each hotfix, the following steps need to be taken:

1. **Identify the production commit.** Use `podman inspect` on the production
   image to find the git commit currently deployed:

       podman pull <production-image-pullspec>
       podman inspect <production-image-pullspec> | jq '.[0].Labels["vcs-ref"]'

   The production image is available in the
   `quay.io/redhat-services-prod/crt-redhat-acm-tenant/hypershift/hypershift-operator`
   repository.

2. **Create a hotfix branch** in the HyperShift repository named
   `ho-hotfix-jiraTicketReferenceLowercased` (e.g.
   `ho-hotfix-cntrlplane-3632`). The branch should contain the fix
   cherry-picked on top of the production commit identified in step 1:

       git checkout -b ho-hotfix-<ticket> <production-commit>
       git cherry-pick <fix-commit>
       git push origin ho-hotfix-<ticket>

3. **Create a ProjectDevelopmentStream** file referencing the
   `hypershift-ho-hotfix-template` ProjectDevelopmentStreamTemplate. The file
   should live in the same directory that contains this README (see example
   below) and be committed to the hotfix branch.

4. **Apply the ProjectDevelopmentStream** to the Konflux cluster so that
   Konflux creates the Application, Component, and ImageRepository:

       oc apply -f contrib/konflux/<stream-file>.yaml

5. **Merge the Konflux generated pull request** that adds the `.tekton`
   pipeline for the hotfix application. After merging, verify the pipeline
   uses the common build pipeline defined in
   `.tekton/pipelines/common-operator-build.yaml`. If the generated pipeline
   does not reference it, submit a follow-up commit to the hotfix branch to
   switch it (see commit `b84c88d1dd` on `ho-hotfix-cntrlplane-3632` for an
   example).

Here you can see an example of a hotfix ProjectDevelopmentStream:

    apiVersion: projctl.konflux.dev/v1beta1
    kind: ProjectDevelopmentStream
    metadata:
      name: hypershift-ho-hotfix-cntrlplane-3632
    spec:
      project: crt-redhat-acm-tenant
      template:
        name: hypershift-ho-hotfix-template
        values:
        - name: ticketReference
          value: "cntrlplane-3632"

## Listing HyperShift Operator releases

It is useful to be able to list our latest HyperShift Operator Konflux-built
releases. Here's a command to do just that without abandoning the comfort of the
cli:

    ❯ oc get release --sort-by=.metadata.creationTimestamp -l "appstudio.openshift.io/application=hypershift-operator"
    NAME                                      SNAPSHOT                    RELEASEPLAN                       RELEASE STATUS   AGE
    hypershift-operator-xkkxd-d606d00-v7vmk   hypershift-operator-xkkxd   hypershift-operator               Succeeded        21h
    hypershift-operator-xkkxd-d606d00-xglj6   hypershift-operator-xkkxd   hypershift-operator-progressive   Succeeded        21h
    hypershift-operator-t4dpx-fac7c6c-7gc6b   hypershift-operator-t4dpx   hypershift-operator               Succeeded        10h
    hypershift-operator-t4dpx-fac7c6c-2ndvz   hypershift-operator-t4dpx   hypershift-operator-progressive   Succeeded        10h

For HyperShift Operator hotfix releases created with the ProjectDevelopmentStreamTemplate, one would list them specifically:

    ❯ oc get release --sort-by=.metadata.creationTimestamp -l "appstudio.openshift.io/application=hypershift-operator-hotfix-cntrlplane-3632"

## Listing Control Plane Operator releases

When preparing a hotfix, it is important to identify Control Plane Operator(CPO)
releases, one can do so with the following command:

    ❯ oc get release --sort-by=.metadata.creationTimestamp -l "appstudio.openshift.io/application=control-plane-operator"
    NAME                                         SNAPSHOT                       RELEASEPLAN                          RELEASE STATUS   AGE
    control-plane-operator-pzlsr-ed43499-65xln   control-plane-operator-pzlsr   control-plane-operator-progressive   Succeeded        7d3h
    control-plane-operator-lgdfh-23964df-6z2ll   control-plane-operator-lgdfh   control-plane-operator-progressive   Succeeded        6d18h
    control-plane-operator-cswfh-9c2f0e2-zbsgj   control-plane-operator-cswfh   control-plane-operator-progressive   Succeeded        6d16h
    control-plane-operator-66lc6-bcd8aee-cwrgg   control-plane-operator-66lc6   control-plane-operator-progressive   Succeeded        6d10h
    control-plane-operator-btcms-a2f04a1-ksvlx   control-plane-operator-btcms   control-plane-operator-progressive   Succeeded        6d
    control-plane-operator-cs4kw-2648eee-fpmkq   control-plane-operator-cs4kw   control-plane-operator-progressive   Succeeded        5d20h
    control-plane-operator-6drzv-e0acd1d-42rwh   control-plane-operator-6drzv   control-plane-operator-progressive   Succeeded        5d19h
    control-plane-operator-9j7bd-7ed7c35-s7hv6   control-plane-operator-9j7bd   control-plane-operator-progressive   Succeeded        5d15h
    control-plane-operator-n88gb-1a80d92-4l7p7   control-plane-operator-n88gb   control-plane-operator-progressive   Succeeded        5d
    control-plane-operator-bcdth-5204154-4g7tk   control-plane-operator-bcdth   control-plane-operator-progressive   Succeeded        4d13h
    control-plane-operator-44r69-b20162c-cl98c   control-plane-operator-44r69   control-plane-operator-progressive   Succeeded        43h
    control-plane-operator-bt8wv-66d0916-svsj5   control-plane-operator-bt8wv   control-plane-operator-progressive   Succeeded        21h
    control-plane-operator-xlcxx-eaa4ab3-zclkl   control-plane-operator-xlcxx   control-plane-operator-progressive   Succeeded        13h
    control-plane-operator-rw5wg-fac7c6c-whf7s   control-plane-operator-rw5wg   control-plane-operator-progressive   Succeeded        10h

Note that for CPO releases <= 4.19, all the different versions are in the same
release, but built with different components. So the image for every release
will be listed in each of those releases when you select the yaml output in the
`oc` command.

For future releases created with the CPO ProjectDevelopmentStreamTemplate, one
would simply list the desired release:

    ❯ oc get release --sort-by=.metadata.creationTimestamp -l "appstudio.openshift.io/application=control-plane-operator-4-20"
