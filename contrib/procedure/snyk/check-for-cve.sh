#!/bin/bash

search_image() {
    local version=$1
    local image_to_check
    local hypershift_image

    echo -e "\n===== Searching CPO Image Version ${version} ====="
    image_to_check=$(curl -q "https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/${version}.0-0.nightly/latest?rel=1" 2>/dev/null | jq -r .pullSpec 2>/dev/null)
    hypershift_image=$(oc adm release info "${image_to_check}" -a "${PULL_SECRETS_FILE}" --pullspecs | grep hypershift | awk '{print $2}')

    if [[ -n "${hypershift_image}" ]]; then
        echo "Image: ${hypershift_image}"
        docker pull "${hypershift_image}" --quiet
        docker run -it --entrypoint=sh "${hypershift_image}" -c "${SHELL_CMD}" 2>/dev/null
    else
        echo "Failed to retrieve hypershift image for version ${version}"
    fi
    echo "============================================="
}

PULL_SECRETS_FILE=${PULL_SECRET:-$HOME/pull-secret.txt}

SEARCH_TERMS=$1
GREP_PATTERN=$(echo "${SEARCH_TERMS}" | tr ',' '|')
SHELL_CMD="rpm -qa | grep -Ei '${GREP_PATTERN}'"

if [[ -z "${SEARCH_TERMS}" ]]; then
    echo "Error: SEARCH_TERMS is required."
    exit 1
fi

echo -e "\n===== Searching Latest (4.16 -> 4.18) HO Image ====="
docker pull quay.io/acm-d/rhtap-hypershift-operator:latest --quiet
docker run -it --entrypoint=sh quay.io/acm-d/rhtap-hypershift-operator:latest -c "${SHELL_CMD}" 2>/dev/null
echo "============================================="
echo -e "\n===== Searching 4.14 HO Image ====="
docker pull quay.io/openshift/origin-base:4.14 --quiet
docker run -it --entrypoint=sh quay.io/openshift/origin-base:4.14 -c "${SHELL_CMD}" 2>/dev/null
echo "============================================="
echo -e "\n===== Searching 4.15 HO Image ====="
docker pull quay.io/openshift/origin-base:4.15 --quiet
docker run -it --entrypoint=sh quay.io/openshift/origin-base:4.15 -c "${SHELL_CMD}" 2>/dev/null
echo "============================================="

for version in 4.14 4.15 4.16 4.17 4.18; do
    search_image "${version}"
done

echo -e "\nSearch completed."
