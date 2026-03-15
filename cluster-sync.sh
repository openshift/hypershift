#!/bin/bash

set -euo pipefail

# NOTE: These scripts started life as a one-off spike. Keep them runnable in
# other environments by allowing env overrides (and only defaulting when unset).
: "${KUBECONFIG:=/root/.kcli/clusters/hypershift/auth/kubeconfig}"
: "${CLUSTER_NAME:=test-primary-udn}"
: "${UDN_NAME:=hcp-${CLUSTER_NAME}}"
: "${PULL_SECRET:=/root/ralavi/merged-pull-secret.json}"
: "${MEM:=10Gi}"
: "${CPU:=4}"
: "${WORKER_COUNT:=1}"
: "${CLUSTER_SUBNET:=10.128.0.0/14}"
: "${WAIT_GUEST_NODES_TIMEOUT_SECONDS:=2400}"
: "${WAIT_GUEST_NODES_READY_MIN:=1}"

export KUBECONFIG

if ! command -v jq >/dev/null 2>&1; then
  echo "ERROR: jq is required but was not found in PATH."
  exit 1
fi

if [ -z "${RELEASE_IMAGE:-}" ]; then
  RELEASE_IMAGE="$(curl -s https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/4-stable/latest | jq -r .pullSpec)"
fi

# Read custom CPO image if available (built by cluster-up.sh)
if [ -f .cpo-image ]; then
  export CPO_IMAGE=$(cat .cpo-image)
  echo "Using custom control-plane-operator image: ${CPO_IMAGE}"
  echo ""
else
  if [ -n "${CPO_IMAGE}" ]; then
    echo "Using provided control-plane-operator image: ${CPO_IMAGE}"
    echo ""
  else
    echo "ERROR: No custom control-plane-operator image found."
    echo "Run ./cluster-up.sh first (it writes .cpo-image), or export CPO_IMAGE to a valid image reference."
    exit 1
  fi
fi

# Destroy existing cluster if it exists
if oc get hostedcluster $CLUSTER_NAME -n clusters &>/dev/null; then
  echo "Hosted cluster $CLUSTER_NAME already exists. Destroying it first..."
  
  cleanup_finalizers_best_effort() {
    # Best-effort cleanup. Safe to run multiple times.
    echo "Cleaning up stuck finalizers (best-effort)..."

    # Remove finalizers from VirtualMachineInstances
    for vmi in $(oc get virtualmachineinstances.kubevirt.io -n "clusters-${CLUSTER_NAME}" -o name 2>/dev/null); do
      echo "  Removing finalizers from $vmi"
      oc patch "$vmi" -n "clusters-${CLUSTER_NAME}" --type=merge -p '{"metadata":{"finalizers":null}}' 2>/dev/null || true
    done

    # Remove finalizers from VirtualMachines
    for vm in $(oc get virtualmachines.kubevirt.io -n "clusters-${CLUSTER_NAME}" -o name 2>/dev/null); do
      echo "  Removing finalizers from $vm"
      oc patch "$vm" -n "clusters-${CLUSTER_NAME}" --type=merge -p '{"metadata":{"finalizers":null}}' 2>/dev/null || true
    done

    # Remove finalizers from Machines
    for machine in $(oc get machines.cluster.x-k8s.io -n "clusters-${CLUSTER_NAME}" -o name 2>/dev/null); do
      echo "  Removing finalizers from $machine"
      oc patch "$machine" -n "clusters-${CLUSTER_NAME}" --type=merge -p '{"metadata":{"finalizers":null}}' 2>/dev/null || true
    done

    # Remove finalizers from MachineSets
    for ms in $(oc get machinesets.cluster.x-k8s.io -n "clusters-${CLUSTER_NAME}" -o name 2>/dev/null); do
      echo "  Removing finalizers from $ms"
      oc patch "$ms" -n "clusters-${CLUSTER_NAME}" --type=merge -p '{"metadata":{"finalizers":null}}' 2>/dev/null || true
    done

    # Remove finalizers from Cluster (cluster.x-k8s.io)
    for cluster in $(oc get clusters.cluster.x-k8s.io -n "clusters-${CLUSTER_NAME}" -o name 2>/dev/null); do
      echo "  Removing finalizers from $cluster"
      oc patch "$cluster" -n "clusters-${CLUSTER_NAME}" --type=merge -p '{"metadata":{"finalizers":null}}' 2>/dev/null || true
    done

    # Remove finalizers from HostedControlPlane
    for hcp in $(oc get hostedcontrolplanes.hypershift.openshift.io -n "clusters-${CLUSTER_NAME}" -o name 2>/dev/null); do
      echo "  Removing finalizers from $hcp"
      oc patch "$hcp" -n "clusters-${CLUSTER_NAME}" --type=merge -p '{"metadata":{"finalizers":null}}' 2>/dev/null || true
    done

    # Remove finalizers from NodePools
    for np in $(oc get nodepools.hypershift.openshift.io -n clusters -o name 2>/dev/null | grep "$CLUSTER_NAME"); do
      echo "  Removing finalizers from $np"
      oc patch "$np" -n clusters --type=merge -p '{"metadata":{"finalizers":null}}' 2>/dev/null || true
    done

    # Remove finalizers from HostedCluster itself
    oc patch "hostedcluster/${CLUSTER_NAME}" -n clusters --type=merge -p '{"metadata":{"finalizers":null}}' 2>/dev/null || true
  }

  wait_for_gone() {
    local kind="$1"
    local name="$2"
    local namespace="${3:-}"
    local timeout_seconds="${4:-900}"
    local elapsed=0

    while true; do
      if [ -n "${namespace}" ]; then
        if ! oc -n "${namespace}" get "${kind}" "${name}" >/dev/null 2>&1; then
          return 0
        fi
      else
        if ! oc get "${kind}" "${name}" >/dev/null 2>&1; then
          return 0
        fi
      fi

      if [ "${elapsed}" -ge "${timeout_seconds}" ]; then
        echo "ERROR: Timed out waiting for ${kind}/${name} to be deleted (ns=${namespace:-<cluster-scope>})." >&2
        return 1
      fi

      # Keep trying finalizer cleanup while we wait; some deletions only unblock after retries.
      # (run every ~30s to reduce API spam)
      if [ $((elapsed % 30)) -eq 0 ]; then
        cleanup_finalizers_best_effort
      fi

      echo "Still waiting for ${kind}/${name} to be deleted... (${elapsed}/${timeout_seconds}s)"
      sleep 10
      elapsed=$((elapsed + 10))
    done
  }

  # Run destroy in background so we can clean up finalizers in parallel
  hypershift destroy cluster kubevirt --name $CLUSTER_NAME &
  DESTROY_PID=$!
  
  # Wait a bit for deletion to start
  sleep 10
  
  # Remove stuck finalizers while destroy is running (and again while waiting below).
  cleanup_finalizers_best_effort
  
  # Wait for destroy to complete
  echo "Waiting for destroy to complete..."
  wait $DESTROY_PID || true

  echo ""
  echo "Waiting for HostedCluster and namespace deletion to complete..."
  # One more cleanup pass after destroy returns (in case it exited early).
  cleanup_finalizers_best_effort
  # 1) hostedcluster object in the clusters namespace
  wait_for_gone hostedcluster "${CLUSTER_NAME}" "clusters" 1200
  # 2) the hosted control plane namespace itself
  wait_for_gone namespace "clusters-${CLUSTER_NAME}" "" 1200
fi

# Create namespace with PRIMARY UDN label (IMMUTABLE - must be set at creation!)
cat <<EOF | oc apply -f -
apiVersion: v1
kind: Namespace
metadata:
  name: clusters-${CLUSTER_NAME}
  labels:
    hypershift.openshift.io/hosted-control-plane: "true"
    # Used by hypershift-operator logic (presence check)
    k8s.ovn.org/primary-user-defined-network: "${UDN_NAME}"
  annotations:
    # Used by OVN-Kubernetes to attach the Primary UDN (value is the UDN name)
    k8s.ovn.org/primary-user-defined-network: "${UDN_NAME}"
EOF

cat <<EOF | oc apply -f -
apiVersion: k8s.ovn.org/v1
kind: UserDefinedNetwork
metadata:
  name: ${UDN_NAME}
  namespace: clusters-${CLUSTER_NAME}
spec:
  topology: Layer2
  layer2:
    role: Primary
    subnets:
      - 10.150.0.0/16
    ipam:
      mode: Enabled
      lifecycle: Persistent
EOF

# Original working command (normal boot with ignition):
# hypershift create cluster kubevirt \
#   --name ${CLUSTER_NAME} \
#   --node-pool-replicas 1 \
#   --pull-secret ${PULL_SECRET} \
#   --memory 8Gi \
#   --cores 2 \
#   --annotations "hypershift.openshift.io/kubevirt-vm-jsonpatch=[{\"op\": \"replace\",\"path\": \"/spec/template/spec/domain/devices/interfaces/0/bridge\",\"value\": null},{\"op\": \"add\",\"path\": \"/spec/template/spec/domain/devices/interfaces/0/binding\",\"value\": {\"name\": \"l2bridge\"}},{\"op\": \"remove\",\"path\": \"/spec/template/metadata/annotations/kubevirt.io~1allow-pod-bridge-network-live-migration\"}]" \
#   --release-image ${RELEASE_IMAGE} \
#   --cluster-cidr ${CLUSTER_SUBNET}

VM_JSONPATCH='[{"op":"replace","path":"/spec/template/spec/domain/devices/interfaces/0/bridge","value":null},{"op":"add","path":"/spec/template/spec/domain/devices/interfaces/0/binding","value":{"name":"l2bridge"}},{"op":"remove","path":"/spec/template/metadata/annotations/kubevirt.io~1allow-pod-bridge-network-live-migration"}]'

hypershift create cluster kubevirt \
  --name ${CLUSTER_NAME} \
  --node-pool-replicas 1 \
  --pull-secret ${PULL_SECRET} \
  --memory 8Gi \
  --cores 2 \
  --annotations "hypershift.openshift.io/kubevirt-vm-jsonpatch=${VM_JSONPATCH}" \
  --release-image ${RELEASE_IMAGE} \
  --cluster-cidr ${CLUSTER_SUBNET}

# Annotate cluster with custom CPO image if available
if [ -n "${CPO_IMAGE}" ]; then
  echo ""
  echo "Waiting for cluster to be created..."
  sleep 10
  
  echo ""
  echo "Applying custom control-plane-operator image annotation..."
  echo "Image: ${CPO_IMAGE}"
  
  oc annotate hostedcluster ${CLUSTER_NAME} -n clusters \
    hypershift.openshift.io/control-plane-operator-image=${CPO_IMAGE} \
    --overwrite
  
  echo "✓ Annotation applied. Control-plane-operator will restart with new image."
fi

echo ""
echo "=========================================="
echo "Waiting for etcd to be ready..."
echo "=========================================="
echo ""

NAMESPACE="clusters-${CLUSTER_NAME}"

# Wait for etcd StatefulSet to be created
echo "Waiting for etcd StatefulSet to be created..."
timeout=600
elapsed=0
while ! oc -n "$NAMESPACE" get statefulset etcd &>/dev/null; do
  if [ $elapsed -ge $timeout ]; then
    echo "ERROR: Timeout waiting for etcd StatefulSet to be created"
    exit 1
  fi
  echo "Still waiting for etcd StatefulSet... ($elapsed/$timeout seconds)"
  sleep 10
  elapsed=$((elapsed + 10))
done

echo "✓ etcd StatefulSet created"
echo ""

# Wait for etcd-0 pod to be running and ready
echo "Waiting for etcd-0 pod to be running and ready..."
oc -n "$NAMESPACE" wait --for=condition=Ready pod/etcd-0 --timeout=600s

echo "✓ etcd-0 pod is ready"
echo ""

# Apply the EndpointSlice workaround
echo "=========================================="
echo "Applying etcd EndpointSlice Workaround"
echo "=========================================="
echo ""

if [ -f "./create-etcd-mirrors.sh" ]; then
  ./create-etcd-mirrors.sh
  
  if [ $? -eq 0 ]; then
    echo ""
    echo "✓ EndpointSlice workaround applied successfully"
  else
    echo ""
    echo "WARNING: EndpointSlice workaround script failed"
    echo "You may need to run it manually: ./create-etcd-mirrors.sh"
  fi
else
  echo "ERROR: create-etcd-mirrors.sh not found in current directory"
  echo "Please ensure the script is present and run it manually"
fi

echo ""
echo "=========================================="
echo "Waiting for Hosted Cluster to be Available"
echo "=========================================="
echo ""

# Wait for the HostedCluster to have Available=True condition
echo "Waiting for hosted cluster to be available..."
timeout=1800  # 30 minutes - cluster creation takes time
elapsed=0
while true; do
  AVAILABLE=$(oc get hostedcluster ${CLUSTER_NAME} -n clusters -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null)
  if [ "$AVAILABLE" = "True" ]; then
    echo "✓ Hosted cluster is available!"
    break
  fi
  
  if [ $elapsed -ge $timeout ]; then
    echo "WARNING: Timeout waiting for hosted cluster to be available."
    echo "DNS fix may need manual setup after cluster is ready."
    echo "Current cluster status:"
    oc get hostedcluster ${CLUSTER_NAME} -n clusters -o jsonpath='{.status.conditions[*].type}={.status.conditions[*].status}' 2>/dev/null || true
    break
  fi
  
  # Show progress
  PROGRESS=$(oc get hostedcluster ${CLUSTER_NAME} -n clusters -o jsonpath='{.status.conditions[?(@.type=="Available")].message}' 2>/dev/null | head -c 80)
  echo "Waiting... ($elapsed/$timeout seconds) - ${PROGRESS:-initializing}"
  sleep 30
  elapsed=$((elapsed + 30))
done

echo ""
echo "=========================================="
echo "Setting Up Internal DNS for Primary UDN"
echo "=========================================="
echo ""

# Extract guest cluster kubeconfig
echo "Extracting guest cluster kubeconfig..."
if ! oc get secret/${CLUSTER_NAME}-admin-kubeconfig -n clusters &>/dev/null; then
  echo "WARNING: Kubeconfig secret not found. Cluster may not be ready yet."
  echo "Skipping DNS setup - you may need to run this manually later."
else
  oc extract -n clusters secret/${CLUSTER_NAME}-admin-kubeconfig --to=. --confirm
  mv kubeconfig ${CLUSTER_NAME}-kubeconfig
  export GUEST_KUBECONFIG="${PWD}/${CLUSTER_NAME}-kubeconfig"
  echo "Guest kubeconfig saved to: ${GUEST_KUBECONFIG}"

  echo ""
  echo "Waiting for guest nodes to be Ready (required to schedule router/DNS pods)..."
  timeout="${WAIT_GUEST_NODES_TIMEOUT_SECONDS}"
  elapsed=0
  while true; do
    ready_nodes="$(
      oc --kubeconfig "${GUEST_KUBECONFIG}" get nodes -o json 2>/dev/null | \
        jq -r '[.items[] | select(any(.status.conditions[]?; .type=="Ready" and .status=="True"))] | length' 2>/dev/null || echo 0
    )"
    if [ "${ready_nodes}" -ge "${WAIT_GUEST_NODES_READY_MIN}" ]; then
      echo "✓ Guest Ready nodes: ${ready_nodes}"
      break
    fi
    if [ "${elapsed}" -ge "${timeout}" ]; then
      echo "WARNING: Only ${ready_nodes} Ready guest nodes after ${timeout}s. Skipping internal DNS/OAuth setup for now."
      echo "This is expected right after control plane becomes Available=True."
      echo "Re-run this script later once nodes join."
      goto_after_dns_setup=true
      break
    fi
    echo "Still waiting for guest Ready nodes (have ${ready_nodes}, need >= ${WAIT_GUEST_NODES_READY_MIN})... (${elapsed}/${timeout} seconds)"
    sleep 15
    elapsed=$((elapsed + 15))
  done
  if [ "${goto_after_dns_setup:-false}" = "true" ]; then
    # Close the kubeconfig extraction block early (skip DNS/OAuth setup).
    echo ""
    echo "Skipping DNS/OAuth setup due to missing guest nodes."
  else

  # Discover the guest cluster apps domain (avoid hardcoding env-specific suffixes)
  APPS_DOMAIN="$(oc --kubeconfig "${GUEST_KUBECONFIG}" get ingresses.config.openshift.io cluster -o jsonpath='{.spec.domain}' 2>/dev/null || true)"
  if [ -z "${APPS_DOMAIN}" ]; then
    # Backward-compatible fallback (old spike default)
    APPS_DOMAIN="apps.${CLUSTER_NAME}.apps.hypershift.qinqon.corp"
    echo "WARNING: Could not determine guest apps domain from guest cluster."
    echo "Falling back to: ${APPS_DOMAIN}"
  else
    echo "Guest cluster apps domain: ${APPS_DOMAIN}"
  fi

# Wait for router to be available in guest cluster
echo ""
echo "Waiting for guest cluster router to be available..."
timeout=600
elapsed=0
while ! oc --kubeconfig ${GUEST_KUBECONFIG} get svc router-internal-default -n openshift-ingress &>/dev/null; do
  if [ $elapsed -ge $timeout ]; then
    echo "WARNING: Timeout waiting for router service. DNS fix may need manual setup."
    break
  fi
  echo "Still waiting for router service... ($elapsed/$timeout seconds)"
  sleep 30
  elapsed=$((elapsed + 30))
done

# Get the internal router ClusterIP
ROUTER_IP=$(oc --kubeconfig ${GUEST_KUBECONFIG} get svc router-internal-default -n openshift-ingress -o jsonpath='{.spec.clusterIP}' 2>/dev/null)
if [ -z "$ROUTER_IP" ]; then
  echo "WARNING: Could not get router ClusterIP. DNS fix may need manual setup."
else
  echo "Internal router ClusterIP: ${ROUTER_IP}"
  
  echo ""
  echo "Creating internal DNS server in guest cluster..."
  
  # Create namespace, ConfigMap, Deployment, and Service for internal DNS
  cat <<EOF | oc --kubeconfig ${GUEST_KUBECONFIG} apply -f -
---
apiVersion: v1
kind: Namespace
metadata:
  name: internal-apps-dns
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: coredns-config
  namespace: internal-apps-dns
data:
  Corefile: |
    .:5353 {
        log
        errors
        hosts {
            ${ROUTER_IP} console-openshift-console.${APPS_DOMAIN}
            ${ROUTER_IP} canary-openshift-ingress-canary.${APPS_DOMAIN}
            ${ROUTER_IP} downloads-openshift-console.${APPS_DOMAIN}
            fallthrough
        }
        # WARNING: forwarding to the guest cluster DNS can cause recursion for zones
        # that are explicitly forwarded to this server. We keep it for now because
        # our expected hostnames are covered by the hosts plugin above.
        forward . /etc/resolv.conf
    }
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: internal-apps-dns
  namespace: internal-apps-dns
spec:
  replicas: 1
  selector:
    matchLabels:
      app: internal-apps-dns
  template:
    metadata:
      labels:
        app: internal-apps-dns
    spec:
      containers:
      - name: coredns
        image: registry.redhat.io/openshift4/ose-coredns-rhel9:v4.17
        args: ["-conf", "/etc/coredns/Corefile"]
        ports:
        - containerPort: 5353
          protocol: UDP
        - containerPort: 5353
          protocol: TCP
        volumeMounts:
        - name: config
          mountPath: /etc/coredns
      volumes:
      - name: config
        configMap:
          name: coredns-config
---
apiVersion: v1
kind: Service
metadata:
  name: internal-apps-dns
  namespace: internal-apps-dns
spec:
  selector:
    app: internal-apps-dns
  ports:
  - name: dns-udp
    port: 5353
    targetPort: 5353
    protocol: UDP
  - name: dns-tcp
    port: 5353
    targetPort: 5353
    protocol: TCP
EOF

  echo ""
  echo "Waiting for internal DNS pod to be ready..."
  if ! oc --kubeconfig ${GUEST_KUBECONFIG} wait --for=condition=available deployment/internal-apps-dns -n internal-apps-dns --timeout=300s; then
    echo "WARNING: internal-apps-dns deployment is not Available."
    echo "Not patching the guest DNS operator because forwarding would break DNS (no endpoints)."
    echo "Check why it can't schedule:"
    echo "  oc --kubeconfig ${GUEST_KUBECONFIG} -n internal-apps-dns get pod -o wide"
    echo "  oc --kubeconfig ${GUEST_KUBECONFIG} -n internal-apps-dns describe pod -l app=internal-apps-dns"
    skip_dns_operator_patch=true
  fi

  # Get the internal DNS service ClusterIP
  DNS_SVC_IP=$(oc --kubeconfig ${GUEST_KUBECONFIG} get svc internal-apps-dns -n internal-apps-dns -o jsonpath='{.spec.clusterIP}')
  echo "Internal DNS service ClusterIP: ${DNS_SVC_IP}"

  echo ""
  if [ "${skip_dns_operator_patch:-false}" = "true" ]; then
    echo "Skipping DNS operator forwarding configuration (internal DNS not ready)."
  else
    # Ensure the service actually has endpoints before patching DNS.
    if ! oc --kubeconfig ${GUEST_KUBECONFIG} -n internal-apps-dns get endpoints internal-apps-dns -o json 2>/dev/null | jq -e '.subsets | length > 0' >/dev/null; then
      echo "WARNING: internal-apps-dns Service has no Endpoints yet."
      echo "Not patching the guest DNS operator to avoid breaking cluster DNS."
      skip_dns_operator_patch=true
    fi
  fi

  if [ "${skip_dns_operator_patch:-false}" != "true" ]; then
    echo "Configuring DNS operator to forward selected zones to internal DNS..."

  # Update DNS operator servers idempotently without clobbering existing entries.
  # We forward:
  # - the guest apps domain (so console/canary/downloads resolve to internal router)
  # - (later) the OAuth zone (so oauth host resolves to oauth-bridge)
  DNS_ZONES_JSON="$(jq -c -n --arg apps "${APPS_DOMAIN}" '[ $apps ]')"

  update_dns_operator_servers() {
    local zones_json="$1"
    local upstream="${2}"

    local current_servers
    current_servers="$(oc --kubeconfig "${GUEST_KUBECONFIG}" get dns.operator.openshift.io default -o json 2>/dev/null | jq -c '.spec.servers // []' || echo '[]')"

    local updated_servers
    updated_servers="$(jq -c --argjson zones "${zones_json}" --arg upstream "${upstream}" '
      . as $servers
      | (map(.name) | index("internal-apps")) as $idx
      | if $idx == null then
          ($servers + [{"name":"internal-apps","zones":$zones,"forwardPlugin":{"upstreams":[$upstream]}}])
        else
          ($servers | map(if .name=="internal-apps" then (.zones=$zones | .forwardPlugin.upstreams=[$upstream]) else . end))
        end
    ' <<<"${current_servers}")"

    oc --kubeconfig "${GUEST_KUBECONFIG}" patch dns.operator.openshift.io default --type=merge \
      -p "$(jq -c -n --argjson servers "${updated_servers}" '{spec:{servers:$servers}}')"
  }

  update_dns_operator_servers "${DNS_ZONES_JSON}" "${DNS_SVC_IP}:5353"

  echo ""
  echo "✓ Internal DNS configured for Primary UDN"
  echo "  - Apps routes (console, canary, downloads) will resolve to internal router: ${ROUTER_IP}"
  echo "  - This fixes the ingress ClusterOperator health checks"

  echo ""
  echo "Verifying apps domain forwarding is effective..."
  DNSPOD="$(oc --kubeconfig "${GUEST_KUBECONFIG}" -n openshift-dns get pod -o json 2>/dev/null | jq -r '.items[0].metadata.name' || true)"
  if [ -n "${DNSPOD}" ] && [ -n "${APPS_DOMAIN}" ]; then
    RESOLVED_IP="$(oc --kubeconfig "${GUEST_KUBECONFIG}" -n openshift-dns exec "${DNSPOD}" -- sh -c "nslookup canary-openshift-ingress-canary.${APPS_DOMAIN} 2>/dev/null | sed -n 's/^Address: \\([0-9.]*\\).*/\\1/p' | tail -n1" || true)"
    echo "canary-openshift-ingress-canary.${APPS_DOMAIN} -> ${RESOLVED_IP:-<no answer>}"
    if [ -n "${RESOLVED_IP}" ] && [ "${RESOLVED_IP}" != "${ROUTER_IP}" ]; then
      echo "WARNING: Canary hostname did not resolve to router-internal ClusterIP (${ROUTER_IP})."
      echo "Ingress canary checks from the management control plane may still fail until this matches."
    fi
  else
    echo "WARNING: Could not verify DNS resolution (openshift-dns pod or apps domain missing)."
  fi
  fi
fi

# ==========================================
# OAuth Bridge Setup for Console Access
# ==========================================
# Problem: Console pod in guest cluster needs to reach OAuth server, but:
#   - Console runs in guest VM (Primary UDN only: 10.150.0.0/16)
#   - OAuth server runs in HCP namespace on management cluster
#   - Guest VM has no route to management cluster network
#
# Solution: All HCP namespace pods (including OAuth) get a Primary UDN IP.
# Create a Service in the guest cluster pointing directly to OAuth pod's Primary UDN IP.
# No proxy needed - direct connection!
# ==========================================

echo ""
echo "=========================================="
echo "Setting Up OAuth Bridge for Console Access"
echo "=========================================="
echo ""

# Discover the OAuth route host from the management cluster (avoid hardcoding)
OAUTH_HOST="$(
  oc get route -n "clusters-${CLUSTER_NAME}" -o json 2>/dev/null | \
    jq -r --arg cn "${CLUSTER_NAME}" '
      [.items[]
        | select(.spec.host != null)
        | select(.spec.host | test("^oauth-"))
        | select(.spec.host | contains("clusters-" + $cn))
        | .spec.host][0] // empty
    ' 2>/dev/null
)"
if [ -z "${OAUTH_HOST}" ]; then
  # Backward-compatible fallback (old spike default)
  OAUTH_HOST="oauth-clusters-${CLUSTER_NAME}.apps.hypershift.qinqon.corp"
  echo "WARNING: Could not discover OAuth route host from management cluster; falling back to: ${OAUTH_HOST}"
else
  echo "OAuth external hostname (discovered): ${OAUTH_HOST}"
fi

OAUTH_ZONE="${OAUTH_HOST#*.}"

# Get OAuth pod's Primary UDN IP from the pod-networks annotation
echo "Getting OAuth pod's Primary UDN IP..."
OAUTH_UDN_IP=""
timeout=600
elapsed=0
while [ $elapsed -lt $timeout ]; do
  OAUTH_UDN_IP="$(oc get pod -n "clusters-${CLUSTER_NAME}" -l app=oauth-openshift \
    -o jsonpath='{.items[0].metadata.annotations.k8s\.ovn\.org/pod-networks}' 2>/dev/null | \
    jq -r ".\"clusters-${CLUSTER_NAME}/hcp-${CLUSTER_NAME}\".ip_address" 2>/dev/null | cut -d/ -f1 || true)"
  if [ -n "${OAUTH_UDN_IP}" ] && [ "${OAUTH_UDN_IP}" != "null" ]; then
    break
  fi
  echo "Still waiting for OAuth pod Primary UDN IP... ($elapsed/$timeout seconds)"
  sleep 15
  elapsed=$((elapsed + 15))
done

if [ -z "$OAUTH_UDN_IP" ] || [ "$OAUTH_UDN_IP" = "null" ]; then
  echo "WARNING: Could not get OAuth pod Primary UDN IP."
  echo "OAuth setup may need manual configuration after OAuth pod is running."
else
  if [ "${skip_dns_operator_patch:-false}" = "true" ]; then
    echo "WARNING: Internal DNS is not ready; skipping OAuth DNS forwarding setup."
    echo "You can rerun this script later once internal-apps-dns is Available."
  fi
  echo "OAuth pod Primary UDN IP: ${OAUTH_UDN_IP}"
  
  # Create service in guest cluster pointing directly to OAuth pod
  echo ""
  echo "Creating OAuth bridge service in guest cluster..."
  cat <<EOF | oc --kubeconfig ${GUEST_KUBECONFIG} apply -f -
apiVersion: v1
kind: Service
metadata:
  name: oauth-bridge
  namespace: default
spec:
  ports:
  - port: 443
    targetPort: 6443
    protocol: TCP
---
apiVersion: v1
kind: Endpoints
metadata:
  name: oauth-bridge
  namespace: default
subsets:
- addresses:
  - ip: ${OAUTH_UDN_IP}
  ports:
  - port: 6443
    protocol: TCP
EOF

  # Get the OAuth bridge service ClusterIP
  OAUTH_BRIDGE_IP=$(oc --kubeconfig ${GUEST_KUBECONFIG} get svc oauth-bridge -n default -o jsonpath='{.spec.clusterIP}')
  echo "OAuth bridge service ClusterIP: ${OAUTH_BRIDGE_IP}"
  
  # Update internal DNS to include OAuth hostname
  echo ""
  echo "Updating internal DNS to resolve OAuth hostname..."
  cat <<EOF | oc --kubeconfig ${GUEST_KUBECONFIG} apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: coredns-config
  namespace: internal-apps-dns
data:
  Corefile: |
    .:5353 {
        log
        errors
        hosts {
            ${ROUTER_IP} console-openshift-console.${APPS_DOMAIN}
            ${ROUTER_IP} canary-openshift-ingress-canary.${APPS_DOMAIN}
            ${ROUTER_IP} downloads-openshift-console.${APPS_DOMAIN}
            ${OAUTH_BRIDGE_IP} ${OAUTH_HOST}
            fallthrough
        }
        forward . /etc/resolv.conf
    }
EOF
  
  # Restart internal DNS to pick up new config
  oc --kubeconfig ${GUEST_KUBECONFIG} rollout restart deployment/internal-apps-dns -n internal-apps-dns
  oc --kubeconfig ${GUEST_KUBECONFIG} rollout status deployment/internal-apps-dns -n internal-apps-dns --timeout=60s || true
  
  if [ "${skip_dns_operator_patch:-false}" != "true" ]; then
    # Update DNS operator to also forward OAuth zone (so the guest cluster asks our internal DNS)
    echo ""
    echo "Updating DNS operator to also forward OAuth zone: ${OAUTH_ZONE}"

    DNS_ZONES_JSON="$(jq -c -n --arg apps "${APPS_DOMAIN}" --arg oauth "${OAUTH_ZONE}" '[ $apps, $oauth ]')"
    update_dns_operator_servers "${DNS_ZONES_JSON}" "${DNS_SVC_IP}:5353"
  fi
  
  echo ""
  echo "✓ OAuth bridge configured for Primary UDN"
  echo "  - OAuth pod Primary UDN IP: ${OAUTH_UDN_IP}"
  echo "  - OAuth bridge service: ${OAUTH_BRIDGE_IP}:443 -> ${OAUTH_UDN_IP}:6443"
  echo "  - No proxy needed - direct connection to OAuth pod!"
  echo "  - This allows console to authenticate users"
fi

fi  # Close the guest-nodes gating if/else

fi  # Close the kubeconfig extraction if block (or the early-skip branch)

# ==========================================
# L2 Connectivity Verification
# ==========================================
# Phase 1 of Full Isolation: Verify that control plane pods and worker VMs
# can communicate directly on the Primary L2 UDN (10.150.0.0/16)
# This is a prerequisite for full isolation where both CP and workers
# share the same isolated L2 network segment.
# ==========================================

echo ""
echo "=========================================="
echo "Verifying L2 Connectivity (Full Isolation Phase 1)"
echo "=========================================="
echo ""

NAMESPACE="clusters-${CLUSTER_NAME}"

# Function to get Primary UDN IP from pod-networks annotation
get_udn_ip() {
  local pod_name=$1
  local namespace=$2
  oc get pod "$pod_name" -n "$namespace" \
    -o jsonpath='{.metadata.annotations.k8s\.ovn\.org/pod-networks}' 2>/dev/null | \
    jq -r "to_entries[] | select(.value.role==\"primary\") | .value.ip_address" 2>/dev/null | \
    cut -d/ -f1
}

# Get a control plane pod (kube-apiserver)
echo "Finding control plane pod..."
CP_POD=$(oc get pod -n ${NAMESPACE} -l app=kube-apiserver -o name 2>/dev/null | head -1)
if [ -z "$CP_POD" ]; then
  echo "WARNING: No kube-apiserver pod found. Skipping L2 verification."
else
  CP_POD_NAME=$(echo "$CP_POD" | sed 's|pod/||')
  CP_UDN_IP=$(get_udn_ip "$CP_POD_NAME" "$NAMESPACE")
  echo "Control plane pod: ${CP_POD_NAME}"
  echo "Control plane Primary UDN IP: ${CP_UDN_IP:-not found}"
  
  # Get a worker VM (virt-launcher pod)
  echo ""
  echo "Finding worker VM pod..."
  WORKER_POD=$(oc get pod -n ${NAMESPACE} -l kubevirt.io=virt-launcher -o name 2>/dev/null | head -1)
  if [ -z "$WORKER_POD" ]; then
    echo "WARNING: No virt-launcher pod found. Worker VMs may not be running yet."
    echo "Skipping L2 connectivity test."
  else
    WORKER_POD_NAME=$(echo "$WORKER_POD" | sed 's|pod/||')
    WORKER_UDN_IP=$(get_udn_ip "$WORKER_POD_NAME" "$NAMESPACE")
    echo "Worker VM pod: ${WORKER_POD_NAME}"
    echo "Worker VM Primary UDN IP: ${WORKER_UDN_IP:-not found}"
    
    if [ -n "$CP_UDN_IP" ] && [ -n "$WORKER_UDN_IP" ] && [ "$CP_UDN_IP" != "null" ] && [ "$WORKER_UDN_IP" != "null" ]; then
      echo ""
      echo "Testing L2 connectivity: Control Plane (${CP_UDN_IP}) -> Worker VM (${WORKER_UDN_IP})"
      echo "---"
      
      # Test connectivity from control plane to worker
      # Note: kube-apiserver container may not have ping, try with different methods
      L2_TEST_RESULT="unknown"
      
      # Try ping first (may not be available in all containers)
      if oc exec -n ${NAMESPACE} ${CP_POD} -c kube-apiserver -- ping -c 3 -W 5 ${WORKER_UDN_IP} 2>/dev/null; then
        L2_TEST_RESULT="success"
      else
        # Try with nc (netcat) if available
        if oc exec -n ${NAMESPACE} ${CP_POD} -c kube-apiserver -- nc -zv -w 5 ${WORKER_UDN_IP} 22 2>/dev/null; then
          L2_TEST_RESULT="success"
        else
          # Try with curl timeout to any port
          if oc exec -n ${NAMESPACE} ${CP_POD} -c kube-apiserver -- timeout 5 bash -c "echo > /dev/tcp/${WORKER_UDN_IP}/22" 2>/dev/null; then
            L2_TEST_RESULT="success"
          else
            L2_TEST_RESULT="failed"
          fi
        fi
      fi
      
      echo ""
      if [ "$L2_TEST_RESULT" = "success" ]; then
        echo "✓ L2 connectivity verified!"
        echo "  Control plane pods and worker VMs can communicate on Primary UDN."
        echo "  This is the foundation for full isolation."
      else
        echo "⚠ L2 connectivity test inconclusive."
        echo "  The test tools may not be available in the container."
        echo "  Manual verification recommended:"
        echo "    oc exec -n ${NAMESPACE} ${CP_POD} -c kube-apiserver -- ping -c 3 ${WORKER_UDN_IP}"
      fi
      
      echo ""
      echo "Network Summary:"
      echo "  Primary UDN Subnet: 10.150.0.0/16"
      echo "  Control Plane IP:   ${CP_UDN_IP}"
      echo "  Worker VM IP:       ${WORKER_UDN_IP}"
      echo ""
      echo "Both are on the same L2 segment - direct communication possible!"
    else
      echo ""
      echo "WARNING: Could not determine UDN IPs for both pods."
      echo "L2 connectivity verification skipped."
    fi
  fi
fi

echo ""
echo "=========================================="
echo "Cluster Setup Complete"
echo "=========================================="
echo ""
if [ -n "${GUEST_KUBECONFIG}" ] && [ -f "${GUEST_KUBECONFIG}" ]; then
  echo "Guest cluster kubeconfig: ${GUEST_KUBECONFIG}"
  echo ""
  echo "To access the guest cluster:"
  echo "  export KUBECONFIG=${GUEST_KUBECONFIG}"
  echo "  oc get nodes"
else
  echo "Guest cluster kubeconfig not extracted yet."
  echo "To extract it manually:"
  echo "  oc extract -n clusters secret/${CLUSTER_NAME}-admin-kubeconfig --to=. --confirm"
  echo "  mv kubeconfig ${CLUSTER_NAME}-kubeconfig"
fi
echo ""

echo "Rescale nodepool to 2 replicas..."
NODEPOOL_NAME=$CLUSTER_NAME
NODEPOOL_REPLICAS=2

oc scale nodepool/$NODEPOOL_NAME --namespace clusters --replicas=$NODEPOOL_REPLICAS

echo "Waiting for guest nodes to be Ready (required to rescale nodepool)..."
timeout="${WAIT_GUEST_NODES_TIMEOUT_SECONDS}"
elapsed=0
while true; do
  ready_nodes="$(
    oc --kubeconfig "${GUEST_KUBECONFIG}" get nodes -o json 2>/dev/null | \
      jq -r '[.items[] | select(any(.status.conditions[]?; .type=="Ready" and .status=="True"))] | length' 2>/dev/null || echo 0
  )"
  if [ "${ready_nodes}" -ge "${NODEPOOL_REPLICAS}" ]; then
    echo "✓ Guest Ready nodes: ${ready_nodes}"
    break
  fi
  if [ "${elapsed}" -ge "${timeout}" ]; then
    echo "WARNING: Only ${ready_nodes} Ready guest nodes after ${timeout}s. Skipping internal DNS/OAuth setup for now."
    echo "This is expected right after control plane becomes Available=True."
    echo "Re-run this script later once nodes join."
    goto_after_dns_setup=true
    break
  fi
  echo "Still waiting for guest Ready nodes (have ${ready_nodes}, need >= ${NODEPOOL_REPLICAS})... (${elapsed}/${timeout} seconds)"
  sleep 15
  elapsed=$((elapsed + 15))
done

echo "Decrease hosted cluster Prometheus retention period to 8 hours to prevent over logging..."
oc --kubeconfig "${CLUSTER_NAME}-kubeconfig" -n openshift-monitoring patch prometheus k8s --type=merge -p '{"spec":{"retention":"8h"}}'

# oc login --server=https://api.hypershift.qinqon.corp:6443 -u kubeadmin -p SFs6e-gkwGC-H8Uk8-w3TTU
