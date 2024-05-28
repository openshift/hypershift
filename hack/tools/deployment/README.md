# General
`deployment-update.go` will update the sectors within a particular ROSA environment with the new tag and the new commit SHA.

All flags are required. Here is an example of how to run the deployment-update.go: 
```
% go run deployment-update.go \
--file ~/Service\ Delivery/app-interface/data/services/ocm/osd-fleet-manager/cicd/deploy.yaml \
--env int \
--old-tag v0.1.32 \
--new-tag v0.1.33 \
--new-commit asdf

```