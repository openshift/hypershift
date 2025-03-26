/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	"fmt"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	coreapis "sigs.k8s.io/karpenter/pkg/apis"
	karpv1beta1 "sigs.k8s.io/karpenter/pkg/apis/v1beta1"

	"github.com/aws/karpenter-provider-aws/pkg/apis"
)

func init() {
	karpv1beta1.RestrictedLabelDomains = karpv1beta1.RestrictedLabelDomains.Insert(RestrictedLabelDomains...)
	karpv1beta1.WellKnownLabels = karpv1beta1.WellKnownLabels.Insert(
		LabelInstanceHypervisor,
		LabelInstanceEncryptionInTransitSupported,
		LabelInstanceCategory,
		LabelInstanceFamily,
		LabelInstanceGeneration,
		LabelInstanceSize,
		LabelInstanceLocalNVME,
		LabelInstanceCPU,
		LabelInstanceCPUManufacturer,
		LabelInstanceMemory,
		LabelInstanceEBSBandwidth,
		LabelInstanceNetworkBandwidth,
		LabelInstanceGPUName,
		LabelInstanceGPUManufacturer,
		LabelInstanceGPUCount,
		LabelInstanceGPUMemory,
		LabelInstanceAcceleratorName,
		LabelInstanceAcceleratorManufacturer,
		LabelInstanceAcceleratorCount,
		LabelTopologyZoneID,
		corev1.LabelWindowsBuild,
	)
}

var (
	TerminationFinalizer   = apis.Group + "/termination"
	AWSToKubeArchitectures = map[string]string{
		"x86_64":                      karpv1beta1.ArchitectureAmd64,
		karpv1beta1.ArchitectureArm64: karpv1beta1.ArchitectureArm64,
	}
	WellKnownArchitectures = sets.NewString(
		karpv1beta1.ArchitectureAmd64,
		karpv1beta1.ArchitectureArm64,
	)
	RestrictedLabelDomains = []string{
		apis.Group,
	}
	RestrictedTagPatterns = []*regexp.Regexp{
		// Adheres to cluster name pattern matching as specified in the API spec
		// https://docs.aws.amazon.com/eks/latest/APIReference/API_CreateCluster.html
		regexp.MustCompile(`^kubernetes\.io/cluster/[0-9A-Za-z][A-Za-z0-9\-_]*$`),
		regexp.MustCompile(fmt.Sprintf("^%s$", regexp.QuoteMeta(karpv1beta1.NodePoolLabelKey))),
		regexp.MustCompile(fmt.Sprintf("^%s$", regexp.QuoteMeta(karpv1beta1.ManagedByAnnotationKey))),
		regexp.MustCompile(fmt.Sprintf("^%s$", regexp.QuoteMeta(LabelNodeClass))),
		regexp.MustCompile(fmt.Sprintf("^%s$", regexp.QuoteMeta(TagNodeClaim))),
	}
	AMIFamilyBottlerocket                          = "Bottlerocket"
	AMIFamilyAL2                                   = "AL2"
	AMIFamilyAL2023                                = "AL2023"
	AMIFamilyUbuntu                                = "Ubuntu"
	AMIFamilyWindows2019                           = "Windows2019"
	AMIFamilyWindows2022                           = "Windows2022"
	AMIFamilyCustom                                = "Custom"
	Windows2019                                    = "2019"
	Windows2022                                    = "2022"
	WindowsCore                                    = "Core"
	Windows2019Build                               = "10.0.17763"
	Windows2022Build                               = "10.0.20348"
	ResourceNVIDIAGPU          corev1.ResourceName = "nvidia.com/gpu"
	ResourceAMDGPU             corev1.ResourceName = "amd.com/gpu"
	ResourceAWSNeuron          corev1.ResourceName = "aws.amazon.com/neuron"
	ResourceHabanaGaudi        corev1.ResourceName = "habana.ai/gaudi"
	ResourceAWSPodENI          corev1.ResourceName = "vpc.amazonaws.com/pod-eni"
	ResourcePrivateIPv4Address corev1.ResourceName = "vpc.amazonaws.com/PrivateIPv4Address"
	ResourceEFA                corev1.ResourceName = "vpc.amazonaws.com/efa"

	LabelNodeClass = apis.Group + "/ec2nodeclass"

	LabelTopologyZoneID = "topology.k8s.aws/zone-id"

	LabelInstanceHypervisor                   = apis.Group + "/instance-hypervisor"
	LabelInstanceEncryptionInTransitSupported = apis.Group + "/instance-encryption-in-transit-supported"
	LabelInstanceCategory                     = apis.Group + "/instance-category"
	LabelInstanceFamily                       = apis.Group + "/instance-family"
	LabelInstanceGeneration                   = apis.Group + "/instance-generation"
	LabelInstanceLocalNVME                    = apis.Group + "/instance-local-nvme"
	LabelInstanceSize                         = apis.Group + "/instance-size"
	LabelInstanceCPU                          = apis.Group + "/instance-cpu"
	LabelInstanceCPUManufacturer              = apis.Group + "/instance-cpu-manufacturer"
	LabelInstanceMemory                       = apis.Group + "/instance-memory"
	LabelInstanceEBSBandwidth                 = apis.Group + "/instance-ebs-bandwidth"
	LabelInstanceNetworkBandwidth             = apis.Group + "/instance-network-bandwidth"
	LabelInstanceGPUName                      = apis.Group + "/instance-gpu-name"
	LabelInstanceGPUManufacturer              = apis.Group + "/instance-gpu-manufacturer"
	LabelInstanceGPUCount                     = apis.Group + "/instance-gpu-count"
	LabelInstanceGPUMemory                    = apis.Group + "/instance-gpu-memory"
	LabelInstanceAcceleratorName              = apis.Group + "/instance-accelerator-name"
	LabelInstanceAcceleratorManufacturer      = apis.Group + "/instance-accelerator-manufacturer"
	LabelInstanceAcceleratorCount             = apis.Group + "/instance-accelerator-count"
	AnnotationEC2NodeClassHash                = apis.Group + "/ec2nodeclass-hash"
	AnnotationEC2NodeClassHashVersion         = apis.Group + "/ec2nodeclass-hash-version"
	AnnotationInstanceTagged                  = apis.Group + "/tagged"

	TagNodeClaim             = coreapis.Group + "/nodeclaim"
	TagManagedLaunchTemplate = apis.Group + "/cluster"
	TagName                  = "Name"
)
