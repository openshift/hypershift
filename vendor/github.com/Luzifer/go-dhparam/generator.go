package dhparam

import (
	"context"
	"crypto/rand"
	"math/big"

	"github.com/pkg/errors"
)

const pemHeader = "DH PARAMETERS"

// GeneratorResult is a type of results sent to the GeneratorCallback function
type GeneratorResult uint

const (
	// GeneratorFoundPossiblePrime signals a possible (non-verified) prime number was found (OpenSSL: ".")
	GeneratorFoundPossiblePrime GeneratorResult = iota
	// GeneratorFirstConfirmation signals the prime number itself was verified but is not yet considered "safe" (OpenSSL: "+")
	GeneratorFirstConfirmation
	// GeneratorSafePrimeFound signals the prime number now is considered "safe" (OpenSSL: "*")
	GeneratorSafePrimeFound
)

// Generator is the generator number to use when determining the prime number
type Generator int

const (
	// GeneratorTwo uses a generator 2
	GeneratorTwo Generator = 2
	// GeneratorFive uses a generator 5
	GeneratorFive = 5
)

// GeneratorCallback is a type of function to receive GeneratorResults while the prime number is determined
type GeneratorCallback func(r GeneratorResult)

func nullCallback(r GeneratorResult) {}

// Generate determines a prime number according to the generator having the specified number of bits
//
// In OpenSSL defined generators are 2 and 5. Others are supported but the verification is not supported in an extend as with generators 2 and 5.
// The bit size should be adjusted to be high enough for the current requirements. Also you should keep
// in mind the higher the bitsize, the longer the generation might take.
func Generate(bits int, generator Generator, cb GeneratorCallback) (*DH, error) {
	// Invoke GenerateWithContext with a background context
	return GenerateWithContext(context.Background(), bits, generator, cb)
}

// GenerateWithContext is just like the Generate function, but it accepts a ctx parameter with a context, that can be used to interrupt the generation if needed
func GenerateWithContext(ctx context.Context, bits int, generator Generator, cb GeneratorCallback) (*DH, error) {
	var (
		err       error
		padd, rem int64
		prime     *big.Int
	)

	if cb == nil {
		cb = nullCallback
	}

	switch generator {
	case 2:
		padd, rem = 24, 11
	case 5:
		padd, rem = 10, 3
	default:
		padd, rem = 2, 1
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			if prime, err = genPrime(bits, big.NewInt(padd), big.NewInt(rem)); err != nil {
				return nil, err
			}

			if prime.BitLen() > bits {
				continue
			}

			t := new(big.Int)
			t.Rsh(prime, 1)

			cb(GeneratorFoundPossiblePrime)

			if prime.ProbablyPrime(0) {
				cb(GeneratorFirstConfirmation)
			} else {
				continue
			}

			if t.ProbablyPrime(0) {
				cb(GeneratorSafePrimeFound)
				return &DH{
					P: prime,
					G: int(generator),
				}, nil
			}
		}
	}
}

func genPrime(bits int, padd, rem *big.Int) (*big.Int, error) {
	var (
		err  error
		p    = new(big.Int)
		qadd = new(big.Int)
		q    = new(big.Int)
		t1   = new(big.Int)
	)

	bits--

	qadd.Rsh(padd, 1)

	if q, err = genRand(bits); err != nil {
		return nil, err
	}

	t1.Mod(q, qadd)
	q.Sub(q, t1)

	t1.Rsh(rem, 1)
	q.Add(q, t1)

	p.Lsh(q, 1)
	p.Add(p, big.NewInt(1))

	for !mightBePrime(p) || !mightBePrime(q) {
		p.Add(p, padd)
		q.Add(q, qadd)
	}

	return p, nil
}

func mightBePrime(i *big.Int) bool {
	m := new(big.Int)
	for _, p := range quickTestPrimes {
		if m.Mod(i, big.NewInt(p)).Int64() == 0 {
			return false
		}
	}
	return true
}

func genRand(bits int) (*big.Int, error) {
	bytes := (bits + 7) / 8
	bit := (bits - 1) % 8
	mask := 0xff << uint(bit+1)

	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return nil, errors.Wrap(err, "Unable to read random")
	}

	if bit == 0 {
		buf[0] = 1
		buf[1] |= 0x80
	} else {
		buf[0] |= (3 << uint(bit-1))
	}

	buf[0] &= byte(^mask)

	buf[bytes-1] |= 1

	r := new(big.Int)
	return r.SetBytes(buf), nil
}
