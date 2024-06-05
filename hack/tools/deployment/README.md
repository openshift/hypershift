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

You can also pass a Jira ticket ID in. This will automatically create a git branch based on the ID and the env.
```
% go run deployment-update.go \
--file ~/Service\ Delivery/app-interface/data/services/ocm/osd-fleet-manager/cicd/deploy.yaml \
--env prod-group2 \
--old-tag v0.1.32 \
--new-tag v0.1.34 \
--new-commit d5b642b \
--jira-ticket HOSTEDCP-1111
```
The example branch created from this command would be `HOSTEDCP-1111-prod-group2`.