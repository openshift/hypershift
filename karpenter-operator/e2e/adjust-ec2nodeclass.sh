#!/usr/bin/env bash

FILE=$1

# delete metadata fields
yq 'del(.metadata.creationTimestamp, .metadata.generation, .metadata.managedFields, .metadata.resourceVersion, .metadata.selfLink, .metadata.uid)' -i "$FILE"

# delete status
yq 'del(.status)' -i "$FILE"
