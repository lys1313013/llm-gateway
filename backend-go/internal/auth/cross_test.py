#!/usr/bin/env python3
"""
Cross-language password-hash compatibility test.

Generates a Werkzeug pbkdf2 hash, then asks the Go CLI to verify it.
Then has the Go CLI produce a hash, and verifies that one here.

Builds the `pwhashcheck` helper from backend-go/cmd/pwhashcheck.
"""
import os
import subprocess
import sys

from werkzeug.security import check_password_hash, generate_password_hash


def main() -> int:
    go_bin = os.environ.get("GO_BIN", "go")
    workdir = os.environ.get("WORKDIR", ".")
    password = "llm_gateway"

    # 1. Build the pwhashcheck helper
    build = subprocess.run(
        [go_bin, "build", "-o", "/tmp/pwhashcheck", "./cmd/pwhashcheck"],
        cwd=workdir, capture_output=True, text=True,
    )
    if build.returncode != 0:
        print("FAILED to build pwhashcheck:")
        print(build.stdout, build.stderr)
        return 1

    # 2. Werkzeug-generated hash
    werk_hash = generate_password_hash(
        password, method="pbkdf2:sha256", salt_length=16
    )
    print(f"werkzeug hash: {werk_hash}")

    # 3. Verify the Werkzeug hash from Go
    res = subprocess.run(
        ["/tmp/pwhashcheck", "verify", werk_hash, password],
        capture_output=True, text=True,
    )
    print("Go verify (Werkzeug hash):", res.stdout.strip(), res.stderr.strip())
    if res.returncode != 0:
        print("FAILED: Go could not verify Werkzeug hash")
        return 1

    # 4. Have Go generate a hash, verify it from Python
    res = subprocess.run(
        ["/tmp/pwhashcheck", "gen", password],
        capture_output=True, text=True,
    )
    go_hash = res.stdout.strip()
    print(f"go hash: {go_hash}")
    if not check_password_hash(go_hash, password):
        print("FAILED: Python could not verify Go hash")
        return 1
    print("Python verify (Go hash): True")

    print("\nALL OK: Go ↔ Werkzeug pbkdf2 round-trip verified")
    return 0


if __name__ == "__main__":
    sys.exit(main())
