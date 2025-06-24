# Azure CI Resources for HyperShift

This directory contains resources and documentation for managing HyperShift Azure CI infrastructure and operations.

## Overview

This folder provides resources for:
- Creating and managing Azure OpenShift clusters for HyperShift CI
- Managing Azure cloud resources and DNS configurations
- Setting up CI infrastructure on Azure

## Directory Structure

### `Create Azure OpenShift Cluster/`
Contains documentation and configuration files for setting up Azure OpenShift management clusters for HyperShift CI:

- **README.md**: Complete guide for creating an Azure OpenShift management cluster from an AWS cluster, including:
  - Prerequisites (OpenShift CLI, OCP cluster, HyperShift CLI)
  - Step-by-step cluster creation process
  - GitHub IDP configuration
  - HyperShift installation on Azure
  - CI kubeconfig setup and Vault integration
  - User permissions configuration

- **hypershift-install-azure.yaml**: Kubernetes manifests for installing HyperShift operator on Azure OpenShift management cluster, including:
  - Namespace and ServiceAccount configuration
  - RBAC setup for cluster-admin access
  - ImageStream for HyperShift operator
  - Deployment configuration for the installer

### `Manage Azure Cloud Resources/`
Contains utilities and documentation for managing Azure cloud infrastructure:

- **Deleting-DNS-Zone-Recordsets.md**: Comprehensive guide for managing DNS zone recordsets, including:
  - Scripts for listing and filtering DNS recordsets
  - Safe deletion procedure with test runs
  - Bulk deletion commands for cleaning up CI DNS zones

## Getting Started

To set up a new Azure management cluster for HyperShift CI:

1. Follow the step-by-step guide in `Create Azure OpenShift Cluster/README.md`
2. Use the provided YAML configuration in `hypershift-install-azure.yaml` for HyperShift installation
3. Refer to Azure cloud resource management documentation as needed

## Prerequisites

- OpenShift CLI (oc)
- OCP cluster (ROSA)
- HyperShift CLI
- Azure credentials (available from Vault or generated following Microsoft documentation)
- Access to OpenShift CI Vault for credential storage

## Security Notes

- Azure credentials are stored securely in Vault
- Follow the resource pruning guide to preserve management clusters
- Proper RBAC is configured for secure access control

## Related Documentation

- [Azure Resource Pruning Guide](https://source.redhat.com/groups/public/openshift/openshift_wiki/openshift_dev_microsoft_azure__azure_government#jive_content_id_Resource_pruning)
- [GitHub Identity Provider Configuration](https://docs.openshift.com/container-platform/4.8/authentication/identity_providers/configuring-github-identity-provider.html)
- [Azure Service Principal Creation](https://learn.microsoft.com/en-us/entra/identity-platform/howto-create-service-principal-portal)
