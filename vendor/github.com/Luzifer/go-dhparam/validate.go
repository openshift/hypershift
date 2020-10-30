package dhparam

import (
	"math/big"

	"github.com/pkg/errors"
)

const dhCheckPNotPrime = 0x01
const dhCheckPNotSafePrime = 0x02
const dhUnableToCheckGenerator = 0x04
const dhNotSuitableGenerator = 0x08
const dhCheckQNotPrime = 0x10
const dhCheckInvalidQValue = 0x20
const dhCheckInvalidJValue = 0x40

// ErrAllParametersOK is defined to check whether the returned error from Check is indeed no error
// For simplicity reasons it is defined as an error instead of an additional result parameter
var ErrAllParametersOK = errors.New("DH parameters appear to be ok")

// Check returns a number of errors and an "ok" bool. If the "ok" bool is set to true, still one
// error is returned: ErrAllParametersOK. If "ok" is false, the error list will contain at least
// one error not being equal to ErrAllParametersOK.
func (d DH) Check() ([]error, bool) {
	var (
		result = []error{}
		ok     = true
	)

	i := d.check()

	if i&dhCheckPNotPrime > 0 {
		result = append(result, errors.New("WARNING: p value is not prime"))
		ok = false
	}

	if i&dhCheckPNotSafePrime > 0 {
		result = append(result, errors.New("WARNING: p value is not a safe prime"))
		ok = false
	}

	if i&dhCheckQNotPrime > 0 {
		result = append(result, errors.New("WARNING: q value is not a prime"))
		ok = false
	}

	if i&dhCheckInvalidQValue > 0 {
		result = append(result, errors.New("WARNING: q value is invalid"))
		ok = false
	}

	if i&dhCheckInvalidJValue > 0 {
		result = append(result, errors.New("WARNING: j value is invalid"))
		ok = false
	}

	if i&dhUnableToCheckGenerator > 0 {
		result = append(result, errors.New("WARNING: unable to check the generator value"))
		ok = false
	}

	if i&dhNotSuitableGenerator > 0 {
		result = append(result, errors.New("WARNING: the g value is not a generator"))
		ok = false
	}

	if i == 0 {
		result = append(result, ErrAllParametersOK)
	}

	return result, ok
}

func (d DH) check() int {
	var ret int

	// Check generator
	switch d.G {
	case 2:
		l := new(big.Int)
		if l.Mod(d.P, big.NewInt(24)); l.Int64() != 11 {
			ret |= dhNotSuitableGenerator
		}
	case 5:
		l := new(big.Int)
		if l.Mod(d.P, big.NewInt(10)); l.Int64() != 3 && l.Int64() != 7 {
			ret |= dhNotSuitableGenerator
		}
	default:
		ret |= dhUnableToCheckGenerator
	}

	if !d.P.ProbablyPrime(1) {
		ret |= dhCheckPNotPrime
	} else {
		t1 := new(big.Int)
		t1.Rsh(d.P, 1)
		if !t1.ProbablyPrime(1) {
			ret |= dhCheckPNotSafePrime
		}
	}

	return ret
}
