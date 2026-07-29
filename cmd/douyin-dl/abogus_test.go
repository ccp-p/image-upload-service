package main

import (
	"encoding/hex"
	"strings"
	"testing"
)

// Classic RC4 test vector (key="Wiki", plaintext="pedia").
func TestRC4Vector(t *testing.T) {
	ct := rc4Encrypt([]byte("Wiki"), []byte("pedia"))
	got := hex.EncodeToString(ct)
	want := "1021bf0420"
	if got != want {
		t.Fatalf("rc4(Wiki,pedia) = %s, want %s", got, want)
	}
}

// Fixed inputs so the Go a_bogus can be compared byte-for-byte against the
// Python original via verify_abogus.py (run on a machine with gmssl installed).
const (
	testUA    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36"
	testFP    = "1536|864|1560|939|0|0|0|0|1920|1080|1920|1040|1536|864|24|24|Win32"
	testNowMs = int64(1751000000000) // 2025-06 -> exercises the >255 timestamp code points
)

var testParams = "device_platform=webapp&aid=6383&channel=channel_pc_web&aweme_id=7380308675841297704"

// fixed random bytes mirror StringProcessor.generate_random_bytes for _rd=5000
// (three identical sequences -> 12 code points).
var testRandom = []int{137, 2, 7, 57, 137, 2, 7, 57, 137, 2, 7, 57}

func newTestSigner() *abogusSigner {
	return &abogusSigner{
		userAgent:   testUA,
		browserFP:   testFP,
		options:     [3]int{0, 1, 14},
		now:         func() int64 { return testNowMs },
		randomBytes: func() []int { return append([]int(nil), testRandom...) },
	}
}

func TestABogusDeterministic(t *testing.T) {
	s := newTestSigner()
	_, ab1, _ := s.generate(testParams, "")
	_, ab2, _ := s.generate(testParams, "")
	if ab1 != ab2 {
		t.Fatalf("a_bogus not deterministic: %s != %s", ab1, ab2)
	}
	t.Logf("a_bogus = %s", ab1)
}

func TestABogusStructure(t *testing.T) {
	s := newTestSigner()
	signed, ab, _ := s.generate(testParams, "")

	if !strings.HasPrefix(signed, testParams+"&a_bogus=") {
		t.Fatalf("signed params missing a_bogus suffix: %s", signed)
	}
	if !strings.HasSuffix(signed, ab) {
		t.Fatalf("signed params tail mismatch")
	}
	if len(ab)%4 != 0 {
		t.Fatalf("a_bogus length %d not multiple of 4", len(ab))
	}
	allowed := abogusAlphabet0 + "="
	for _, r := range ab {
		if !strings.ContainsRune(allowed, r) {
			t.Fatalf("a_bogus contains invalid char %q", r)
		}
	}
}
