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
	"context"

	apiconfigv1 "github.com/openshift/api/config/v1"
	mcov1 "github.com/openshift/api/machineconfiguration/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Handler interface {
	// Delete deletes the components owned by the controller
	Delete(ctx context.Context, profileName string) error
	// Exists checks for the existences of the components owned by the controller
	Exists(ctx context.Context, profileName string) bool
	// Apply applies the desired state to the components owned by the controller, or creates them if not exists
	Apply(ctx context.Context, obj client.Object, recorder record.EventRecorder, options *Options) error
}

type Options struct {
	ProfileMCP                  *mcov1.MachineConfigPool
	MachineConfig               MachineConfigOptions
	MixedCPUsFeatureGateEnabled bool
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
