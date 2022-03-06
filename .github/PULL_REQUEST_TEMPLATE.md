<!--
- Please ensure code changes are split into a series of logically independent commits.
- Every commit should have a subject/title (What) and a description/body (Why).
- Every PR must have a description.
- As an example you can use git commit -m"What" -m"Why" to achieve the requirements above. GitHub automatically recognises the commit description (-m"Why") in single commit PRs and adds it as the PR description.
- Use the [imperative mood](https://en.wikipedia.org/wiki/Imperative_mood) in the subject line for every commit. E.g `Mark infraID as required` instead of `This patch marks infraID as required` (This follows Git’s own built-in conventions). See https://github.com/openshift/hypershift/pull/485 as an example.
- See https://hypershift-docs.netlify.app/contribute for more details.

Delete this text before submitting the PR.
-->

**What this PR does / why we need it**:

**Which issue(s) this PR fixes** *(optional, use `fixes #<issue_number>(, fixes #<issue_number>, ...)` format, where issue_number might be a GitHub issue, or a Jira story*:
Fixes #

**Checklist**
- [ ] Subject and description added to both, commit and PR.
- [ ] Relevant issues have been referenced.
- [ ] This change includes docs. 
- [ ] This change includes unit tests.


**Gotchas to lookout for**
- Label selectors on deployments/daemonsets are immutable: reconciliation will fail if you expect them to update in place
- roleRef in clusterRoleBindings/roleBindings are immutable: reconciliation will fail if you expect them to update in place.
- Changes to the core components (kube-apiserver, controller-manager, etc) PKI/Secrets should be reviewed by all stakeholders. This includes introducing new PKI for a component, changing the names of existing PKI, or anything similar.