package main

import (
	"encoding/binary"
	"math/bits"
)

// sm3 implements the Chinese SM3 hash (GB/T 32905-2016). The upstream Python
// abogus signer hashes params/bodies/UA through gmssl's sm3, so a
// byte-identical implementation is required here.

var sm3IV = [8]uint32{
	0x7380166f, 0x4914b2b9, 0x172442d7, 0xda8a0600,
	0xa96f30bc, 0x163138aa, 0xe38dee4d, 0xb0fb0e4e,
}

func sm3T(j int) uint32 {
	if j < 16 {
		return 0x79cc4519
	}
	return 0x7a879d8a
}

func sm3FF(x, y, z uint32, j int) uint32 {
	if j < 16 {
		return x ^ y ^ z
	}
	return (x & y) | (x & z) | (y & z)
}

func sm3GG(x, y, z uint32, j int) uint32 {
	if j < 16 {
		return x ^ y ^ z
	}
	return (x & y) | (^x & z)
}

func sm3P0(x uint32) uint32 { return x ^ bits.RotateLeft32(x, 9) ^ bits.RotateLeft32(x, 17) }
func sm3P1(x uint32) uint32 { return x ^ bits.RotateLeft32(x, 15) ^ bits.RotateLeft32(x, 23) }

// sm3Sum returns the 32-byte SM3 digest of data.
func sm3Sum(data []byte) [32]byte {
	msgLen := uint64(len(data) * 8)
	padded := make([]byte, len(data), len(data)+72)
	copy(padded, data)
	padded = append(padded, 0x80)
	for len(padded)%64 != 56 {
		padded = append(padded, 0)
	}
	var lenBytes [8]byte
	binary.BigEndian.PutUint64(lenBytes[:], msgLen)
	padded = append(padded, lenBytes[:]...)

	var v [8]uint32 = sm3IV
	for off := 0; off < len(padded); off += 64 {
		var w [68]uint32
		var w1 [64]uint32
		for i := 0; i < 16; i++ {
			w[i] = binary.BigEndian.Uint32(padded[off+i*4:])
		}
		for j := 16; j < 68; j++ {
			w[j] = sm3P1(w[j-16]^w[j-9]^bits.RotateLeft32(w[j-3], 15)) ^ bits.RotateLeft32(w[j-13], 7) ^ w[j-6]
		}
		for j := 0; j < 64; j++ {
			w1[j] = w[j] ^ w[j+4]
		}

		a, b, c, d, e, f, g, h := v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7]
		for j := 0; j < 64; j++ {
			rotA := bits.RotateLeft32(a, 12)
			ss1 := bits.RotateLeft32(rotA+e+bits.RotateLeft32(sm3T(j), j%32), 7)
			ss2 := ss1 ^ rotA
			tt1 := sm3FF(a, b, c, j) + d + ss2 + w1[j]
			tt2 := sm3GG(e, f, g, j) + h + ss1 + w[j]
			d = c
			c = bits.RotateLeft32(b, 9)
			b = a
			a = tt1
			h = g
			g = bits.RotateLeft32(f, 19)
			f = e
			e = sm3P0(tt2)
		}
		v[0] ^= a
		v[1] ^= b
		v[2] ^= c
		v[3] ^= d
		v[4] ^= e
		v[5] ^= f
		v[6] ^= g
		v[7] ^= h
	}

	var out [32]byte
	for i, word := range v {
		binary.BigEndian.PutUint32(out[i*4:], word)
	}
	return out
}

// sm3Bytes returns the SM3 digest as a slice of 32 ints, matching Python's
// sm3_to_array (which returns a list of ints in 0-255). The abogus pipeline
// feeds these into code-point arrays, so []int is the right representation.
func sm3Bytes(data []byte) []int {
	sum := sm3Sum(data)
	out := make([]int, 32)
	for i, b := range sum {
		out[i] = int(b)
	}
	return out
}
