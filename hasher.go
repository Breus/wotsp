package wotsp

import (
	"encoding/binary"
	"hash"
	"reflect"
)

// The hasher struct implements the W-OTS+ functions PRF and HashF efficiently
// by precomputing part of the hash digests. Using precomputation improves
// performance by ~41%.
//
// Since the PRF function calculates H(toByte(3, 32) || seed || M), where seed
// can be the secret or public seed, the first 64 bytes of the input are
// recomputed on every evaluation of PRF. We can significantly improve
// performance by precomputing the hash digest for this part of the input.
//
// For HashF we can only precompute the first 32 bytes of hash digest: it
// calculates H(toByte(0, 32) || key || M) where key is the result of an
// evaluation of PRF.
type hasher struct {
	// Precomputed hash digests
	precompPrfPubSeed  reflect.Value
	precompPrfPrivSeed reflect.Value
	precompHashF       reflect.Value

	// params based on the mode
	params params

	// Hash function instances
	hasher []hash.Hash
	// Hash digests of hasher
	hasherVal []reflect.Value
}

func newHasher(privSeed, pubSeed []byte, opts Opts, nrRoutines int) (h *hasher, err error) {
	chash, err := opts.hash()
	if err != nil {
		return
	}

	h = new(hasher)
	h.hasher = make([]hash.Hash, nrRoutines)
	h.hasherVal = make([]reflect.Value, nrRoutines)

	if h.params, err = opts.Mode.params(); err != nil {
		return
	}

	for i := 0; i < nrRoutines; i++ {
		h.hasher[i] = chash.New()
		h.hasherVal[i] = reflect.ValueOf(h.hasher[i]).Elem()
	}

	padding := make([]byte, N)

	// While padding is all zero, precompute hashF
	hashHashF := chash.New()
	hashHashF.Write(padding)
	h.precompHashF = reflect.ValueOf(hashHashF).Elem()

	// Set padding for prf
	binary.BigEndian.PutUint16(padding[N-2:], uint16(3))

	if privSeed != nil {
		// Precompute prf with private seed (not used in PkFromSig)
		hashPrfSk := chash.New()
		hashPrfSk.Write(padding)
		hashPrfSk.Write(privSeed)
		h.precompPrfPrivSeed = reflect.ValueOf(hashPrfSk).Elem()
	}

	// Precompute prf with public seed
	hashPrfPub := chash.New()
	hashPrfPub.Write(padding)
	hashPrfPub.Write(pubSeed)
	h.precompPrfPubSeed = reflect.ValueOf(hashPrfPub).Elem()

	return
}

//
// PRF with precomputed hash digests for pub and priv seeds
//

func (h *hasher) hashF(routineNr int, key, inout []byte) {
	h.hasherVal[routineNr].Set(h.precompHashF)
	h.hasher[routineNr].Write(key)
	h.hasher[routineNr].Write(inout)
	h.hasher[routineNr].Sum(inout[:0])
}

func (h *hasher) prfPubSeed(routineNr int, addr *Address, out []byte) {
	h.hasherVal[routineNr].Set(h.precompPrfPubSeed)
	h.hasher[routineNr].Write(addr.data[:])
	h.hasher[routineNr].Sum(out[:0]) // Must make sure that out's capacity is >= 32 bytes!
}

func (h *hasher) prfPrivSeed(routineNr int, ctr []byte, out []byte) {
	h.hasherVal[routineNr].Set(h.precompPrfPrivSeed)
	h.hasher[routineNr].Write(ctr)
	h.hasher[routineNr].Sum(out[:0]) // Must make sure that out's capacity is >= 32 bytes!
}

// Computes the base-16 representation of a binary input.
func (h *hasher) baseW(x []byte, outlen int) []uint8 {
	var total byte
	in := 0
	out := 0
	bits := uint(0)
	baseW := make([]uint8, outlen)

	logW := h.params.logW
	w := h.params.w

	for consumed := 0; consumed < outlen; consumed++ {
		if bits == 0 {
			total = x[in]
			in++
			bits += 8
		}

		bits -= logW
		baseW[out] = uint8((total >> bits) & byte(w-1))
		out++
	}

	return baseW
}

// Performs the chaining operation using an n-byte input and n-byte seed.
// Assumes the input is the <start>-th element in the chain, and performs
// <steps> iterations.
//
// Scratch is used as a scratch pad: it is pre-allocated to prevent every call
// to chain from allocating slices for keys and bitmask. It is used as:
// 		scratch = key || bitmask.
func (h *hasher) chain(routineNr int, scratch, in, out []byte, start, steps uint8, adrs *Address) {
	copy(out, in)

	w := h.params.w

	for i := start; i < start+steps && i < w; i++ {
		adrs.setHash(uint32(i))

		adrs.setKeyAndMask(0)
		h.prfPubSeed(routineNr, adrs, scratch[:32])
		adrs.setKeyAndMask(1)
		h.prfPubSeed(routineNr, adrs, scratch[32:64])

		for j := 0; j < N; j++ {
			out[j] = out[j] ^ scratch[32+j]
		}

		h.hashF(routineNr, scratch[:32], out)
	}
}

// Expands a 32-byte seed into an (l*n)-byte private key.
func (h *hasher) expandSeed() []byte {
	l := h.params.l

	privKey := make([]byte, l*N)
	ctr := make([]byte, 32)

	for i := 0; i < l; i++ {
		binary.BigEndian.PutUint16(ctr[30:], uint16(i))
		h.prfPrivSeed(0, ctr, privKey[i*N:])
	}

	return privKey
}

func (h *hasher) checksum(msg []uint8) []uint8 {
	l1, l2, w, logW := h.params.l1, h.params.l2, h.params.w, h.params.logW

	csum := uint32(0)
	for i := 0; i < l1; i++ {
		csum += uint32(w - 1 - msg[i])
	}
	csum <<= 8 - ((uint(l2) * logW) % 8)

	// Length of the checksum is (l2*logw + 7) / 8
	csumBytes := make([]byte, 2)
	// Since bytesLen is always 2, we can truncate csum to a uint16.
	binary.BigEndian.PutUint16(csumBytes, uint16(csum))

	return h.baseW(csumBytes, l2)
}

// Distributes the chains that must be computed between numRoutine goroutines.
//
// When fromSig is true, in contains a signature and out must be a public key;
// in this case the routines must complete the signature chains so they use
// lengths as start indices. If fromSig is false, we are either computing a
// public key from a private key, or a signature from a private key, so the
// routines use lengths as the amount of iterations to perform.
func (h *hasher) computeChains(numRoutines int, in, out []byte, lengths []uint8, adrs *Address, p params, fromSig bool) {
	chainsPerRoutine := (p.l-1)/numRoutines + 1

	// Initialise scratch pad
	scratch := make([]byte, numRoutines*64)

	done := make(chan struct{}, numRoutines)

	for i := 0; i < numRoutines; i++ {
		// NOTE: address is passed by value here, since this creates a new
		// reference.
		go func(nr int, scratch []byte, adrs Address) {
			firstChain := nr * chainsPerRoutine
			lastChain := firstChain + chainsPerRoutine - 1

			// Make sure the last routine ends at the right chain
			if lastChain >= p.l {
				lastChain = p.l - 1
			}

			// Compute the hash chains
			for j := firstChain; j <= lastChain; j++ {
				adrs.setChain(uint32(j))
				if fromSig {
					h.chain(nr, scratch, in[j*N:(j+1)*N], out[j*N:(j+1)*N], lengths[j], p.w-1-lengths[j], &adrs)
				} else {
					h.chain(nr, scratch, in[j*N:(j+1)*N], out[j*N:(j+1)*N], 0, lengths[j], &adrs)
				}
			}

			done <- struct{}{}
		}(i, scratch[i*64:(i+1)*64], *adrs)
	}

	for i := 0; i < numRoutines; i++ {
		<-done
	}
}
