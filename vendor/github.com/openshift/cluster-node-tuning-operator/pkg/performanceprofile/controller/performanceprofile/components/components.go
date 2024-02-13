/*
 * Copyright 2023 Red Hat, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package components

import (
	apiconfigv1 "github.com/openshift/api/config/v1"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

type Options struct {
	ProfileMCP    *mcov1.MachineConfigPool
	MachineConfig MachineConfigOptions
}

type MachineConfigOptions struct {
	PinningMode      *apiconfigv1.CPUPartitioningMode
	DefaultRuntime   mcov1.ContainerRuntimeDefaultRuntime
	MixedCPUsEnabled bool
}

type KubeletConfigOptions struct {
	MachineConfigPoolSelector map[string]string
	MixedCPUsEnabled          bool
}
