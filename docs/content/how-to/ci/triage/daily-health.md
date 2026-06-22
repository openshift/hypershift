# Daily CI Health Check Procedures
This document outlines the daily checks that should be performed each morning to ensure the health and stability of our CI systems.

!!! tip

    Daily CI checks help identify issues early and ensure that our development pipeline remains stable. These checks should be performed at the start of each workday.

## 1. OCP Release Payload Controllers
Check the status of OpenShift Container Platform release payload controllers for the **current** and **previous** OCP versions to ensure they are functioning properly.

- Review [amd64 release payload controller](https://amd64.ocp.releases.ci.openshift.org/) to make sure HyperShift AWS and AKS jobs are passing, thus they are not blocking the CI and nightly payloads (both OCP versions)
- Review [multi-arch release payload controller](https://multi.ocp.releases.ci.openshift.org/) to make sure HyperShift AKS job is passing, thus it is not blocking the nightly payload (both OCP versions)
- Alternatively, you can view the same info on the Sippy Payload streams dashboard. [Here is an example](https://sippy.dptools.openshift.org/sippy-ng/release/4.21/streams) for OCP 4.21.

!!! warning - "HyperShift job is failing and blocking a payload release"

    When either HyperShift job is blocking a payload release:
    
    - Open a chat thread in #team-ocp-hypershift to start a dialogue on what is happening and to begin root causing the problem.
    - In addition, alert #forum-ocp-oversight we are aware of the issue and working to root cause the problem.

---

## 2. Periodic & Conformance Jobs
Review periodic job status for the **current** and **previous** OCP versions to ensure long-running validation and maintenance tasks are healthy. We want to be passing 70% or higher.

For each OCP version, click on the Jobs link on the left hand side of the screen in Sippy. [Here is an example for OCP 4.21](https://sippy.dptools.openshift.org/sippy-ng/jobs/4.21?filters=%257B%2522items%2522%253A%255B%257B%2522columnField%2522%253A%2522current_runs%2522%252C%2522operatorValue%2522%253A%2522%253E%253D%2522%252C%2522value%2522%253A%25227%2522%257D%252C%257B%2522columnField%2522%253A%2522variants%2522%252C%2522operatorValue%2522%253A%2522contains%2522%252C%2522value%2522%253A%2522never-stable%2522%252C%2522not%2522%253Atrue%257D%252C%257B%2522id%2522%253A99%252C%2522columnField%2522%253A%2522name%2522%252C%2522operatorValue%2522%253A%2522contains%2522%252C%2522value%2522%253A%2522hypershift%2522%257D%255D%257D&sort=asc&sortField=net_improvement) with the jobs filtered on *hypershift*.

We care about the following jobs (you can filter by these names if desired):

- AWS
    - periodic-ci-openshift-hypershift-release-*-periodics-e2e-aws-ovn-conformance
    - periodic-ci-openshift-hypershift-release-*-periodics-e2e-aws-upgrade
    - periodic-ci-openshift-hypershift-release-*-periodics-e2e-aws-multi
    - periodic-ci-openshift-hypershift-release-*-periodics-e2e-aws-ovn
- Azure / ARO HCP
    - periodic-ci-openshift-hypershift-release-*-periodics-e2e-aks
    - periodic-ci-openshift-hypershift-release-*-periodics-e2e-aks-multi-x-ax
    - periodic-ci-openshift-hypershift-release-*-periodics-e2e-azure-aks-ovn-conformance

!!! tip - "Tip - How to check the job test results"

    For any of these jobs, if you click on the running man emblem, Sippy will show you all the test runs. 
    For each of the test runs, you can click the Prow ship emblem to see the test results of the individual run.

!!! warning - "What to do when a job is permafailing"

    Open a chat thread in #team-ocp-hypershift to start a dialogue on what is happening and to begin root causing the problem.

Alternatively, you can view the job runs in [TestGrid](https://testgrid.k8s.io/redhat-hypershift#Summary).

---

## 3. Presubmit Jobs
Monitor presubmit job health for the **current OCP version only** to catch any systemic issues that could block development.

The best way to check to make sure the presubmit jobs are not permafailing are to look at a recent PR in the HyperShift repo and go to the job history of the specific job you want to review.

The presubmit jobs we most care about are:

- [pull-ci-openshift-hypershift-main-e2e-aws](https://prow.ci.openshift.org/job-history/gs/test-platform-results/pr-logs/directory/pull-ci-openshift-hypershift-main-e2e-aws)
- [pull-ci-openshift-hypershift-main-e2e-aws-upgrade-hypershift-operator](https://prow.ci.openshift.org/job-history/gs/test-platform-results/pr-logs/directory/pull-ci-openshift-hypershift-main-e2e-aws-upgrade-hypershift-operator)
- [pull-ci-openshift-hypershift-main-e2e-aks](https://prow.ci.openshift.org/job-history/gs/test-platform-results/pr-logs/directory/pull-ci-openshift-hypershift-main-e2e-aks)
- [pull-ci-openshift-hypershift-main-e2e-aks-4-20](https://prow.ci.openshift.org/job-history/gs/test-platform-results/pr-logs/directory/pull-ci-openshift-hypershift-main-e2e-aks-4-20)
- [pull-ci-openshift-hypershift-main-e2e-kubevirt-aws-ovn-reduced](https://prow.ci.openshift.org/job-history/gs/test-platform-results/pr-logs/directory/pull-ci-openshift-hypershift-main-e2e-kubevirt-aws-ovn-reduced)
- [pull-ci-openshift-hypershift-main-verify](https://prow.ci.openshift.org/job-history/gs/test-platform-results/pr-logs/directory/pull-ci-openshift-hypershift-main-verify)
- [pull-ci-openshift-hypershift-main-unit](https://prow.ci.openshift.org/job-history/gs/test-platform-results/pr-logs/directory/pull-ci-openshift-hypershift-main-unit)

!!! tip

    If the job is not solid red, the job is not permafailing.

!!! warning - "What to do when a job is permafailing"

    Open a chat thread in #team-ocp-hypershift to start a dialogue on what is happening and to begin root causing the problem.
