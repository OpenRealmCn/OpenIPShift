#!/usr/bin/env python3
"""Smoke local images only; ephemeral state/token, loopback port, no production changes."""
import json
import os
import secrets
import subprocess
import time
import urllib.error
import urllib.request

backend_image = os.environ.get("BACKEND_IMAGE", "openipshift-backend:security-upgrade")
frontend_image = os.environ.get("FRONTEND_IMAGE", "openipshift-frontend:security-upgrade")
prefix = "openipshift-smoke-" + secrets.token_hex(4)
token = secrets.token_hex(32)


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()


def request(path, method="GET", body=None, auth=token, key=None):
    headers = {}
    if auth is not None:
        headers["Authorization"] = "Bearer " + auth
    if key is not None:
        headers["Idempotency-Key"] = key
    data = None
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    try:
        response = urllib.request.urlopen(req, timeout=10)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        return response.status, response.read(), response.headers


def expect(path, code=200, **kwargs):
    status, body, headers = request(path, **kwargs)
    assert status == code, f"{kwargs.get('method', 'GET')} {path}: {status}, expected {code}"
    if path.startswith("/api/"):
        assert headers.get("Cache-Control") == "no-store"
    return json.loads(body) if body and body[:1] in (b"{", b"[") else body


try:
    docker("network", "create", prefix)
    docker("run", "-d", "--name", prefix + "-backend", "--network", prefix,
           "--network-alias", "backend", "-e", "ROTATOR_API_TOKEN=" + token, backend_image)
    docker("run", "-d", "--name", prefix + "-frontend", "--network", prefix,
           "-p", "127.0.0.1::3000", "-e", "API_INTERNAL_URL=http://backend:8080", frontend_image)
    port = docker("port", prefix + "-frontend", "3000/tcp").rsplit(":", 1)[1]
    base = "http://127.0.0.1:" + port
    for attempt in range(60):
        try:
            if request("/", auth=None)[0] == 200:
                break
        except (urllib.error.URLError, TimeoutError, ConnectionResetError):
            pass
        time.sleep(1)
    else:
        raise AssertionError("frontend readiness timed out")
    page = expect("/", auth=None)
    assert "访问 Token".encode() in page and token.encode() not in page
    for path in ("/api/state", "/api/nodes", "/api/tasks/missing"):
        expect(path, 401, auth=None)
        expect(path, 401, auth="wrong")
    state = expect("/api/state")
    assert state["nodes"] == [] and not state["tasks"]
    print("PASS page 200 / login screen / token gate / zero-node initial state")
    node = dict(id="smoke", name="Smoke node", region="ap-northeast-1", instance="mock",
                staticIP="192.0.2.10", dnsName="smoke.example.test", proxyTarget="smoke.example.test:443")
    expect("/api/nodes", 201, method="POST", body=node)
    assert expect("/api/nodes")[0]["id"] == "smoke"
    node["name"] = "Edited node"
    expect("/api/nodes/smoke", method="PUT", body=node)
    assert expect("/api/nodes")[0]["name"] == "Edited node"
    expect("/api/nodes/smoke", method="DELETE")
    assert expect("/api/nodes") == []
    print("PASS node create/list/update/delete through same-origin runtime proxy")
    expect("/api/nodes", 201, method="POST", body=node)
    expect("/api/rotations", 409, method="POST", body={"nodeId": "smoke"})
    first = expect("/api/rotations", 202, method="POST", body={"nodeId": "smoke"}, key="smoke-key")
    second = expect("/api/rotations", 202, method="POST", body={"nodeId": "smoke"}, key="smoke-key")
    assert first["id"] == second["id"]
    expect("/api/tasks/" + first["id"])
    print("PASS Authorization / JSON body / Idempotency-Key forwarding / no-store responses")
    for component in ("backend", "frontend"):
        assert docker("exec", prefix + "-" + component, "id", "-u") == "10001"
    # Tokens must never be baked into or reflected by the frontend's static assets.
    result = subprocess.run(["docker", "exec", prefix + "-frontend", "grep", "-R", "-l", token,
                             "/app/.next/static"], capture_output=True)
    assert result.returncode == 1, "token found in frontend assets or asset check failed"
    print("PASS both containers UID 10001 / token absent from static assets")
finally:
    for component in ("frontend", "backend"):
        subprocess.run(["docker", "rm", "-f", "-v", prefix + "-" + component],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.run(["docker", "network", "rm", prefix], stdout=subprocess.DEVNULL,
                   stderr=subprocess.DEVNULL)
