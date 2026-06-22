package certificates

import (
	"crypto/x509"
	"fmt"
	"sort"

	certificatesv1 "k8s.io/api/certificates/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

var keyUsageDict = map[certificatesv1.KeyUsage]x509.KeyUsage{
	certificatesv1.UsageSigning:           x509.KeyUsageDigitalSignature,
	certificatesv1.UsageDigitalSignature:  x509.KeyUsageDigitalSignature,
	certificatesv1.UsageContentCommitment: x509.KeyUsageContentCommitment,
	certificatesv1.UsageKeyEncipherment:   x509.KeyUsageKeyEncipherment,
	certificatesv1.UsageKeyAgreement:      x509.KeyUsageKeyAgreement,
	certificatesv1.UsageDataEncipherment:  x509.KeyUsageDataEncipherment,
	certificatesv1.UsageCertSign:          x509.KeyUsageCertSign,
	certificatesv1.UsageCRLSign:           x509.KeyUsageCRLSign,
	certificatesv1.UsageEncipherOnly:      x509.KeyUsageEncipherOnly,
	certificatesv1.UsageDecipherOnly:      x509.KeyUsageDecipherOnly,
}

var extKeyUsageDict = map[certificatesv1.KeyUsage]x509.ExtKeyUsage{
	certificatesv1.UsageAny:             x509.ExtKeyUsageAny,
	certificatesv1.UsageServerAuth:      x509.ExtKeyUsageServerAuth,
	certificatesv1.UsageClientAuth:      x509.ExtKeyUsageClientAuth,
	certificatesv1.UsageCodeSigning:     x509.ExtKeyUsageCodeSigning,
	certificatesv1.UsageEmailProtection: x509.ExtKeyUsageEmailProtection,
	certificatesv1.UsageSMIME:           x509.ExtKeyUsageEmailProtection,
	certificatesv1.UsageIPsecEndSystem:  x509.ExtKeyUsageIPSECEndSystem,
	certificatesv1.UsageIPsecTunnel:     x509.ExtKeyUsageIPSECTunnel,
	certificatesv1.UsageIPsecUser:       x509.ExtKeyUsageIPSECUser,
	certificatesv1.UsageTimestamping:    x509.ExtKeyUsageTimeStamping,
	certificatesv1.UsageOCSPSigning:     x509.ExtKeyUsageOCSPSigning,
	certificatesv1.UsageMicrosoftSGC:    x509.ExtKeyUsageMicrosoftServerGatedCrypto,
	certificatesv1.UsageNetscapeSGC:     x509.ExtKeyUsageNetscapeServerGatedCrypto,
}

// KeyUsagesFromStrings will translate a slice of usage strings from the
// certificates API ("pkg/apis/certificates".KeyUsage) to x509.KeyUsage and
// x509.ExtKeyUsage types.
func KeyUsagesFromStrings(usages []certificatesv1.KeyUsage) (x509.KeyUsage, []x509.ExtKeyUsage, error) {
	var keyUsage x509.KeyUsage
	var unrecognized []certificatesv1.KeyUsage
	extKeyUsageSet := sets.New[x509.ExtKeyUsage]()
	for _, usage := range usages {
		if val, ok := keyUsageDict[usage]; ok {
			keyUsage |= val
		} else if val, ok := extKeyUsageDict[usage]; ok {
			extKeyUsageSet.Insert(val)
		} else {
			unrecognized = append(unrecognized, usage)
		}
	}

	extKeyUsages := extKeyUsageSet.UnsortedList()
	sort.Slice(extKeyUsages, func(i, j int) bool {
		return extKeyUsages[i] < extKeyUsages[j]
	})

	if len(unrecognized) > 0 {
		return 0, nil, fmt.Errorf("unrecognized usage values: %q", unrecognized)
	}

	return keyUsage, extKeyUsages, nil
}
