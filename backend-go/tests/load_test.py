#!/usr/bin/env python3
"""
Quick concurrent load test against a running gateway.

Usage:
    python3 tests/load_test.py [base_url] [concurrency] [requests_per_worker]

Exercises GET /api/logs (DB read) + GET /v1/models (DB read) to show
real-world sustained RPS.
"""
import concurrent.futures
import os
import sys
import time

import requests

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:5002"
CONCURRENCY = int(sys.argv[2]) if len(sys.argv) > 2 else 20
PER_WORKER = int(sys.argv[3]) if len(sys.argv) > 3 else 100
ENDPOINTS = [
    ("/api/healthz", None),
    ("/v1/models", {"Authorization": "Bearer sk-bogus"}),  # expect 401 fast
]


def fire(_i):
    results = []
    for path, hdrs in ENDPOINTS:
        t0 = time.perf_counter()
        try:
            r = requests.get(f"{BASE}{path}", headers=hdrs or {}, timeout=10)
            ms = (time.perf_counter() - t0) * 1000
            results.append((path, r.status_code, ms))
        except Exception as e:
            results.append((path, f"ERR:{e}", -1))
    return results


def main():
    print(f"load test: {BASE}  concurrency={CONCURRENCY}  req/worker={PER_WORKER}")
    t0 = time.perf_counter()
    with concurrent.futures.ThreadPoolExecutor(max_workers=CONCURRENCY) as ex:
        futs = [ex.submit(fire, i) for i in range(CONCURRENCY * PER_WORKER)]
        all_results = [r for f in futs for r in f.result()]
    elapsed = time.perf_counter() - t0
    total = len(all_results)
    rps = total / elapsed

    by_path: dict[str, list[tuple[int, float]]] = {}
    for path, code, ms in all_results:
        by_path.setdefault(path, []).append((code, ms))

    print(f"\ntotal: {total} requests in {elapsed:.2f}s  ({rps:.0f} rps)\n")
    print(f"{'path':<20} {'count':>6} {'ok':>6} {'err':>6} {'avg_ms':>9} {'p95_ms':>9} {'max_ms':>9}")
    print("-" * 70)
    for path, samples in by_path.items():
        codes = [c for c, _ in samples]
        ok = sum(1 for c in codes if isinstance(c, int) and 200 <= c < 500)
        err = sum(1 for c in codes if not (isinstance(c, int) and 200 <= c < 500))
        times = sorted(m for _, m in samples if m >= 0)
        if not times:
            continue
        avg = sum(times) / len(times)
        p95 = times[int(len(times) * 0.95)]
        mx = times[-1]
        print(f"{path:<20} {len(samples):>6} {ok:>6} {err:>6} {avg:>9.1f} {p95:>9.1f} {mx:>9.1f}")


if __name__ == "__main__":
    main()
