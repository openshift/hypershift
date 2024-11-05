#!/bin/bash

# set -eux
set -eu

### Details
## Purpose: This script is used to backup and restore Hosted Cluster resources on Baremetal environments among different Managemeng Clusters.
## Source: The original one was located here https://github.com/openshift/ops-sop/blob/master/hypershift/utils/dr-script/migrate-hcp.sh
## Status: alpha
## Author: Red Hat
###

show_warning() {
    echo ""
    echo ""
    echo "There may be sensitive data in ${WORKSPACE_DIR}"
    echo "Ensure proper cleanup if the data is no longer needed."
}

trap 'show_warning' EXIT INT

function get_hc_kubeconfig() {
    if [ ! -f ${HC_KUBECONFIG} ]; then
        touch ${HC_KUBECONFIG}
    fi

    ${OC} get secret -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} admin-kubeconfig -ojsonpath='{.data.kubeconfig}' | base64 -d > ${HC_KUBECONFIG}
    export KUBECONFIG=${HC_KUBECONFIG}
    # Don't exit if login failed, takes time for control plane to come up on restore
    set +e
    attempts=$1
    for i in $(seq 1 $attempts); do
        echo "$( date ) get_hc_kubeconfig: login to hosted cluster, attempt $i / $attempts"
        ${OC} cluster-info
        if [ $? -eq 0 ]; then
            echo "$( date ) get_hc_kubeconfig: successfully logging into hosted cluster"
            break
        fi

        sleep 3
    done
    set -e
}

function create_cm_in_hc() {
    set +x
    get_hc_kubeconfig 40
    set -x

    # Create a ConfigMap on the guest so we can tell which management cluster it came from
    export KUBECONFIG=${HC_KUBECONFIG}

    # HC may be in failed state. Do not fail backup if access to HC fails
    set +e
    ${OC} create configmap ${USER}-dev-cluster -n default --from-literal=from=${MGMT_CLUSTER_NAME} || true
    if [ $? -ne 0 ]; then
        echo "$( date ) create_cm_in_hc: failed to create ConfigMap on Hosted Cluster."
    fi
    set -e
}

function change_reconciliation() {

    if [[ -z "${1}" ]];then
        echo "$( date ) change_reconciliation: Missing arg <start|stop>"
        exit 1
    fi
    echo "$( date ) change_reconciliation: status ${1}"

    if [[ -z ${NODEPOOLS} ]];then
        NODEPOOLS=$(${OC} get nodepools -n ${HC_CLUSTER_NS} -o=jsonpath='{.items[?(@.spec.clusterName=="'${HC_CLUSTER_NAME}'")].metadata.name}')
    fi

    case ${1} in
        "stop")
            export KUBECONFIG=${MGMT_KUBECONFIG}
            # Pause reconciliation of HC and NP and ETCD writers
            PAUSED_UNTIL="true"
            ${OC} patch -n ${HC_CLUSTER_NS} hostedclusters/${HC_CLUSTER_NAME} -p '{"spec":{"pausedUntil":"'${PAUSED_UNTIL}'"}}' --type=merge
            for nodepool in ${NODEPOOLS}
            do
                ${OC} patch -n ${HC_CLUSTER_NS} nodepools/${nodepool} -p '{"spec":{"pausedUntil":"'${PAUSED_UNTIL}'"}}' --type=merge
            done

            # Pause AgentMachine & AgentCluster
            oc annotate agentcluster -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} cluster.x-k8s.io/paused=true --all
            oc annotate agentmachine -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} cluster.x-k8s.io/paused=true --all

            # Pause Cluster
            oc annotate cluster -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} cluster.x-k8s.io/paused=true --all

            # Annotate HC to prevent deleting hosted control plane namespace
            oc annotate -n ${HC_CLUSTER_NS} hostedclusters/${HC_CLUSTER_NAME} hypershift.openshift.io/skip-delete-hosted-controlplane-namespace=true

            ${OC} scale deployment -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --replicas=0 kube-apiserver openshift-apiserver openshift-oauth-apiserver control-plane-operator
            ;;
        "start")
            paused=$(${OC} get -n ${HC_CLUSTER_NS} hostedclusters/${HC_CLUSTER_NAME} -o=json | jq -r '.spec.pausedUntil')
            if [ "$paused" == "true" ]; then
                # Restart reconciliation of HC and NP and ETCD writers
                PAUSED_UNTIL="false"
                ${OC} patch -n ${HC_CLUSTER_NS} hostedclusters/${HC_CLUSTER_NAME} -p '{"spec":{"pausedUntil":"'${PAUSED_UNTIL}'"}}' --type=merge
                for nodepool in ${NODEPOOLS}
                do
                    ${OC} patch -n ${HC_CLUSTER_NS} nodepools/${nodepool} -p '{"spec":{"pausedUntil":"'${PAUSED_UNTIL}'"}}' --type=merge
                done
                unpause_agent
                ${OC} scale deployment -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --replicas=1 kube-apiserver openshift-apiserver openshift-oauth-apiserver control-plane-operator
            fi
            ;;
        *)
            echo "$( date ) change_reconciliation: status not implemented"
            exit 1
            ;;
    esac

}

function unpause_agent() {
    # Restart AgentMachine & AgentCluster
    oc annotate agentcluster -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} cluster.x-k8s.io/paused- --overwrite=true --all
    oc annotate agentmachine -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} cluster.x-k8s.io/paused- --overwrite=true --all

    # Restart Cluster
    oc annotate cluster -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} cluster.x-k8s.io/paused- --overwrite=true --all
    # Annotate HC to remove deleting hosted control plane namespace annotation
    oc annotate -n ${HC_CLUSTER_NS} hostedclusters/${HC_CLUSTER_NAME} hypershift.openshift.io/skip-delete-hosted-controlplane-namespace- --overwrite=true
}

function check_aws_creds() {
	if [[ -z "${AWS_ACCESS_KEY_ID}" ]] || [[ -z "${AWS_SECRET_ACCESS_KEY}" ]];
	then
		echo "$( date ) check_aws_creds: AWS_ACCESS_KEY_ID or AWS_SECRET_ACCESS_KEY not set. Run `export $(osdctl account cli -o env -i ${MGMT_ACCOUNT_ID} -p ${PROFILE} -r ${MGMT_REGION} | xargs)` prior to invoking this script"
	fi
}

function backup_etcd() {
    echo "$( date ) 🍀 backup_etcd: backing up etcd"

    # ETCD Backup
    POD="etcd-0"
    ETCD_CA_LOCATION=/etc/etcd/tls/etcd-ca/ca.crt

    # Create an etcd snapshot
    echo "$( date ) backup_etcd: create etcd snapshot"
    ${OC} exec -it ${POD} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -- env ETCDCTL_API=3 /usr/bin/etcdctl --cacert ${ETCD_CA_LOCATION} --cert /etc/etcd/tls/client/etcd-client.crt --key /etc/etcd/tls/client/etcd-client.key --endpoints=localhost:2379 snapshot save /var/lib/data/snapshot.db

    echo "$( date ) backup_etcd: table etcd snapshot status"
    ${OC} exec -it ${POD} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -- env ETCDCTL_API=3 /usr/bin/etcdctl -w table snapshot status /var/lib/data/snapshot.db

    FILEPATH="/${BUCKET_NAME}/${HC_CLUSTER_NAME}-${POD}-snapshot.db"
    CONTENT_TYPE="application/x-compressed-tar"
    DATE_VALUE=`date -R`
    SIGNATURE_STRING="PUT\n\n${CONTENT_TYPE}\n${DATE_VALUE}\n${FILEPATH}"
    echo "$( date ) backup_etcd: etcd snapshot path $FILEPATH"

    check_aws_creds

    echo "$( date ) backup_etcd: push etcd snapshot to s3"
    ${OC} cp -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} etcd-0:/var/lib/data/snapshot.db ${HC_CLUSTER_DIR}/snapshot.db
    aws s3 cp ${HC_CLUSTER_DIR}/snapshot.db s3://${BUCKET_NAME}/${HC_CLUSTER_NAME}-${POD}-snapshot.db
    rm -f ${HC_CLUSTER_DIR}/snapshot.db

    echo "$( date ) backup_etcd: checking to see if the backup uploaded successfully to s3..."
    aws s3 ls s3://${BUCKET_NAME}/${HC_CLUSTER_NAME}-${POD}-snapshot.db
    echo "$( date ) 🍀 backup_etcd: done!"
}

function render_agent_objects {
    # Backup resources
    mkdir -p ${BACKUP_DIR}/namespaces/${AGENT_NAMESPACE} ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    chmod 700 ${BACKUP_DIR}/namespaces/

    export KUBECONFIG="${MGMT_KUBECONFIG}"

    # AgentClusterInstall
    echo "$( date ) render_agent_objects: --> AgentClusterInstall"
    ${OC} get agentclusterinstall ${HC_CLUSTER_NAME} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/aci-${HC_CLUSTER_NAME}.yaml

    # ClusterDeployment
    echo "$( date ) render_agent_objects: --> ClusterDeployment"
    ${OC} get clusterdeployment ${HC_CLUSTER_NAME} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/cd-${HC_CLUSTER_NAME}.yaml

    # Agent
    echo "$( date ) render_agent_objects: --> Agent"
    for s in $(${OC} get agent -n ${AGENT_NAMESPACE} --no-headers | awk '{print $1}'); do
        ${OC} get agent -n ${AGENT_NAMESPACE} $s -o yaml  > ${BACKUP_DIR}/namespaces/${AGENT_NAMESPACE}/agent-${s}.yaml
    done

    # InfraEnv
    echo "$( date ) render_agent_objects: --> InfraEnv"
    for s in $(${OC} get infraenv -n ${AGENT_NAMESPACE} --no-headers | awk '{print $1}'); do
        ${OC} get infraenv -n ${AGENT_NAMESPACE} $s -o yaml  > ${BACKUP_DIR}/namespaces/${AGENT_NAMESPACE}/ie-${s}.yaml
    done

    # BMH
    echo "$( date ) render_agent_objects: --> BMH"
    for s in $(${OC} get bmh -n ${AGENT_NAMESPACE} --no-headers | awk '{print $1}'); do
        ${OC} get bmh -n ${AGENT_NAMESPACE} $s -o yaml  > ${BACKUP_DIR}/namespaces/${AGENT_NAMESPACE}/bmh-${s}.yaml
    done

    # Secrets in the Agent Namespace
    echo "$( date ) render_agent_objects: --> Agent Secrets"
    for s in $(${OC} get secret -n ${AGENT_NAMESPACE} --no-headers | awk '{print $1}'); do
        ${OC} get secret -n ${AGENT_NAMESPACE} $s -o yaml  > ${BACKUP_DIR}/namespaces/${AGENT_NAMESPACE}/secret-${s}.yaml
    done

    # Role in the Agent Namespace
    echo "$( date ) render_agent_objects: --> Agent Roles"
    ${OC} get role -n ${AGENT_NAMESPACE} capi-provider-role -o yaml  > ${BACKUP_DIR}/namespaces/${AGENT_NAMESPACE}/role-capi-provider-role.yaml
}

function render_hc_objects {
    # Backup resources
    mkdir -p ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS} ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    chmod 700 ${BACKUP_DIR}/namespaces/

    export KUBECONFIG="${MGMT_KUBECONFIG}"

    ## Not necessary, certificate object is not registered in the customers API
    #set +e
    ## Certificates
    #echo "$( date ) render_hc_objects: backing Up Certificate Objects:"
    #${OC} get certificate cluster-api-cert -n ${HC_CLUSTER_NS} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/certificate-cluster-api-cert.yaml
    #echo "$( date ) render_hc_objects: --> Certificate"
    ## $SEDCMD -i'' -e '/^status:$/,$d' ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/hc-${HC_CLUSTER_NAME}.yaml
    #set -e

    # HostedCluster
    echo "$( date ) render_hc_objects: backing Up HostedCluster Objects:"
    ${OC} get hc ${HC_CLUSTER_NAME} -n ${HC_CLUSTER_NS} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/hc-${HC_CLUSTER_NAME}.yaml
    echo "$( date ) render_hc_objects: --> HostedCluster"
    $SEDCMD -i'' -e '/^status:$/,$d' ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/hc-${HC_CLUSTER_NAME}.yaml

    # NodePool
    echo "$( date ) render_hc_objects: backing Up NodePool Objects:"
    for nodepool in ${NODEPOOLS}
    do
        ${OC} get np ${nodepool} -n ${HC_CLUSTER_NS} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/np-${nodepool}.yaml
        echo "$( date ) render_hc_objects: --> NodePool ${nodepool}"
        $SEDCMD -i'' -e '/^status:$/,$ d' ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/np-${nodepool}.yaml
    done

    # Secrets in the HC Namespace
    echo "$( date ) render_hc_objects: --> HostedCluster Secrets"
    for s in $(${OC} get secret -n ${HC_CLUSTER_NS}  | grep "${HC_CLUSTER_NAME}" | awk '{print $1}'); do
        ${OC} get secret -n ${HC_CLUSTER_NS} $s -o yaml  > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/secret-${s}.yaml
    done

    echo "$( date ) render_hc_objects: --> HostedCluster Secrets"
    for s in $(${OC} get secret -n ${HC_CLUSTER_NS}  | grep bound | awk '{print $1}'); do
        ${OC} get secret -n ${HC_CLUSTER_NS} $s -o yaml  > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/secret-${s}.yaml
    done
    for s in $(${OC} get secret -n ${HC_CLUSTER_NS}  | grep htpasswd-secret | awk '{print $1}'); do
        ${OC} get secret -n ${HC_CLUSTER_NS} $s -o yaml  > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/secret-${s}.yaml
    done

    # Secrets in the HC Control Plane Namespace
    echo "$( date ) render_hc_objects: --> HostedCluster ControlPlane Secrets"
    for s in $(${OC} get secret -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}  | egrep -v "service-account-token|oauth-openshift|NAME|token-${HC_CLUSTER_NAME}" | awk '{print $1}'); do
        ${OC} get secret  -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} $s -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/secret-${s}.yaml
    done

    # CAPI Agent Role
    echo "$( date ) render_hc_objects: --> Roles"
    ${OC} get role capi-provider -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/role-${HC_CLUSTER_NAME}.yaml

    # Hosted Control Plane
    echo "$( date ) render_hc_objects: --> HostedControlPlane"
    ${OC} get hcp ${HC_CLUSTER_NAME} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/hcp-${HC_CLUSTER_NAME}.yaml

    # Cluster
    echo "$( date ) render_hc_objects: --> Cluster"
    CL_NAME=$(${OC} get hcp ${HC_CLUSTER_NAME} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -ojson | jq -r '.metadata.labels."cluster.x-k8s.io/cluster-name"')
    ${OC} get cluster.cluster ${CL_NAME} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/cl-${HC_CLUSTER_NAME}.yaml

    # Agent Cluster
    echo "$( date ) render_hc_objects: --> Agent Cluster"
    ${OC} get agentCluster ${HC_CLUSTER_NAME} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/agentcluster-${HC_CLUSTER_NAME}.yaml

    # Agent MachineTemplate
    echo "$( date ) render_hc_objects: --> Agent Machine Template"
    MT_NAME=$(${OC} get agentmachinetemplate -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o json | jq -r '.items[0].metadata.name')
    ${OC} get agentmachinetemplate ${MT_NAME} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/agentmachinetemplate-${HC_CLUSTER_NAME}.yaml

    # Agent Machines
    echo "$( date ) render_hc_objects: --> Agent Machine"
    CL_NAME=$(${OC} get hcp ${HC_CLUSTER_NAME} -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o jsonpath={.metadata.labels.\*})
    for s in $(${OC} get agentmachine -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --no-headers | grep ${CL_NAME} | cut -f 1 -d\ ); do
        ${OC} get -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} agentmachine $s -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/agentmachine-${s}.yaml
    done

    # MachineDeployments
    echo "$( date ) render_hc_objects: --> HostedCluster MachineDeployments"
    for s in $(${OC} get machinedeployment -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o name); do
        mdp_name=$(echo ${s} | cut -f 2 -d /)
        ${OC} get -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} $s -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/machinedeployment-${mdp_name}.yaml
    done

    # MachineSets
    echo "$( date ) render_hc_objects: --> HostedCluster MachineSets"
    for s in $(${OC} get machineset.cluster.x-k8s.io -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o name); do
        ms_name=$(echo ${s} | cut -f 2 -d /)
        ${OC} get -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} $s -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/machineset-${ms_name}.yaml
    done

    # Machines
    echo "$( date ) render_hc_objects: --> HostedCluster Machines"
    for s in $(${OC} get machine.cluster.x-k8s.io -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o name); do
        m_name=$(echo ${s} | cut -f 2 -d /)
        ${OC} get -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} $s -o yaml > ${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}/machine-${m_name}.yaml
    done
}

function restore_etcd() {
    echo "$( date ) 🚨 restore_etcd: restore etcd"

    # Check Hypershift CRD if 'restoreSnapShotURL' property has x-kubernetes-validations
    hc_crd=$(kubectl get crd hostedclusters.hypershift.openshift.io -o json)
    etcd_valdation_exist=$(echo "$hc_crd" | jq '.spec.versions[1].schema.openAPIV3Schema.properties.spec.properties.etcd.properties.managed.properties.storage.properties.restoreSnapshotURL | has("x-kubernetes-validations")')

    ETCD_PODS="etcd-0"
    if [ "$etcd_valdation_exist" == "false" ] && [ "${CONTROL_PLANE_AVAILABILITY_POLICY}" = "HighlyAvailable" ]; then
      ETCD_PODS="etcd-0 etcd-1 etcd-2"
    fi

    HC_RESTORE_FILE=${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/hc-${HC_CLUSTER_NAME}-restore.yaml
    HC_BACKUP_FILE=${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/hc-${HC_CLUSTER_NAME}.yaml
    HC_NEW_FILE=${BACKUP_DIR}/namespaces/${HC_CLUSTER_NS}/hc-${HC_CLUSTER_NAME}-new.yaml
    cat ${HC_BACKUP_FILE} > ${HC_NEW_FILE}
    cat > ${HC_RESTORE_FILE} <<EOF
        restoreSnapshotURL:
EOF

    for POD in ${ETCD_PODS}; do
      # Create a pre-signed URL for the etcd snapshot
      ETCD_SNAPSHOT="s3://${BUCKET_NAME}/${HC_CLUSTER_NAME}-etcd-0-snapshot.db"
      ETCD_SNAPSHOT_URL=$(AWS_DEFAULT_REGION=${MGMT2_REGION} aws s3 presign ${ETCD_SNAPSHOT})
      echo "$( date ) PRESIGN URL: $ETCD_SNAPSHOT_URL"
      # FIXME no CLI support for restoreSnapshotURL yet
      cat >> ${HC_RESTORE_FILE} <<EOF
        - "${ETCD_SNAPSHOT_URL}"
EOF
    done

    cat ${HC_RESTORE_FILE}

    if ! grep ${HC_CLUSTER_NAME}-snapshot.db ${HC_NEW_FILE}; then
      $SEDCMD -i'' -e "/type: PersistentVolume/r ${HC_RESTORE_FILE}" ${HC_NEW_FILE}
      $SEDCMD -i'' -e '/pausedUntil:/d' ${HC_NEW_FILE}
    fi

    HC=$(${OC} get hc -n ${HC_CLUSTER_NS} ${HC_CLUSTER_NAME} -o name || true)
    if [[ ${HC} == "" ]];then
        echo "$( date ) 🚨 restore_etcd: deploying HC Cluster: ${HC_CLUSTER_NAME} in ${HC_CLUSTER_NS} namespace"
        ${OC} apply -f ${HC_NEW_FILE}
    else
        echo "$( date ) 🚨 restore_etcd: HC Cluster ${HC_CLUSTER_NAME} already exists, avoiding step"
    fi
}

function restore_object() {
    echo "$( date ) restore_object: restore objects ${2}/${1}"

    if [[ -z ${1} || ${1} == " " ]]; then
        echo "$( date ) restore_object: argument to deploy K8s objects is required"
        exit 1
    fi

    if [[ -z ${2} || ${2} == " " ]]; then
        echo "$( date ) restore_object: namespace to deploy the K8s objects is required"
        exit 1
    fi

    if [[ ! -d ${BACKUP_DIR}/namespaces/${2} ]];then
        echo "$( date ) restore_object: folder ${BACKUP_DIR}/namespaces/${2} does not exists"
        exit 1
    fi

    case ${1} in
        "secret" | "machine" | "machineset" | "hcp" | "cl" | "agentcluster" | "agentmachinetemplate" | "agentmachine" | "machinedeployment")
            # Cleaning the YAML files before apply them
            for f in $(ls -1 ${BACKUP_DIR}/namespaces/${2}/${1}-*); do
                yq eval 'del(.metadata.ownerReferences,.metadata.creationTimestamp,.metadata.resourceVersion,.metadata.uid,.status)' $f | ${OC} apply --server-side=true -f -
            done
            ;;
        "aci" | "cd")
            # Cleaning the YAML files before apply them
            for f in $(ls -1 ${BACKUP_DIR}/namespaces/${2}/${1}-*); do
                yq eval 'del(.metadata.ownerReferences,.metadata.creationTimestamp,.metadata.resourceVersion,.metadata.uid)' $f | ${OC} apply --server-side=true -f -
            done
            ;;
        "agent" | "ie" | "bmh" | "role")
            # Cleaning the YAML files before apply them
            for f in $(ls -1 ${BACKUP_DIR}/namespaces/${2}/${1}-*); do
                yq eval 'del(.metadata.creationTimestamp,.metadata.resourceVersion,.metadata.uid)' $f | ${OC} apply --server-side=true -f -
            done
            ;;
        "certificate")
            for f in $(ls -1 ${BACKUP_DIR}/namespaces/${2}/${1}-*); do
                yq eval 'del(.metadata.ownerReferences,.metadata.creationTimestamp,.metadata.resourceVersion,.metadata.uid,.status)' $f | ${OC} apply --server-side=true -f -
            done
            ;;
        "hc")
            # Cleaning the YAML files before apply them
            for f in $(ls -1 ${BACKUP_DIR}/namespaces/${2}/${1}-*); do
                yq eval 'del(.metadata.ownerReferences,.metadata.creationTimestamp,.metadata.resourceVersion,.metadata.uid,.status,.spec.pausedUntil)' $f | ${OC} apply --server-side=true -f -
            done
            ;;
        "np")
            # Cleaning the YAML files before apply them
            for f in $(ls -1 ${BACKUP_DIR}/namespaces/${2}/${1}-*); do
                yq eval 'del(.metadata.annotations,.metadata.ownerReferences,.metadata.creationTimestamp,.metadata.resourceVersion,.metadata.uid,.status,.spec.pausedUntil)' $f | ${OC} apply --server-side=true -f -
            done
            ;;
        *)
            echo "$( date ) restore_object: K8s object not supported: ${1}"
            exit 1
            ;;
    esac

}

function clean_routes() {

    if [[ -z "${1}" ]];then
        echo "$( date ) clean_routes: namespace where to clean the routes is required"
        exit 1
    fi

    echo "$( date ) clean_routes: clean routes in namespace ${1}"
    ${OC} delete route -n ${1} --all
}

function render_svc_objects() {
    echo "$( date ) render_svc_objects: restore Management cluster resources for ACM/MCE"

    BACKUP_DIR=${HC_CLUSTER_DIR}/backup
    mkdir -p ${BACKUP_DIR}/svc
    # Change kubeconfig to service cluster
    # The SVC Cluster is not relevant outside of OCM environment.
    export KUBECONFIG="${MGMT_KUBECONFIG}"

    # ManagedCluster
    echo "$( date ) render_svc_objects: backing Up HostedCluster Objects:"
    ${OC} get managedcluster ${HC_CLUSTER_NAME} -o yaml > ${BACKUP_DIR}/svc/managedcluster-${HC_CLUSTER_NAME}.yaml
    echo "$( date ) render_svc_objects: --> ManagedCluster"
    $SEDCMD -i'' -e '/^status:$/,$d' ${BACKUP_DIR}/svc/managedcluster-${HC_CLUSTER_NAME}.yaml

    # ManagedClusterAddOns
    ${OC} get managedclusteraddons -n ${HC_CLUSTER_NAME} config-policy-controller -o yaml > ${BACKUP_DIR}/svc/managedclusteraddon-config-policy-controller-${HC_CLUSTER_NAME}.yaml
    echo "$( date ) render_svc_objects: --> config-policy-controller ManagedClusterAddOn"
    $SEDCMD -i'' -e '/^status:$/,$d' ${BACKUP_DIR}/svc/managedclusteraddon-config-policy-controller-${HC_CLUSTER_NAME}.yaml

    ${OC} get managedclusteraddons -n ${HC_CLUSTER_NAME} governance-policy-framework -o yaml > ${BACKUP_DIR}/svc/managedclusteraddon-governance-policy-framework-${HC_CLUSTER_NAME}.yaml
    echo "$( date ) render_svc_objects: --> governance-policy-framework ManagedClusterAddOn"
    $SEDCMD -i'' -e '/^status:$/,$d' ${BACKUP_DIR}/svc/managedclusteraddon-governance-policy-framework-${HC_CLUSTER_NAME}.yaml

    ${OC} get managedclusteraddons -n ${HC_CLUSTER_NAME} work-manager -o yaml > ${BACKUP_DIR}/svc/managedclusteraddon-work-manager-${HC_CLUSTER_NAME}.yaml
    echo "$( date ) render_svc_objects: --> work-manager ManagedClusterAddOn"
    $SEDCMD -i'' -e '/^status:$/,$d' ${BACKUP_DIR}/svc/managedclusteraddon-work-manager-${HC_CLUSTER_NAME}.yaml

}

function backup_hc() {
    echo "$( date ) backup_hc: backup hosted cluster resources"
    BACKUP_DIR=${HC_CLUSTER_DIR}/backup

    if [ -d ${BACKUP_DIR} ]; then
        echo "$( date ) backup_hc: there is an existing backup in ${BACKUP_DIR}. Remove it before starting a new backup."
        exit 1
    fi

    mkdir -p ${BACKUP_DIR}

    # Get list of nodepools
    export KUBECONFIG="${MGMT_KUBECONFIG}"
    NODEPOOLS=$(${OC} get nodepools -n ${HC_CLUSTER_NS} -o=jsonpath='{.items[?(@.spec.clusterName=="'${HC_CLUSTER_NAME}'")].metadata.name}')

    if [ -z $MGMT_CLUSTER_NAME ]; then
        MGMT_CLUSTER_NAME=$(oc config view --minify -o jsonpath='{.clusters[*].cluster.server}')
    fi

    change_reconciliation "start"
    create_cm_in_hc

    # Change kubeconfig to management cluster
    export KUBECONFIG="${MGMT_KUBECONFIG}"
    change_reconciliation "stop"
    set +x
    backup_etcd
    set -x
    render_svc_objects
    render_hc_objects
    render_agent_objects
    echo "$( date ) backup_hc: backup hosted cluster resources completed, restarting reconciliation"
    change_reconciliation "start"
}

function restore_hc() {
    ## In order to restore HostedCluster in MGMT2, we need to have in mind:
    ## - Have stopped the reconciliation of the HostedCluster in MGMT
    ## - Clean the routes for that HC in the MGMT cluster
    ## So now we can restore the HostedCluster in MGMT2.
    export KUBECONFIG=${MGMT_KUBECONFIG}
    BACKUP_DIR=${HC_CLUSTER_DIR}/backup

    # Get list of nodepools
    NODEPOOLS=$(${OC} get nodepools -n ${HC_CLUSTER_NS} -o=jsonpath='{.items[?(@.spec.clusterName=="'${HC_CLUSTER_NAME}'")].metadata.name}')

    echo "$( date ) restore_hc: Stopping reconciliation and cleanning the routes in MGMT of HostedCluster ${HC_CLUSTER_NAME}"
    change_reconciliation "stop"
    clean_routes ${HC_CLUSTER_NS}

    # MGMT2 Context
    echo "$( date ) restore_hc: restore hosted cluster resources"
    if [[ ! -f ${MGMT2_KUBECONFIG} ]]; then
        echo "$( date ) restore_hc: destination Cluster Kubeconfig does not exists"
        echo "$( date ) restore_hc: dir: ${MGMT2_KUBECONFIG}"
        exit 1
    fi

    export KUBECONFIG=${MGMT2_KUBECONFIG}
    ${OC} new-project ${HC_CLUSTER_NS} || ${OC} project ${HC_CLUSTER_NS}
    ${OC} new-project ${AGENT_NAMESPACE} || ${OC} project ${AGENT_NAMESPACE}
    restore_object "secret" ${HC_CLUSTER_NS}
    ${OC} new-project ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} || ${OC} project ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    restore_object "secret" ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    restore_object "hcp" ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    restore_object "cl" ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    restore_object "aci" ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    restore_object "cd" ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    restore_object "role" ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    restore_object "secret" ${AGENT_NAMESPACE}
    restore_object "role" ${AGENT_NAMESPACE}
    restore_object "ie" ${AGENT_NAMESPACE}
    restore_object "bmh" ${AGENT_NAMESPACE}
    restore_object "agent" ${AGENT_NAMESPACE}
    restore_object "agentcluster" ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    restore_object "agentmachinetemplate" ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    restore_object "agentmachine" ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    restore_object "machinedeployment" ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    restore_object "machine" ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    restore_object "machineset" ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME}
    restore_etcd
    restore_object "np" ${HC_CLUSTER_NS}
    unpause_agent
}

function restore_svc() {
    echo "$( date ) restore_svc: restore service cluster resources"

    export KUBECONFIG=${MGMT2_KUBECONFIG}
    for f in $(ls -1 ${BACKUP_DIR}/svc/*); do
      # Delete managed cluster addons first, instead of updating it
      if [[ "$f" == managedclusteraddon-*.yaml ]]; then
        ADDON_NAME=$(cat $f | yq .metadata.name)
        ${OC} delete managedclusteraddon -n ${HC_CLUSTER_NAME} ${ADDON_NAME}
      fi

      yq eval 'del(.metadata.ownerReferences,.metadata.creationTimestamp,.metadata.resourceVersion,.metadata.uid,.status)' $f | ${OC} apply -f -
    done
}

function teardown_old_hc() {
    echo "$( date ) teardown_old_hc: teardown old hosted cluster resources"
    export KUBECONFIG=${MGMT_KUBECONFIG}

    # Scale down deployments
    ${OC} scale deployment -n hypershift operator --replicas 0
    ${OC} scale deployment -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --replicas=0 --all
    ${OC} scale statefulset.apps -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --replicas=0 --all
    sleep 15


    # Delete Nodepools
    echo "$( date ) teardown_old_hc: delete nodepools"
    NODEPOOLS=$(${OC} get nodepools -n ${HC_CLUSTER_NS} -o=jsonpath='{.items[?(@.spec.clusterName=="'${HC_CLUSTER_NAME}'")].metadata.name}')
    if [[ ! -z "${NODEPOOLS}" ]];then
        for nodepool in ${NODEPOOLS}
        do
            ${OC} patch -n "${HC_CLUSTER_NS}" nodepool ${nodepool} --type=json --patch='[ { "op":"remove", "path": "/metadata/finalizers" }]'
            ${OC} delete np -n ${HC_CLUSTER_NS} ${nodepool}
        done
    fi

    # Machines
    echo "$( date ) teardown_old_hc: delete machines.cluster.x-k8s.io"
    for m in $(${OC} get machines.cluster.x-k8s.io -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o name); do
        ${OC} patch -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} ${m} --type=json --patch='[ { "op":"remove", "path": "/metadata/finalizers" }]'
        ${OC} delete -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} ${m} || true
    done

    ${OC} delete machineset.cluster.x-k8s.io -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --all || true

    # Cluster
    echo "$( date ) teardown_old_hc: delete cluster"
    C_NAME=$(${OC} get cluster -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o name)
    ${OC} patch -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} ${C_NAME} --type=json --patch='[ { "op":"remove", "path": "/metadata/finalizers" }]'
    ${OC} delete cluster.cluster.x-k8s.io -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --all || true

    # Agent Cluster
    echo "$( date ) teardown_old_hc: delete agent cluster"
    C_NAME=$(${OC} get agentcluster -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o name)
    ${OC} patch agentcluster -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} ${C_NAME} --type=json --patch='[ { "op":"remove", "path": "/metadata/finalizers" }]'
    ${OC} delete agentcluster -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --all || true

    # Agent Machines
    echo "$( date ) teardown_old_hc: delete agentmachine"
    for m in $(${OC} get agentmachine -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} -o name)
    do
        ${OC} patch -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} ${m} --type=json --patch='[ { "op":"remove", "path": "/metadata/finalizers" }]'
        ${OC} delete -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} ${m} || true
    done

    # Service
    echo "$( date ) teardown_old_hc: delete service private-router"
    ${OC} patch -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} service private-router --type=json --patch='[ { "op":"remove", "path": "/metadata/finalizers" }]' || true

    # HCP
    echo "$( date ) teardown_old_hc: delete hostedcontrolplane"
    ${OC} patch -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} hostedcontrolplane.hypershift.openshift.io ${HC_CLUSTER_NAME} --type=json --patch='[ { "op":"remove", "path": "/metadata/finalizers" }]'
    ${OC} delete hostedcontrolplane.hypershift.openshift.io -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --all

    ${OC} delete ns ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} || true

    echo "$( date ) teardown_old_hc: delete hostedcluster"
    ${OC} -n ${HC_CLUSTER_NS} patch hostedclusters ${HC_CLUSTER_NAME} -p '{"metadata":{"finalizers":null}}' --type merge || true
    ${OC} delete hc -n ${HC_CLUSTER_NS} ${HC_CLUSTER_NAME}  --wait=false || true
    ${OC} -n ${HC_CLUSTER_NS} patch hostedclusters ${HC_CLUSTER_NAME} -p '{"metadata":{"finalizers":null}}' --type merge || true
    ${OC} delete hc -n ${HC_CLUSTER_NS} ${HC_CLUSTER_NAME}  || true

    ${OC} scale deployment -n hypershift operator --replicas 2

    ${OC} delete ns ${HC_CLUSTER_NS} || true
}

function teardown_old_klusterlet() {
    echo "$( date ) teardown_old_klusterlet: delete old klusterlet"

    export KUBECONFIG=${MGMT_KUBECONFIG}

    # Klusterlet + NS
    ${OC} delete klusterlet klusterlet-${HC_CLUSTER_NAME} --wait=false
    ${OC} patch klusterlet klusterlet-${HC_CLUSTER_NAME} --type=json --patch='[ { "op":"remove", "path": "/metadata/finalizers" }]' || true
    ${OC} delete klusterlet klusterlet-${HC_CLUSTER_NAME} --ignore-not-found=true

    ${OC} delete ns klusterlet-${HC_CLUSTER_NAME} --wait=false
    for p in $(${OC} get configurationpolicy -n klusterlet-${HC_CLUSTER_NAME} -o name)
    do
        ${OC} patch -n klusterlet-${HC_CLUSTER_NAME} ${p} --type=json --patch='[ { "op":"remove", "path": "/metadata/finalizers" }]' || true
    done
    ${OC} delete ns klusterlet-${HC_CLUSTER_NAME} --ignore-not-found=true
}

function restore_ovn_pods() {
    echo "$( date ) restore_ovn_pods: deleting OVN Pods in Guest Cluster to reconnect with new OVN Master"

    ${OC} --kubeconfig=${HC_KUBECONFIG} delete pod -n openshift-ovn-kubernetes --all --wait=false --grace-period=0
}

function restart_kube_apiserver() {
    echo "$( date ) restart_kube_apiserver: restart audit-webook, kube-apiserver, and openshift-route-controller-manager to fix intermittent api issues"
    export KUBECONFIG=${MGMT2_KUBECONFIG}
    ${OC} scale -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --replicas=0 deployment/audit-webhook
    ${OC} scale -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --replicas=2 deployment/audit-webhook
    for i in {1..36}; do
        STATUS=$(${OC} get deployment -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} audit-webhook -o jsonpath='{.status.conditions[?(@.type=="Available")].status}')
        if [ "$STATUS" == "True" ]; then
            break
        fi

        if [ $i -eq 36 ]; then
            echo "$( date ) restart_kube_apiserver: timed-out waiting for audit-webhook to be restarted"
        else
            sleep 5
        fi
    done

    ${OC} scale -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --replicas=0 deployment/kube-apiserver
    ${OC} scale -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --replicas=3 deployment/kube-apiserver
    for i in {1..36}; do
        STATUS=$(${OC} get deployment -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} kube-apiserver -o jsonpath='{.status.conditions[?(@.type=="Available")].status}')
        if [ "$STATUS" == "True" ]; then
            break
        fi

        if [ $i -eq 36 ]; then
            echo "$( date ) restart_kube_apiserver: timed-out waiting for kube-apiserver to be restarted"
        else
            sleep 5
        fi
    done

    ${OC} scale -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --replicas=0 deployment/openshift-route-controller-manager
    ${OC} scale -n ${HC_CLUSTER_NS}-${HC_CLUSTER_NAME} --replicas=3 deployment/openshift-route-controller-manager
}

function readd_appliedmanifestwork_ownerref() {
    echo "$( date ) readd_appliedmanifestwork_ownerref: update applied manifestworks"

    export KUBECONFIG=${MGMT2_KUBECONFIG}
    export AMW=$(${OC} get appliedmanifestwork --no-headers -o custom-columns=name:.metadata.name | grep ${HC_CLUSTER_NAME}$)
    export AMW_UID=$(${OC} get appliedmanifestwork $AMW -o go-template='{{ .metadata.uid }}')
    export AMW_NAME=$(${OC} get appliedmanifestwork $AMW -o go-template='{{ .metadata.name }}')
    ${OC} -n ${HC_CLUSTER_NS} patch hostedcluster ${HC_CLUSTER_NAME} --patch "{\"metadata\":{\"ownerReferences\":[{\"apiVersion\":\"work.open-cluster-management.io/v1\",\"kind\":\"AppliedManifestWork\",\"name\":\"$AMW_NAME\",\"uid\":\"$AMW_UID\"}]}}" --type=merge
}

function teardown_old_svc() {
    echo "$( date ) teardown_old_svc: delete old manifestworks from service cluster"

    export KUBECONFIG=${MGMT_KUBECONFIG}
    ${OC} delete manifestwork -n ${MGMT_CLUSTER_NAME} addon-config-policy-controller-deploy-hosting-${HC_CLUSTER_NAME}-0 addon-governance-policy-framework-deploy-hosting-${HC_CLUSTER_NAME}-0 addon-work-manager-deploy-hosting-${HC_CLUSTER_NAME}-0 ${HC_CLUSTER_NAME}-hosted-klusterlet
}

helpFunc()
{
   echo ""
   echo "Usage: $0 \n use -b to run the backup and use -r to run restore"
   exit 1
}

if [ -z $HC_CLUSTER_NAME ]; then
    echo "No value for HC_CLUSTER_NAME parameter specified"
    exit 1
fi
#shift
echo $HC_CLUSTER_NAME
if [ -z $HC_CLUSTER_NS ]; then
    echo "No value for HC_CLUSTER_NS specified"
    exit 1
fi

if [ -z $AGENT_NAMESPACE ]; then
    echo "No value for AGENT_NAMESPACE parameter specified. Provide the value by using the -p parameter."
    exit 1
fi

HC_CLUSTER_DIR="${WORKSPACE_DIR}/${HC_CLUSTER_NAME}"
HC_KUBECONFIG="${HC_CLUSTER_DIR}/kubeconfig"
BACKUP_DIR=${HC_CLUSTER_DIR}/backup

SEDCMD="sed"
if [ "$(uname -s)" == "Darwin" ]; then
    SEDCMD="gsed"
fi

commands=("base64" "curl" "jq" "oc" "openssl" "yq" "${SEDCMD}" "kubectl" "aws")
for cmd in "${commands[@]}"
do
    echo "Checking to see if $cmd command is available..."
    command -v $cmd
done

OC="oc"

## Backup
function backup(){
    echo "$( date ) Creating Backup of the HC"
    SECONDS=0
    backup_hc
    echo "$( date ) Backup Done!"
    ELAPSED="Elapsed: $(($SECONDS / 3600))hrs $((($SECONDS / 60) % 60))min $(($SECONDS % 60))sec"
    echo "$( date ) $ELAPSED"
}

## Migration
function restore() {
    SECONDS=0
    echo "$( date )  Executing the HC Migration"
    restore_hc
    echo "$( date )  Restoration Done!"
    ELAPSED="Elapsed: $(($SECONDS / 3600))hrs $((($SECONDS / 60) % 60))min $(($SECONDS % 60))sec"
    echo "$( date ) $ELAPSED"
}

## Teardown - not running this atm
function teardown() {
    SECONDS=0
    echo "$( date )  Tearing down the HC in Source Management Cluster"
    #teardown_old_svc
    teardown_old_hc
    restart_kube_apiserver
    set +x
    get_hc_kubeconfig 200
    set -x
    restore_ovn_pods
    # Hosted Cluster is up, perform remaining cleanup tasks
    teardown_old_klusterlet
    readd_appliedmanifestwork_ownerref
    echo "$( date )  Teardown Done"
    ELAPSED="Elapsed: $(($SECONDS / 3600))hrs $((($SECONDS / 60) % 60))min $(($SECONDS % 60))sec"
    echo "$( date ) $ELAPSED"
}

while getopts "brid:" opt
do
   case "$opt" in
      b ) backup ;;
      r ) restore ;;
      ? ) helpFunc ;;
   esac
done