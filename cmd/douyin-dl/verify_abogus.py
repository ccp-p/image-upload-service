#!/usr/bin/env python3
"""Cross-validate the Go a_bogus port against the original utils/abogus.py.

Run on a machine with gmssl installed (pip install gmssl):
    python verify_abogus.py

It monkeypatches time/random to fixed values so the output is deterministic,
then prints the a_bogus. Compare it to the value logged by:
    go test -run TestABogusDeterministic -v
in abogus_test.go -- they must match exactly.
"""
import sys

# Point this at your local clone of jiji262/douyin-downloader.
REPO = r"C:\tmp\douyin-downloader"
if REPO not in sys.path:
    sys.path.insert(0, REPO)

import time as _time
from utils import abogus as _abogus

# Fixed inputs -- MUST match abogus_test.go constants.
TEST_UA = ("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
           "(KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36")
TEST_FP = "1536|864|1560|939|0|0|0|0|1920|1080|1920|1040|1536|864|24|24|Win32"
TEST_NOW_MS = 1751000000000
TEST_PARAMS = ("device_platform=webapp&aid=6383&channel=channel_pc_web"
               "&aweme_id=7380308675841297704")

# Fixed random bytes: three sequences for _rd=5000 -> [137,2,7,57] each.
TEST_RANDOM = [137, 2, 7, 57, 137, 2, 7, 57, 137, 2, 7, 57]


_time.time = lambda: TEST_NOW_MS / 1000.0
_abogus.StringProcessor.generate_random_bytes = staticmethod(
    lambda length=3: "".join(chr(x) for x in TEST_RANDOM)
)


def main():
    signer = _abogus.ABogus(user_agent=TEST_UA, fp=TEST_FP)
    params_with_ab, abogus, ua, _ = signer.generate_abogus(TEST_PARAMS, "")
    print("a_bogus =", abogus)


if __name__ == "__main__":
    main()