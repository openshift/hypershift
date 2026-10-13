#!/bin/bash

# Azure login helper script for HyperShift managed Azure setup
# This script logs into Azure with the correct scope for managing Azure resources

echo "Logging into Azure with management scope..."
az login --scope https://management.core.windows.net//.default

if [ $? -eq 0 ]; then
    echo "Successfully logged into Azure!"
    echo "You can now run the setup scripts."
else
    echo "Azure login failed. Please check your credentials and try again."
    exit 1
fi 