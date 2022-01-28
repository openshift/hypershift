#!/usr/bin/env python

import sys
import yaml

usage="""
Usage: provide a list of yaml manifests on stdin and expect the template on stdout
"""

template = {
    "apiVersion": "v1",
    "kind": "Template",
    "metadata": {
        "name": "hypershift-saas-template"
    },
    "parameters": [
        {
            "name": "REGISTRY_IMG",
            "value": "quay.io/app-sre/hypershift-operator",
        },
        {
            "name": "IMAGE_TAG",
            "value": "latest",
        }
    ],
    "objects": []
}

# read objects
template["objects"] = list(yaml.load_all(sys.stdin, Loader=yaml.SafeLoader))

for obj in template["objects"]:
    # patch image
    if obj and obj["kind"] in "Deployment":
        for container in obj["spec"]["template"]["spec"]["containers"]:
            if container["image"] == "quay.io/app-sre/hypershift-operator:latest":
                container["image"] = '${REGISTRY_IMG}:${IMAGE_TAG}'

# write output
yaml.dump(template, sys.stdout, default_flow_style=False)
