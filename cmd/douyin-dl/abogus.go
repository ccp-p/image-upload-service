package main

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// abogus.go is a faithful Go port of utils/abogus.py from jiji262/douyin-downloader.
//
// Key fidelity note: the Python code builds intermediate "strings" out of chr()
// over values that can exceed 255 (timestamp high bytes reach ~407 today), so
// Python operates on Unicode code points, not bytes. We therefore represent those
// intermediate Python strings as []int (one int per code point) throughout the
// pipeline. chr(x)->x, ord(c)->c, and ISO-8859-1 encode/decode becomes identity.

const abogusSalt = "cus"

var abogusAlphabet0 = "Dkdpgh2ZmsQB80/MfvV36XI1R45-WUAlEixNLwoqYTOPuzKFjJnry79HbGcaStCe"
var abogusAlphabet1 = "ckdp1h4ZKsUB80/Mfvw36XIgR25+WQAlEi7NLboqYTOPuzmFjJnryx9HVGDaStCe"

// bigArray mirrors CryptoUtility.big_array in abogus.py exactly (do not reorder).
var abogusBigArray = [256]int{
	121, 243, 55, 234, 103, 36, 47, 228, 30, 231, 106, 6, 115, 95, 78, 101, 250, 207, 198, 50,
	139, 227, 220, 105, 97, 143, 34, 28, 194, 215, 18, 100, 159, 160, 43, 8, 169, 217, 180, 120,
	247, 45, 90, 11, 27, 197, 46, 3, 84, 72, 5, 68, 62, 56, 221, 75, 144, 79, 73, 161,
	178, 81, 64, 187, 134, 117, 186, 118, 16, 241, 130, 71, 89, 147, 122, 129, 65, 40, 88, 150,
	110, 219, 199, 255, 181, 254, 48, 4, 195, 248, 208, 32, 116, 167, 69, 201, 17, 124, 125, 104,
	96, 83, 80, 127, 236, 108, 154, 126, 204, 15, 20, 135, 112, 158, 13, 1, 188, 164, 210, 237,
	222, 98, 212, 77, 253, 42, 170, 202, 26, 22, 29, 182, 251, 10, 173, 152, 58, 138, 54, 141,
	185, 33, 157, 31, 252, 132, 233, 235, 102, 196, 191, 223, 240, 148, 39, 123, 92, 82, 128, 109,
	57, 24, 38, 113, 209, 245, 2, 119, 153, 229, 189, 214, 230, 174, 232, 63, 52, 205, 86, 140,
	66, 175, 111, 171, 246, 133, 238, 193, 99, 60, 74, 91, 225, 51, 76, 37, 145, 211, 166, 151,
	213, 206, 0, 200, 244, 176, 218, 44, 184, 172, 49, 216, 93, 168, 53, 21, 183, 41, 67, 85,
	224, 155, 226, 242, 87, 177, 146, 70, 190, 12, 162, 19, 137, 114, 25, 165, 163, 192, 23, 59,
	9, 94, 179, 107, 35, 7, 142, 131, 239, 203, 149, 136, 61, 249, 14, 156,
}

var abogusSortIndex = []int{
	18, 20, 52, 26, 30, 34, 58, 38, 40, 53, 42, 21, 27, 54, 55, 31, 35, 57, 39, 41, 43, 22, 28,
	32, 60, 36, 23, 29, 33, 37, 44, 45, 59, 46, 47, 48, 49, 50, 24, 25, 65, 66, 70, 71,
}

var abogusSortIndex2 = []int{
	18, 20, 26, 30, 34, 38, 40, 42, 21, 27, 31, 35, 39, 41, 43, 22, 28, 32, 36, 23, 29, 33, 37,
	44, 45, 46, 47, 48, 49, 50, 24, 25, 52, 53, 54, 55, 57, 58, 59, 60, 65, 66, 70, 71,
}

// abogusSigner mirrors the ABogus class. A fresh instance is created per
// request (matching api_client behaviour), so bigArray mutation is per-call.
type abogusSigner struct {
	userAgent  string
	browserFP  string
	options    [3]int
	now        func() int64        // injectable clock for tests
	randomBytes func() []int        // injectable random for tests
}

func newABogus(ua, fp string) *abogusSigner {
	return &abogusSigner{
		userAgent:   ua,
		browserFP:   fp,
		options:     [3]int{0, 1, 14},
		now:         func() int64 { return time.Now().UnixMilli() },
		randomBytes: defaultRandomBytes,
	}
}

// generateChromeFingerprint mirrors BrowserFingerprintGenerator._generate_fingerprint("Win32").
// Uses the global rand source (auto-seeded since Go 1.20).
func generateChromeFingerprint() string {
	randInt := func(lo, hi int) int { return lo + rand.Intn(hi-lo+1) }
	innerW := randInt(1024, 1920)
	innerH := randInt(768, 1080)
	outerW := innerW + randInt(24, 32)
	outerH := innerH + randInt(75, 90)
	screenY := 0
	if rand.Intn(2) == 1 {
		screenY = 30
	}
	sizeW := randInt(1024, 1920)
	sizeH := randInt(768, 1080)
	availW := randInt(1280, 1920)
	availH := randInt(800, 1080)
	return fmt.Sprintf("%d|%d|%d|%d|%d|%d|0|0|%d|%d|%d|%d|%d|%d|24|24|%s",
		innerW, innerH, outerW, outerH, 0, screenY,
		sizeW, sizeH, availW, availH, innerW, innerH, "Win32")
}

// defaultRandomBytes mirrors StringProcessor.generate_random_bytes(length=3).
func defaultRandomBytes() []int {
	r := rand.Intn(10000)
	seq := func() []int {
		return []int{
			((r & 255) & 170) | 1,
			((r & 255) & 85) | 2,
			(((r % 0x100000000) >> 8) & 170) | 5,
			(((r % 0x100000000) >> 8) & 85) | 40,
		}
	}
	out := make([]int, 0, 12)
	for i := 0; i < 3; i++ {
		out = append(out, seq()...)
		r = rand.Intn(10000)
	}
	return out
}

// sm3OfStr salts (optionally) and SM3-hashes a string (UTF-8 encoded).
func (a *abogusSigner) sm3OfStr(s string, salt bool) []int {
	if salt {
		s = s + abogusSalt
	}
	return sm3Bytes([]byte(s))
}

// sm3OfArr SM3-hashes a code-point list treated as raw bytes (values 0-255).
func (a *abogusSigner) sm3OfArr(arr []int) []int {
	b := make([]byte, len(arr))
	for i, x := range arr {
		b[i] = byte(x)
	}
	return sm3Bytes(b)
}

// rc4Encrypt mirrors CryptoUtility.rc4_encrypt (key/data are byte slices).
func rc4Encrypt(key, data []byte) []byte {
	s := make([]int, 256)
	for i := range s {
		s[i] = i
	}
	j := 0
	for i := 0; i < 256; i++ {
		j = (j + s[i] + int(key[i%len(key)])) % 256
		s[i], s[j] = s[j], s[i]
	}
	i, jj := 0, 0
	out := make([]byte, len(data))
	for k := range data {
		i = (i + 1) % 256
		jj = (jj + s[i]) % 256
		s[i], s[jj] = s[jj], s[i]
		out[k] = data[k] ^ byte(s[(s[i]+s[jj])%256])
	}
	return out
}

// base64Encode mirrors CryptoUtility.base64_encode over code points 0-255.
func (a *abogusSigner) base64Encode(in []int, alphabetIdx int) string {
	alphabet := abogusAlphabet0
	if alphabetIdx == 1 {
		alphabet = abogusAlphabet1
	}
	var bits []int
	for _, cp := range in {
		for b := 7; b >= 0; b-- {
			bits = append(bits, (cp>>b)&1)
		}
	}
	pad := (6 - len(bits)%6) % 6
	for i := 0; i < pad; i++ {
		bits = append(bits, 0)
	}
	var sb strings.Builder
	for i := 0; i < len(bits); i += 6 {
		idx := 0
		for j := 0; j < 6; j++ {
			idx = (idx << 1) | bits[i+j]
		}
		sb.WriteByte(alphabet[idx])
	}
	for i := 0; i < pad/2; i++ {
		sb.WriteByte('=')
	}
	return sb.String()
}

// transformBytes mirrors CryptoUtility.transform_bytes; mutates ba in place.
func transformBytes(ba *[256]int, in []int) []int {
	result := make([]int, 0, len(in))
	indexB := ba[1]
	initialValue := 0
	valueE := 0
	for index, charValue := range in {
		var sumInitial int
		if index == 0 {
			initialValue = ba[indexB]
			sumInitial = indexB + initialValue
			ba[1] = initialValue
			ba[indexB] = indexB
		} else {
			sumInitial = initialValue + valueE
		}
		sumInitial %= 256
		valueF := ba[sumInitial]
		result = append(result, charValue^valueF)
		valueE = ba[(index+2)%256]
		sumInitial = (indexB + valueE) % 256
		initialValue = ba[sumInitial]
		ba[sumInitial] = ba[(index+2)%256]
		ba[(index+2)%256] = initialValue
		indexB = sumInitial
	}
	return result
}

// abogusEncode mirrors CryptoUtility.abogus_encode over code points (may exceed 255).
func (a *abogusSigner) abogusEncode(in []int, alphabetIdx int) string {
	alphabet := abogusAlphabet0
	if alphabetIdx == 1 {
		alphabet = abogusAlphabet1
	}
	js := []int{18, 12, 6, 0}
	ks := []int{0xFC0000, 0x03F000, 0x0FC0, 0x3F}
	var sb strings.Builder
	for i := 0; i < len(in); i += 3 {
		var n int
		switch {
		case i+2 < len(in):
			n = (in[i] << 16) | (in[i+1] << 8) | in[i+2]
		case i+1 < len(in):
			n = (in[i] << 16) | (in[i+1] << 8)
		default:
			n = in[i] << 16
		}
		for idx, j := range js {
			if j == 6 && i+1 >= len(in) {
				break
			}
			if j == 0 && i+2 >= len(in) {
				break
			}
			sb.WriteByte(alphabet[(n&ks[idx])>>j])
		}
	}
	pad := (4 - sb.Len()%4) % 4
	for i := 0; i < pad; i++ {
		sb.WriteByte('=')
	}
	return sb.String()
}

// generate mirrors ABogus.generate_abogus. Returns (params&a_bogus=..., abogus, userAgent).
func (a *abogusSigner) generate(params, body string) (string, string, string) {
	var ba [256]int
	ba = abogusBigArray

	aid := 6383
	pageID := 0
	start := a.now()

	// array1 = sm3(sm3(params + salt)), array2 = sm3(sm3(body + salt))
	array1 := a.sm3OfArr(a.sm3OfStr(params, true))
	array2 := a.sm3OfArr(a.sm3OfStr(body, true))
	// array3 = sm3(base64(rc4(uaKey, ua)), no salt)
	rc4Out := rc4Encrypt([]byte{0x00, 0x01, 0x0e}, []byte(a.userAgent))
	rc4Cps := make([]int, len(rc4Out))
	for i, b := range rc4Out {
		rc4Cps[i] = int(b)
	}
	array3 := a.sm3OfStr(a.base64Encode(rc4Cps, 1), false)

	end := a.now()

	opts := a.options
	abDir := map[int]int{8: 3, 66: 0, 69: 0, 70: 0, 71: 0}
	get := func(k int) int { if v, ok := abDir[k]; ok { return v }; return 0 }

	abDir[20] = int((start >> 24) & 255)
	abDir[21] = int((start >> 16) & 255)
	abDir[22] = int((start >> 8) & 255)
	abDir[23] = int(start & 255)
	abDir[24] = int(start / 256 / 256 / 256 / 256)
	abDir[25] = int(start / 256 / 256 / 256 / 256 / 256)
	abDir[26] = int((opts[0] >> 24) & 255)
	abDir[27] = int((opts[0] >> 16) & 255)
	abDir[28] = int((opts[0] >> 8) & 255)
	abDir[29] = int(opts[0] & 255)
	abDir[30] = int(opts[1]/256) & 255
	abDir[31] = int(opts[1]%256) & 255
	abDir[32] = int((opts[1] >> 24) & 255)
	abDir[33] = int((opts[1] >> 16) & 255)
	abDir[34] = int((opts[2] >> 24) & 255)
	abDir[35] = int((opts[2] >> 16) & 255)
	abDir[36] = int((opts[2] >> 8) & 255)
	abDir[37] = int(opts[2] & 255)
	abDir[38] = array1[21]
	abDir[39] = array1[22]
	abDir[40] = array2[21]
	abDir[41] = array2[22]
	abDir[42] = array3[23]
	abDir[43] = array3[24]
	abDir[44] = int((end >> 24) & 255)
	abDir[45] = int((end >> 16) & 255)
	abDir[46] = int((end >> 8) & 255)
	abDir[47] = int(end & 255)
	abDir[48] = get(8)
	abDir[49] = int(end / 256 / 256 / 256 / 256)
	abDir[50] = int(end / 256 / 256 / 256 / 256 / 256)
	abDir[51] = int((pageID >> 24) & 255)
	abDir[52] = int((pageID >> 16) & 255)
	abDir[53] = int((pageID >> 8) & 255)
	abDir[54] = int(pageID & 255)
	abDir[55] = pageID
	abDir[56] = aid
	abDir[57] = int(aid & 255)
	abDir[58] = int((aid >> 8) & 255)
	abDir[59] = int((aid >> 16) & 255)
	abDir[60] = int((aid >> 24) & 255)
	abDir[64] = len(a.browserFP)
	abDir[65] = len(a.browserFP)

	sortedValues := make([]int, 0, len(abogusSortIndex)+len(a.browserFP)+1)
	for _, k := range abogusSortIndex {
		sortedValues = append(sortedValues, get(k))
	}
	for _, r := range a.browserFP {
		sortedValues = append(sortedValues, int(r))
	}
	abXor := 0
	for i := 0; i < len(abogusSortIndex2)-1; i++ {
		if i == 0 {
			abXor = get(abogusSortIndex2[0])
		}
		abXor ^= get(abogusSortIndex2[i+1])
	}
	sortedValues = append(sortedValues, abXor)

	abogusBytesStr := append(a.randomBytes(), transformBytes(&ba, sortedValues)...)
	abogus := a.abogusEncode(abogusBytesStr, 0)
	signed := params + "&a_bogus=" + abogus
	return signed, abogus, a.userAgent
}
