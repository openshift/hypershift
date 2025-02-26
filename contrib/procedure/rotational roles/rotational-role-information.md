# General
This document outlines the purposes, expectations, and general information for each of the rotational roles on the
HyperShift team. These roles are generally served by one engineer on a rotational period; this rotational period can
vary in duration depending on the role. Those roles are (as of 2025-02-25):

1. HyperShift Interrupt Catcher Role
2. HyperShift Operator Release Role
3. HyperShift BareMetal Role

A previous role, HyperShift Snyk Role, has been deprecated as it is no longer needed.

## HyperShift Interrupt Catcher Role
### Purpose
The purpose of this role is to triage and address (if you can) any requests in #project-hypershift or any service
delivery issues. This role helps other HyperShift team members concentrate on other work and avoid context switching
consistently.

### Expectations
* Address requests in #project-hypershift in a timely manner
* Prioritize any service delivery issues/incidents immediately

## HyperShift Operator Release Role
### Purpose
The purpose of this role is to ensure the team is involved in delivering new versions of the HyperShift Operator (HO) to
managed services on timely basis.

Since this role is not as time-consuming as some of the other roles, this role also triages and merges any Konflux or
Dependabot PRs opened in the HyperShift repo.

### Expectations
Daily:
* Triage and merge Konflux and/or Dependabot PRs
* Continue progressing new HO versions through the ROSA environments: integration > staging > prod-canary > prod groups 1 through 5

Every Monday & Wednesday:
* If there are new merged PRs in main and the branch is in a healthy state, put a merge request for the ROSA integration environment.
* Notify He Liu or Liangquan Li to run the tests when a new version deploys to one of:
    * Envs:
      * infra
      * stage
      * prod-canary
    * Alternatively you can run the tests using https://gitlab.cee.redhat.com/asegurap/qe-rosa-jobs
    * Review SLOs and report status by the end of the day. Suggest additions, improvements and maintain existing SLOs/SLIs.

Weekly:
* Let’s do the SLOs
  * Present the HyperShift team status on the Let’s do the SLOs meeting with SD.
    * https://grafana.stage.devshift.net/d/H8qOpjY4z/hypershift-team-slos?orgId=1&from=now-7d&to=now&var-datasource=hypershift-observatorium-stage
    * https://grafana.stage.devshift.net/d/H8qOpjY4z/hypershift-team-slos?orgId=1&from=now-7d&to=now&var-datasource=hypershift-observatorium-production
    * If we are not meeting the SLOs:
      * Check if we have pending SLO actions https://issues.redhat.com/issues/?filter=12430625
      * If none are there, raise the issue as an agenda item in the team weekly meeting or in #team-ocp-hypershift slack
      * Create the jira issue if necessary with the right priority and rank in the backlog
    * Post a summary of the spoken report (including the corrective actions agreed by the team)

At the end of your rotation in this role:
* Propose improvements to the role and the documentation.
* Handover to the next release person at the end of the sprint
* Add the new person to the @hypershift-release group
* Remove yourself from the @hypershift-release group

### Other Information
The original information related to this role is contained in [this document] (https://docs.google.com/document/d/1Q_H3auZdjYvD5Wea1s2qB9_zGlaCnoTxoL8cDot3nX8/edit?tab=t.0).

TBD - add minion stuff here

## HyperShift BareMetal Role
### Purpose
The purpose of this role is

### Expectations