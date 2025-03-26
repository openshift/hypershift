# General
This directory contains several developer-focused scripts and instructions related to setting up managed Azure (aka ARO 
HCP).

There are no guarantees these scripts will work 100% with your setup. If you do have issues with these scripts or need 
further help. Please reach out to #project-hypershift on Red Hat Slack.

Review each of the scripts beforehand to make sure you are accomplishing any prerequisites required for running the 
script. If you are starting out with nothing set up, you'll want to go through the scripts in this order:

1. [Review Steps 1-7 to Set Up Environment Variables and Such](../../docs/content/how-to/azure/create-azure-cluster_on_aks.md)
2. [Create an AKS management cluster](setup_aks_cluster.sh)
3. [Set up externalDNS](setup_external_dns.sh)
4. [Install the HyperShift Operator](setup_install_ho_on_aks.sh)
5. [Create a HostedCluster](create_basic_hosted_cluster.sh)