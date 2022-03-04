package util

import "k8s.io/apimachinery/pkg/api/resource"

const (
	RHCOSOpenStackChecksumParameter string = "{rhcos:openstack:checksum}"
	RHCOSMagicVolumeName            string = "rhcos"
	RHCOSOpenStackURLParam          string = "{rhcos:openstack:url}"
)

var KubeVirtVolumeDefaultSize = resource.MustParse("16Gi")
