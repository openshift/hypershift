package components

import (
	"bytes"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"k8s.io/utils/cpuset"
)

const bitsInWord = 32

// GetComponentName returns the component name for the specific performance profile
func GetComponentName(profileName string, prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, profileName)
}

// GetFirstKeyAndValue return the first key / value pair of a map
func GetFirstKeyAndValue(m map[string]string) (string, string) {
	for k, v := range m {
		return k, v
	}
	return "", ""
}

// SplitLabelKey returns the given label key splitted up in domain and role
func SplitLabelKey(s string) (domain, role string, err error) {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("Can't split %s", s)
	}
	return parts[0], parts[1], nil
}

// CPUListToHexMask converts a list of cpus into a cpu mask represented in hexdecimal
func CPUListToHexMask(cpulist string) (hexMask string, err error) {
	cpus, err := cpuset.Parse(cpulist)
	if err != nil {
		return "", err
	}

	reservedCPUs := cpus.List()
	currMask := big.NewInt(0)
	for _, cpu := range reservedCPUs {
		x := new(big.Int).Lsh(big.NewInt(1), uint(cpu))
		currMask.Or(currMask, x)
	}
	return fmt.Sprintf("%0x", currMask), nil
}

// CPUListToMaskList converts a list of cpus into a cpu mask represented
// in a list of hexadecimal mask divided by a delimiter ","
func CPUListToMaskList(cpulist string) (hexMask string, err error) {
	maskStr, err := CPUListToHexMask(cpulist)
	if err != nil {
		return "", nil
	}

	// Make sure the raw mask can be processed in 8 character chunks
	padding_needed := len(maskStr) % 8
	if padding_needed != 0 {
		padding_needed = 8 - padding_needed
		maskStr = strings.Repeat("0", padding_needed) + maskStr
	}

	index := 0
	for index < (len(maskStr) - 8) {
		if maskStr[index:index+8] != "00000000" {
			break
		}
		index = index + 8
	}
	var b bytes.Buffer
	for index <= (len(maskStr) - 16) {
		b.WriteString(maskStr[index : index+8])
		b.WriteString(",")
		index = index + 8
	}
	b.WriteString(maskStr[index : index+8])
	trimmedCPUMaskList := b.String()
	return trimmedCPUMaskList, nil
}

// CPULists allows easy checks between the different cpu set definitions
type CPULists struct {
	sets map[string]cpuset.CPUSet
}

// Intersect returns cpu ids found in both the provided cpuLists, if any
func Intersect(firstSet cpuset.CPUSet, secondSet cpuset.CPUSet) []int {
	commonSet := firstSet.Intersection(secondSet)
	return commonSet.List()
}

func (c *CPULists) GetIsolated() cpuset.CPUSet {
	return c.sets["isolated"]
}

func (c *CPULists) GetReserved() cpuset.CPUSet {
	return c.sets["reserved"]
}

func (c *CPULists) GetOfflined() cpuset.CPUSet {
	return c.sets["offlined"]
}

func (c *CPULists) GetShared() cpuset.CPUSet {
	return c.sets["shared"]
}

func (c *CPULists) GetSets() map[string]cpuset.CPUSet {
	return c.sets
}

// NewCPULists parse text representations of reserved and isolated cpusets definition and returns a CPULists object
func NewCPULists(reserved, isolated, offlined, shared string) (*CPULists, error) {
	reservedSet, err := cpuset.Parse(reserved)
	if err != nil {
		return nil, err
	}
	isolatedSet, err := cpuset.Parse(isolated)
	if err != nil {
		return nil, err
	}
	offlinedSet, err := cpuset.Parse(offlined)
	if err != nil {
		return nil, err
	}
	sharedSet, err := cpuset.Parse(shared)
	if err != nil {
		return nil, err
	}
	return &CPULists{
		sets: map[string]cpuset.CPUSet{
			"reserved": reservedSet,
			"isolated": isolatedSet,
			"offlined": offlinedSet,
			"shared":   sharedSet,
		},
	}, nil
}

// CPUMaskToCPUSet parses a CPUSet received in a Mask Format, see:
// https://man7.org/linux/man-pages/man7/cpuset.7.html#FORMATS
func CPUMaskToCPUSet(cpuMask string) (cpuset.CPUSet, error) {
	chunks := strings.Split(cpuMask, ",")

	// reverse the chunks order
	n := len(chunks)
	for i := 0; i < n/2; i++ {
		chunks[i], chunks[n-i-1] = chunks[n-i-1], chunks[i]
	}

	cpuSet := cpuset.New()
	for i, chunk := range chunks {
		if chunk == "" {
			return cpuSet, fmt.Errorf("malformed CPU mask %q chunk %q", cpuMask, chunk)
		}
		mask, err := strconv.ParseUint(chunk, 16, bitsInWord)
		if err != nil {
			return cpuSet, fmt.Errorf("failed to parse the CPU mask %q (chunk %q): %v", cpuMask, chunk, err)
		}
		for j := 0; j < bitsInWord; j++ {
			if mask&1 == 1 {
				ids := cpuSet.List()
				ids = append(ids, i*bitsInWord+j)
				cpuSet = cpuset.New(ids...)
			}
			mask >>= 1
		}
	}

	return cpuSet, nil
}

func ListToString(cpus []int) string {
	items := make([]string, len(cpus))
	for idx, cpu := range cpus {
		items[idx] = strconv.FormatInt(int64(cpu), 10)
	}
	return strings.Join(items, ",")
}
