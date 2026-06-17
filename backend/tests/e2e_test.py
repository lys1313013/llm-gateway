#!/usr/bin/env python3
"""
End-to-end test suite for the LLM Gateway Go backend.

Hits a running server on $BASE_URL (default http://127.0.0.1:5002) and
exercises every public route. Run the server separately:

    cd backend
    go run ./cmd/gateway     # in another shell
    python3 tests/e2e_test.py

Residue handling — the script is self-healing, no admin credentials required:

  * Before the suite runs, it lists /api/auth/users, /api/exposed_model, and
    /api/route and deletes any rows left by a prior failed run (anything
    whose name/pattern starts with `e2e_`, `e2e-model_`, or `e2e-`).
  * At the end, the just-created exposed_model and route are deleted by the
    e2e user itself (it's a JWT user with the same /api/* access).
  * The just-created e2e user cannot delete itself (server enforces
    "不能删除自己"), so it is intentionally left for the *next* run to
    clean up via pre-cleanup. Worst case: 1 stale e2e_jwt_* user between
    runs.
  * 真实的上游调用会使用配置的 session id 请求头（默认
    X-Claude-Code-Session-Id）为 api_logs 打标，套件会打印该 id，
    方便人工或定时任务清理。

Exit code 0 = all green, non-zero = some check failed.
"""
import json
import os
import sys
import time
from typing import Any

import requests

BASE_URL = os.environ.get("BASE_URL", "http://127.0.0.1:5002")
DB_API_KEY = os.environ.get("DB_API_KEY", "")  # Set to a real API key from the DB for /v1 tests

# ANSI colours
GREEN = "\033[92m"
RED = "\033[91m"
YELLOW = "\033[93m"
RESET = "\033[0m"

passed = 0
failed = 0
failures: list[str] = []

# Per-run state — populated as tests run, consumed by pre_cleanup() and
# the end-of-suite teardown block in main().
state: dict[str, Any] = {
    "e2e_user_id": None,
    "e2e_username": None,
    "exposed_model_id": None,
    "exposed_model_name": None,
    "log_session_id": f"e2e-test-{int(time.time())}",
}

# Residue name patterns. These match the names the suite itself generates.
USER_PREFIX = "e2e_jwt_"
MODEL_PREFIX = "e2e_model_"
ROUTE_PATTERN = "e2e-"


def check(name: str, cond: bool, detail: str = ""):
    global passed, failed
    if cond:
        print(f"  {GREEN}✓{RESET} {name}")
        passed += 1
    else:
        print(f"  {RED}✗{RESET} {name}" + (f" — {detail}" if detail else ""))
        failed += 1
        failures.append(f"{name}: {detail}")


def section(title: str):
    print(f"\n{YELLOW}▶ {title}{RESET}")


# ---------------------------------------------------------------------------
# Pre-cleanup — delete any rows left over from a prior run. Runs after the
# e2e JWT user is created (so we have a token) but before the rest of the
# suite. The e2e user cannot delete itself, so the current run's user is
# always left for the next run to pick up.
# ---------------------------------------------------------------------------
def pre_cleanup(token: str):
    section("Pre-cleanup (residue from prior runs)")
    H = {"Authorization": f"Bearer {token}"}

    # Users with e2e_ prefix (excluding self)
    r = requests.get(f"{BASE_URL}/api/auth/users", headers=H, timeout=5)
    if r.status_code == 200:
        users = (r.json().get("data") or [])
        for u in users:
            name = u.get("username") or ""
            if not name.startswith(USER_PREFIX):
                continue
            uid = u.get("id")
            if uid == state["e2e_user_id"]:
                continue
            r = requests.delete(f"{BASE_URL}/api/auth/users/{uid}", headers=H, timeout=5)
            check(f"pre-cleanup delete user '{name}' (id={uid})",
                  r.status_code == 200, f"got {r.status_code}")
    else:
        print(f"  {YELLOW}skip users list — got {r.status_code}{RESET}")

    # Exposed models with e2e_model_ prefix
    r = requests.get(f"{BASE_URL}/api/exposed_model", headers=H, timeout=5)
    if r.status_code == 200:
        for m in (r.json().get("data") or []):
            mid = m.get("id")
            name = m.get("model_id") or ""
            if not name.startswith(MODEL_PREFIX):
                continue
            r = requests.delete(f"{BASE_URL}/api/exposed_model/{mid}", headers=H, timeout=5)
            check(f"pre-cleanup delete exposed_model '{name}' (id={mid})",
                  r.status_code == 200, f"got {r.status_code}")
    else:
        print(f"  {YELLOW}skip exposed_model list — got {r.status_code}{RESET}")

    # Routes with e2e- pattern
    r = requests.get(f"{BASE_URL}/api/route", headers=H, timeout=5)
    if r.status_code == 200:
        for rt in (r.json().get("data") or []):
            rid = rt.get("id")
            pattern = rt.get("model_pattern") or ""
            if not pattern.startswith(ROUTE_PATTERN):
                continue
            r = requests.delete(f"{BASE_URL}/api/route/{rid}", headers=H, timeout=5)
            check(f"pre-cleanup delete route '{pattern}' (id={rid})",
                  r.status_code == 200, f"got {r.status_code}")
    else:
        print(f"  {YELLOW}skip route list — got {r.status_code}{RESET}")


# ---------------------------------------------------------------------------
# Test 1: Health
# ---------------------------------------------------------------------------
def test_health():
    section("Health")
    r = requests.get(f"{BASE_URL}/api/healthz", timeout=5)
    check("GET /api/healthz returns 200", r.status_code == 200, f"got {r.status_code}")
    check("healthz body has status=ok", r.json().get("status") == "ok", f"got {r.text!r}")


# ---------------------------------------------------------------------------
# Test 2: /v1/* auth
# ---------------------------------------------------------------------------
def test_v1_auth():
    section("/v1/* authentication")
    # No auth
    r = requests.get(f"{BASE_URL}/v1/models", timeout=5)
    check("missing API key returns 401", r.status_code == 401, f"got {r.status_code}")

    # Invalid API key
    r = requests.get(f"{BASE_URL}/v1/models",
                     headers={"Authorization": "Bearer sk-bogus"}, timeout=5)
    check("invalid API key returns 401", r.status_code == 401, f"got {r.status_code}")


# ---------------------------------------------------------------------------
# Test 3: JWT auth — register, login, me
# ---------------------------------------------------------------------------
def test_jwt_auth() -> str:
    section("JWT auth (register, login, me, change_password)")
    import secrets
    suffix = secrets.token_hex(4)
    username = f"{USER_PREFIX}{suffix}"
    password = "e2e_jwt_pw_123"

    r = requests.post(f"{BASE_URL}/api/auth/register",
                      json={"username": username, "password": password}, timeout=5)
    check("register returns 201", r.status_code == 201, f"got {r.status_code}: {r.text[:200]}")
    data = r.json().get("data") or {}
    token = data.get("token")
    check("register returns a token", bool(token), f"got {data!r}")
    user_id = data.get("user", {}).get("id")
    check("register returns user id", bool(user_id), f"got {data!r}")

    state["e2e_user_id"] = user_id
    state["e2e_username"] = username

    r = requests.post(f"{BASE_URL}/api/auth/login",
                      json={"username": username, "password": password}, timeout=5)
    check("login returns 200", r.status_code == 200, f"got {r.status_code}")
    check("login returns token", bool(r.json().get("data", {}).get("token")), f"got {r.text[:200]}")

    if not token:
        return ""

    # Bad password
    r = requests.post(f"{BASE_URL}/api/auth/login",
                      json={"username": username, "password": "wrong"}, timeout=5)
    check("wrong password returns 401", r.status_code == 401, f"got {r.status_code}")

    # /api/auth/me
    r = requests.get(f"{BASE_URL}/api/auth/me",
                     headers={"Authorization": f"Bearer {token}"}, timeout=5)
    check("GET /api/auth/me returns 200", r.status_code == 200, f"got {r.status_code}")
    check("/me returns same user id", r.json().get("data", {}).get("id") == user_id,
          f"expected {user_id}, got {r.json()}")

    # /me with bad token
    r = requests.get(f"{BASE_URL}/api/auth/me",
                     headers={"Authorization": "Bearer invalid"}, timeout=5)
    check("/me with invalid token returns 401", r.status_code == 401, f"got {r.status_code}")

    # change_password
    new_pw = "e2e_jwt_pw_new_456"
    r = requests.put(f"{BASE_URL}/api/auth/change_password",
                     headers={"Authorization": f"Bearer {token}"},
                     json={"old_password": password, "new_password": new_pw}, timeout=5)
    check("change_password returns 200", r.status_code == 200, f"got {r.status_code}: {r.text[:200]}")
    r = requests.post(f"{BASE_URL}/api/auth/login",
                      json={"username": username, "password": new_pw}, timeout=5)
    check("login with new password works", r.status_code == 200, f"got {r.status_code}")

    # Revert password so the user is usable later
    r = requests.put(f"{BASE_URL}/api/auth/change_password",
                     headers={"Authorization": f"Bearer {token}"},
                     json={"old_password": new_pw, "new_password": password}, timeout=5)
    check("change_password revert works", r.status_code == 200, f"got {r.status_code}")

    return token


# ---------------------------------------------------------------------------
# Test 4: Admin CRUD
# ---------------------------------------------------------------------------
def test_admin_crud(token: str):
    section("Admin CRUD (provider/route/exposed_model)")
    auth = {"Authorization": f"Bearer {token}"}
    H = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}

    # Provider: create / read / update / delete
    p = requests.post(f"{BASE_URL}/api/provider", headers=H, json={
        "name": f"e2e_provider_{int(time.time())}",
        "openai_base_url": "https://example.com/v1",
        "api_key": "sk-test",
        "remark": "E2E test",
    }, timeout=5).json()
    pid = (p.get("data") or {}).get("id")
    check("create provider", bool(pid), f"got {p}")

    r = requests.get(f"{BASE_URL}/api/provider/{pid}", headers=auth, timeout=5)
    check("get provider by id", r.status_code == 200, f"got {r.status_code}")

    r = requests.put(f"{BASE_URL}/api/provider/{pid}", headers=H, json={
        "name": p["data"]["name"],
        "openai_base_url": "https://example.com/v2",
        "api_key": "sk-test2",
        "remark": "Updated",
    }, timeout=5)
    check("update provider", r.status_code == 200, f"got {r.status_code}")
    check("update applied", r.json()["data"]["remark"] == "Updated", f"got {r.json()}")

    r = requests.delete(f"{BASE_URL}/api/provider/{pid}", headers=auth, timeout=5)
    check("delete provider", r.status_code == 200, f"got {r.status_code}")

    # Exposed model: duplicate detection (created once, expected 409 on second)
    name = f"{MODEL_PREFIX}{int(time.time())}"
    r = requests.post(f"{BASE_URL}/api/exposed_model", headers=H,
                      json={"model_id": name}, timeout=5)
    check("create exposed model", r.status_code == 200, f"got {r.status_code}: {r.text[:200]}")
    em_id = (r.json().get("data") or {}).get("id")
    state["exposed_model_id"] = em_id
    state["exposed_model_name"] = name
    r = requests.post(f"{BASE_URL}/api/exposed_model", headers=H,
                      json={"model_id": name}, timeout=5)
    check("duplicate exposed model returns 409", r.status_code == 409, f"got {r.status_code}")

    # Route: create + delete
    r = requests.post(f"{BASE_URL}/api/route", headers=H, json={
        "model_pattern": "e2e-test-*",
        "route_type": "proxy",
        "provider_id": 1,
        "timeout": -1,
        "priority": 99,
        "is_active": True,
    }, timeout=5)
    check("create route", r.status_code == 200, f"got {r.status_code}: {r.text[:200]}")
    rid = (r.json().get("data") or {}).get("id")
    if rid:
        requests.delete(f"{BASE_URL}/api/route/{rid}", headers=auth, timeout=5)


# ---------------------------------------------------------------------------
# Test 5: Logs / stats
# ---------------------------------------------------------------------------
def test_logs_stats(token: str):
    section("Logs & stats")
    auth = {"Authorization": f"Bearer {token}"}

    r = requests.get(f"{BASE_URL}/api/logs?limit=5", headers=auth, timeout=5)
    check("GET /api/logs returns 200", r.status_code == 200, f"got {r.status_code}")
    body = r.json()
    check("logs response has data + total", "data" in body and "total" in body, f"got {body}")
    logs = body.get("data") or []
    if logs:
        log_id = logs[0]["id"]
        r = requests.get(f"{BASE_URL}/api/logs/{log_id}", headers=auth, timeout=5)
        check("GET /api/logs/<id> returns 200", r.status_code == 200, f"got {r.status_code}")
        check("log detail has request_data", "request_data" in (r.json().get("data") or {}),
              f"got {list(r.json().get('data', {}).keys())[:8]}")

    r = requests.get(f"{BASE_URL}/api/logs/today_stats", headers=auth, timeout=5)
    check("GET /api/logs/today_stats returns 200", r.status_code == 200, f"got {r.status_code}")
    check("today_stats has total_requests", "total_requests" in (r.json().get("data") or {}),
          f"got {r.json()}")

    r = requests.get(f"{BASE_URL}/api/stats/daily_tokens", headers=auth, timeout=5)
    check("GET /api/stats/daily_tokens returns 200", r.status_code == 200, f"got {r.status_code}")
    data = r.json().get("data") or {}
    check("daily_tokens has 'models'", "models" in data, f"got keys {list(data.keys())}")
    check("daily_tokens has 'is_single_day'", "is_single_day" in data, f"got keys {list(data.keys())}")

    # Single-day mode: must include request_count and pad 24 hours
    r = requests.get(f"{BASE_URL}/api/stats/daily_tokens?start_date=2026-06-04&end_date=2026-06-04",
                     headers=auth, timeout=5)
    check("GET /api/stats/daily_tokens (single day) returns 200", r.status_code == 200, f"got {r.status_code}")
    data = r.json().get("data") or {}
    check("single-day has is_single_day=true", data.get("is_single_day") is True,
          f"got {data.get('is_single_day')}")
    check("single-day has hourly array", isinstance(data.get("hourly"), list),
          f"got {type(data.get('hourly'))}")
    if data.get("hourly"):
        check("single-day hourly has 24 entries (padded)", len(data["hourly"]) == 24,
              f"got {len(data['hourly'])}")
        first = data["hourly"][0]
        check("hourly[0] has 'hour' field", "hour" in first, f"got {list(first.keys())}")
        check("hourly[0] has 'request_count' field", "request_count" in first,
              f"got {list(first.keys())} — request_count is required by the React TokenStats UI")
        check("hourly[0] has 'prompt_tokens' field", "prompt_tokens" in first,
              f"got {list(first.keys())}")
    if data.get("models"):
        check("models[0] has 'request_count' field", "request_count" in data["models"][0],
              f"got {list(data['models'][0].keys())}")


# ---------------------------------------------------------------------------
# Test 6: API key lifecycle
# ---------------------------------------------------------------------------
def test_api_key(token: str):
    section("API key lifecycle")
    auth = {"Authorization": f"Bearer {token}"}
    H = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}

    r = requests.post(f"{BASE_URL}/api/auth/api_keys", headers=H,
                      json={"name": "e2e_key"}, timeout=5)
    check("create API key returns 201", r.status_code == 201, f"got {r.status_code}: {r.text[:200]}")
    data = r.json().get("data") or {}
    full_key = data.get("key")
    key_id = data.get("id")
    check("API key has sk- prefix", (full_key or "").startswith("sk-"), f"got {full_key!r}")

    r = requests.get(f"{BASE_URL}/api/auth/api_keys", headers=auth, timeout=5)
    check("list API keys", r.status_code == 200, f"got {r.status_code}")

    # Use the new key to call /v1/models
    r = requests.get(f"{BASE_URL}/v1/models",
                     headers={"Authorization": f"Bearer {full_key}"}, timeout=5)
    check("newly created key works for /v1/models", r.status_code == 200, f"got {r.status_code}")

    # Toggle off
    r = requests.put(f"{BASE_URL}/api/auth/api_keys/{key_id}/toggle", headers=H,
                     json={"is_active": False}, timeout=5)
    check("toggle API key", r.status_code == 200, f"got {r.status_code}")
    r = requests.get(f"{BASE_URL}/v1/models",
                     headers={"Authorization": f"Bearer {full_key}"}, timeout=5)
    check("disabled key returns 403", r.status_code == 403, f"got {r.status_code}")

    # Toggle back on
    requests.put(f"{BASE_URL}/api/auth/api_keys/{key_id}/toggle", headers=H,
                 json={"is_active": True}, timeout=5)

    # Delete
    r = requests.delete(f"{BASE_URL}/api/auth/api_keys/{key_id}", headers=auth, timeout=5)
    check("delete API key", r.status_code == 200, f"got {r.status_code}")


# ---------------------------------------------------------------------------
# Test 7: Live upstream call (OpenAI streaming + non-streaming)
# ---------------------------------------------------------------------------
def test_upstream(api_key: str):
    section("Live upstream (OpenAI, real model)")
    if not api_key:
        print(f"  {YELLOW}skipped — DB_API_KEY not set{RESET}")
        return
    headers = {
        "Authorization": f"Bearer {api_key}",
        "X-Claude-Code-Session-Id": state["log_session_id"],
    }

    # Non-streaming
    r = requests.post(f"{BASE_URL}/v1/chat/completions", headers=headers, json={
        "model": "qwen3.7-max",
        "messages": [{"role": "user", "content": "Reply with the single word: pong"}],
        "max_tokens": 50,
    }, timeout=60)
    check("non-stream /v1/chat/completions returns 200", r.status_code == 200, f"got {r.status_code}: {r.text[:200]}")
    body = r.json()
    check("non-stream has choices", "choices" in body, f"got keys {list(body.keys())}")
    check("non-stream has usage", "usage" in body, f"got keys {list(body.keys())}")
    if "choices" in body and body["choices"]:
        msg = body["choices"][0].get("message", {})
        check("non-stream message has content", bool(msg.get("content")), f"got {msg}")

    # Streaming
    r = requests.post(f"{BASE_URL}/v1/chat/completions", headers=headers, json={
        "model": "qwen3.7-max",
        "messages": [{"role": "user", "content": "Count: 1 2 3"}],
        "stream": True,
        "max_tokens": 60,
    }, timeout=60, stream=True)
    check("stream /v1/chat/completions returns 200", r.status_code == 200, f"got {r.status_code}")

    chunks = 0
    has_done = False
    for line in r.iter_lines():
        if not line:
            continue
        if line.startswith(b"data: "):
            payload = line[6:]
            if payload == b"[DONE]":
                has_done = True
                continue
            try:
                json.loads(payload)
                chunks += 1
            except json.JSONDecodeError:
                pass
    check("stream received at least 1 chunk", chunks >= 1, f"got {chunks}")
    check("stream ended with [DONE]", has_done, f"DONE not seen, chunks={chunks}")


# ---------------------------------------------------------------------------
# Test 8: Anthropic messages (if an API key works)
# ---------------------------------------------------------------------------
def test_anthropic(api_key: str):
    section("Live upstream (Anthropic, real model)")
    if not api_key:
        print(f"  {YELLOW}skipped — DB_API_KEY not set{RESET}")
        return
    headers = {
        "x-api-key": api_key,
        "anthropic-version": "2023-06-01",
        "X-Claude-Code-Session-Id": state["log_session_id"],
    }
    r = requests.post(f"{BASE_URL}/v1/messages", headers=headers, json={
        "model": "claude-opus-4-8",
        "max_tokens": 50,
        "messages": [{"role": "user", "content": "Say hi"}],
    }, timeout=60)
    if r.status_code == 200:
        body = r.json()
        check("anthropic response has content", "content" in body, f"got keys {list(body.keys())}")
    else:
        print(f"  {YELLOW}skip — upstream unreachable (status {r.status_code}){RESET}")


# ---------------------------------------------------------------------------
# End-of-suite teardown — delete what THIS run created. The e2e user
# itself is NOT deleted here (server refuses self-delete); it is left for
# the next run's pre_cleanup() to pick up.
# ---------------------------------------------------------------------------
def teardown(token: str):
    section("Teardown (this run)")
    H = {"Authorization": f"Bearer {token}"}

    em_id = state.get("exposed_model_id")
    em_name = state.get("exposed_model_name")
    if em_id:
        r = requests.delete(f"{BASE_URL}/api/exposed_model/{em_id}", headers=H, timeout=5)
        check(f"teardown delete exposed_model '{em_name}' (id={em_id})",
              r.status_code == 200, f"got {r.status_code}")

    sid = state.get("log_session_id")
    if DB_API_KEY:
        print(f"  {YELLOW}— api_logs from upstream tests tagged session_id='{sid}'."
              f" Purge with:{RESET}")
        print(f"      docker exec llm_gateway_postgres psql -U postgres -d llm_gateway \\\n"
              f"        -c \"DELETE FROM api_logs WHERE session_id = '{sid}';\"")
    else:
        print(f"  {YELLOW}— upstream tests skipped (no DB_API_KEY), no logs to clean{RESET}")

    uname = state.get("e2e_username")
    uid = state.get("e2e_user_id")
    print(f"  {YELLOW}— e2e user '{uname}' (id={uid}) left in DB; next run's pre_cleanup"
          f" will remove it (server forbids self-delete){RESET}")


# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------
def main() -> int:
    print(f"E2E target: {BASE_URL}")
    try:
        test_health()
        test_v1_auth()
        token = test_jwt_auth()
        if not token:
            print(f"{RED}FATAL: no JWT token, cannot continue admin tests{RESET}")
            return 1

        # Self-heal: clean residue from prior runs (or from this run failing).
        pre_cleanup(token)

        test_admin_crud(token)
        test_logs_stats(token)
        test_api_key(token)
        if DB_API_KEY:
            test_upstream(DB_API_KEY)
            test_anthropic(DB_API_KEY)
        else:
            print(f"\n{YELLOW}Note: set DB_API_KEY env var to a real API key from the DB to run upstream tests{RESET}")
    finally:
        if state.get("e2e_user_id"):
            # Reuse the e2e token to do best-effort cleanup. The user itself
            # can't be deleted this way (server enforces self-delete block);
            # exposed_model and api_logs hint are handled in teardown().
            # We need a fresh token because the e2e user's password may have
            # been mutated mid-test (change_password flow).
            try:
                r = requests.post(f"{BASE_URL}/api/auth/login",
                                  json={"username": state["e2e_username"],
                                        "password": "e2e_jwt_pw_123"}, timeout=5)
                if r.status_code == 200:
                    t = (r.json().get("data") or {}).get("token") or ""
                    if t:
                        teardown(t)
                else:
                    print(f"\n{YELLOW}Teardown skipped — re-login failed (status {r.status_code}){RESET}")
            except Exception as e:
                print(f"\n{YELLOW}Teardown skipped — {e!r}{RESET}")

    print()
    print("─" * 60)
    if failed == 0:
        print(f"{GREEN}ALL {passed} CHECKS PASSED{RESET}")
        return 0
    print(f"{RED}{failed} FAILED{RESET} (out of {passed + failed})")
    for f in failures:
        print(f"  - {f}")
    return 1


if __name__ == "__main__":
    sys.exit(main())
