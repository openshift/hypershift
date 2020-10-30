package roks

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

func includeVPNFunc(includeVPN bool) func() bool {
	return func() bool {
		return includeVPN
	}
}

func imageFunc(images map[string]string) func(string) string {
	return func(imageName string) string {
		return images[imageName]
	}
}

func versionFunc(versions map[string]string) func(string) string {
	return func(component string) string {
		return versions[component]
	}
}

func pkiFunc(pkiDir string) func(string) string {
	return func(fileName string) string {
		file := filepath.Join(pkiDir, fileName)
		if _, err := os.Stat(file); err != nil {
			panic(err.Error())
		}
		b, err := ioutil.ReadFile(file)
		if err != nil {
			panic(err.Error())
		}
		return base64.StdEncoding.EncodeToString(b)
	}
}

func includePKIFunc(pkiDir string) func(string, int) string {
	return func(fileName string, indent int) string {
		file := filepath.Join(pkiDir, fileName)
		if _, err := os.Stat(file); err != nil {
			panic(err.Error())
		}
		b, err := ioutil.ReadFile(file)
		if err != nil {
			panic(err.Error())
		}
		input := bytes.NewBuffer(b)
		output := &bytes.Buffer{}
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			fmt.Fprintf(output, "%s%s\n", strings.Repeat(" ", indent), scanner.Text())
		}
		return output.String()
	}
}

func base64Func(params interface{}, rc *renderContext) func(string) string {
	return func(fileName string) string {
		result, err := rc.substituteParams(params, fileName)
		if err != nil {
			panic(err.Error())
		}
		return base64.StdEncoding.EncodeToString([]byte(result))
	}
}

func includeDataFunc() func(string, int) string {
	return func(data string, indent int) string {
		input := bytes.NewBufferString(data)
		output := &bytes.Buffer{}
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			fmt.Fprintf(output, "%s%s\n", strings.Repeat(" ", indent), scanner.Text())
		}
		return output.String()
	}
}

func includeFileFunc(params interface{}, rc *renderContext) func(string, int) string {
	return func(fileName string, indent int) string {
		result, err := rc.substituteParams(params, fileName)
		if err != nil {
			panic(err.Error())
		}
		includeFn := includeDataFunc()
		return includeFn(result, indent)
	}
}

func cidrAddress(cidr string) string {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err.Error())
	}
	return ip.String()
}

func cidrMask(cidr string) string {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err.Error())
	}
	m := ipNet.Mask
	if len(m) != 4 {
		panic("Expecting a 4-byte mask")
	}
	return fmt.Sprintf("%d.%d.%d.%d", m[0], m[1], m[2], m[3])
}

// randomString uses RawURLEncoding to ensure we do not get / characters or trailing ='s
func randomString(size int) string {
	// each byte (8 bits) gives us 4/3 base64 (6 bits) characters
	// we account for that conversion and add one to handle truncation
	b64size := base64.RawURLEncoding.DecodedLen(size) + 1
	// trim down to the original requested size since we added one above
	return base64.RawURLEncoding.EncodeToString(randomBytes(b64size))[:size]
}

func randomBytes(size int) []byte {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		panic(err) // rand should never fail
	}
	return b
}

func indent(spaces int, v string) string {
	pad := strings.Repeat(" ", spaces)
	return pad + strings.Replace(v, "\n", "\n"+pad, -1)
}

func base64StringEncode(inputString string) string {
	return base64.StdEncoding.EncodeToString([]byte(inputString))
}

func trimTrailingSpace(s string) string {
	return strings.TrimRightFunc(s, unicode.IsSpace)
}
