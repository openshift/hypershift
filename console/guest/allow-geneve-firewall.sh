#!/usr/bin/env bash
set -euo pipefail

# Guest VPC firewall fix: allow OVN-Kubernetes geneve (UDP 6081) between nodes.
#
# The guest VPC (patmart-b3bb-network) shipped with only tcp:10250 (kubelet)
# allowed internally; GCP implied-deny dropped everything else, including the
# OVN-K overlay tunnels. Result: all CROSS-NODE pod networking was broken
# (geneve tx>0/rx=0; pod->pod cross-node 100% fail, same-node OK), which
# cascaded into konnectivity 504s and blocked control-plane-side console
# monitoring. See CONSOLE_CONTROL_PLANE_DOCS (Phase 2).
#
# This is the minimal unblock (geneve only). A real fix belongs in HyperShift's
# GCP infra provisioning (this rule should be created with the cluster) and will
# be folded into a Go tool later; kept here as a reminder + manual repro.

PROJECT=patmarti-hcp-test
NETWORK=patmart-b3bb-network
NODE_CIDR=10.0.0.0/24
NAME=patmart-b3bb-allow-geneve

gcloud compute firewall-rules create "$NAME" \
  --project="$PROJECT" \
  --network="$NETWORK" \
  --direction=INGRESS \
  --action=ALLOW \
  --rules=udp:6081 \
  --source-ranges="$NODE_CIDR"

echo "Created $NAME (udp:6081 from $NODE_CIDR on $NETWORK)"
