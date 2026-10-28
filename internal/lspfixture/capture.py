#!/usr/bin/env python3
"""Explicit development-only TLS fixture capture; never installs reference tools."""

import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import queue
import subprocess
import tempfile
import threading
import time


def messages(stream, output):
    try:
        while True:
            headers = {}
            while True:
                line = stream.readline()
                if not line:
                    raise EOFError("oracle closed its protocol stream")
                if line == b"\r\n":
                    break
                key, value = line.decode("ascii").split(":", 1)
                headers[key.lower()] = value.strip()
            length = int(headers["content-length"])
            body = bytearray()
            while len(body) < length:
                part = stream.read(length - len(body))
                if not part:
                    raise EOFError("truncated oracle frame")
                body.extend(part)
            output.put(json.loads(body))
    except Exception as error:
        output.put(error)


def replace_root(value, old, new):
    if isinstance(value, str):
        return value.replace(old, new)
    if isinstance(value, list):
        return [replace_root(item, old, new) for item in value]
    if isinstance(value, dict):
        return {key.replace(old, new): replace_root(item, old, new) for key, item in value.items()}
    return value


class OracleError(Exception):
    def __init__(self, response):
        self.response = response
        super().__init__(str(response))


def capture(args):
    fixture = json.loads(args.fixture.read_text())
    if args.out.exists():
        raise ValueError("output exists; capture to a new path and review the diff")
    for binary, version in [(args.server, "6.0.2"), (args.tsserver, "6.0.3")]:
        metadata = json.loads((binary.parent.parent / "package.json").read_text())
        if metadata["version"] != version:
            raise ValueError(f"unexpected oracle version for {binary}")
    fixture["oracle"] = {
        "server_version": "6.0.2",
        "typescript_version": "6.0.3",
        "server_package_sha256": hashlib.sha256(args.server_package.read_bytes()).hexdigest(),
        "typescript_package_sha256": hashlib.sha256(args.typescript_package.read_bytes()).hexdigest(),
    }
    with tempfile.TemporaryDirectory(prefix="goya-lsp-oracle-") as temp, tempfile.TemporaryFile() as log:
        # Use one physical workspace identity: tsserver realpaths package/map
        # targets (notably /var -> /private/var on macOS).
        root = Path(temp).resolve()
        for name, text in {**fixture["project"]["files"], **fixture["project"].get("declarations", {})}.items():
            rel = PurePosixPath(name.lstrip("/"))
            if ".." in rel.parts or not rel.parts:
                raise ValueError(f"invalid virtual path: {name}")
            target = root / str(rel)
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(text)
        selected_config = fixture["project"].get("config_file")
        if fixture["project"].get("oracle_inferred_project", False):
            if selected_config or any(PurePosixPath(name).name in ("tsconfig.json", "jsconfig.json") for name in fixture["project"]["files"]):
                raise ValueError("inferred oracle fixture cannot select or contain an automatic config")
        if selected_config:
            config_path = root / selected_config.lstrip("/")
            if config_path.name != "tsconfig.json" or not config_path.is_file():
                raise ValueError("oracle capture requires a supplied conventional tsconfig.json; custom host selection is tested directly")
        elif not (root / "tsconfig.json").exists() and not fixture["project"].get("oracle_inferred_project", False):
            (root / "tsconfig.json").write_text(json.dumps({
                "compilerOptions": fixture["project"]["compiler_options"],
                # The embedded host roots every catalog declaration, even
                # when executable/source roots were selected explicitly.
                "files": list(dict.fromkeys(
                    [name.lstrip("/") for name in
                     (fixture["project"].get("entry_points") or fixture["project"]["files"])
                     if name.endswith((".ts", ".tsx", ".mts", ".cts"))]
                    + [name.lstrip("/") for name in fixture["project"]["files"]
                       if name.endswith((".d.ts", ".d.mts", ".d.cts"))]
                    + [name.lstrip("/") for name in fixture["project"].get("declarations", {})]
                )),
            }))
        proc = subprocess.Popen([args.node, str(args.server), "--stdio"], cwd=root,
                                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=log)
        received = queue.Queue()
        threading.Thread(target=messages, args=(proc.stdout, received), daemon=True).start()
        request_id = 0
        published_diagnostics = {}

        def send(message):
            raw = json.dumps(message, ensure_ascii=False).encode()
            proc.stdin.write(f"Content-Length: {len(raw)}\r\n\r\n".encode() + raw)
            proc.stdin.flush()

        recorded_client_requests = []
        recorded_client_methods = []

        def accept_message(message):
            if isinstance(message, Exception):
                raise message
            if message.get("method") == "textDocument/publishDiagnostics":
                published_diagnostics[message["params"]["uri"]] = message["params"]["diagnostics"]
            if "method" in message and "id" in message:
                if message["method"] in recorded_client_methods:
                    recorded_client_requests.append({"method": message["method"], "params": message.get("params")})
                responses = fixture.get("initialization", {}).get("client_responses", {})
                if message["method"] in responses:
                    result = replace_root(responses[message["method"]], "{{ROOT_URI}}", root_uri)
                elif message["method"] == "workspace/configuration":
                    configuration = fixture.get("initialization", {}).get("client_configuration", {})
                    result = [
                        replace_root(configuration.get(
                            replace_root(item.get("scopeUri", ""), root_uri, "{{ROOT_URI}}"), {}
                        ).get(item.get("section", "")), "{{ROOT_URI}}", root_uri)
                        for item in message["params"].get("items", [])
                    ]
                elif message["method"] == "workspace/applyEdit":
                    result = {"applied": False, "failureReason": "capture does not apply client edits"}
                else:
                    result = None
                send({"jsonrpc": "2.0", "id": message["id"], "result": result})

        def request(method, params):
            nonlocal request_id
            request_id += 1
            current = request_id
            send({"jsonrpc": "2.0", "id": current, "method": method, "params": params})
            while True:
                message = received.get(timeout=30)
                accept_message(message)
                if message.get("id") == current and "method" not in message:
                    if "error" in message:
                        raise OracleError(message["error"])
                    return message["result"]

        def pull_from_published_diagnostics(params, settling_ms):
            # TLS 6 uses push diagnostics. Only wrap the captured array; do not
            # remove/reorder tags, related locations, ranges or messages.
            uri = params["textDocument"]["uri"]
            start = time.monotonic()
            stable_after = start + (settling_ms or 2000) / 1000
            deadline = start + 30
            while time.monotonic() < deadline:
                timeout = max(0.001, min(stable_after, deadline) - time.monotonic())
                try:
                    message = received.get(timeout=timeout)
                except queue.Empty:
                    if uri in published_diagnostics and time.monotonic() >= stable_after:
                        return {"kind": "full", "items": published_diagnostics[uri]}
                    stable_after = time.monotonic() + 0.5
                    continue
                accept_message(message)
                if message.get("method") == "textDocument/publishDiagnostics" and message["params"]["uri"] == uri:
                    stable_after = max(stable_after, time.monotonic() + 0.5)
            raise TimeoutError("oracle did not publish settled diagnostics")

        try:
            root_uri = root.as_uri()
            initialization = fixture.get("initialization") or {}
            initialization_options = dict(initialization.get("options") or {})
            initialization_options.update({"disableAutomaticTypingAcquisition": True,
                                           "tsserver": {"path": str(args.tsserver), "useSyntaxServer": "never"}})
            request("initialize", {
                "processId": None, "rootUri": root_uri,
                "capabilities": initialization.get("capabilities", {"textDocument": {"hover": {"contentFormat": ["markdown"]}}}),
                "initializationOptions": initialization_options,
            })
            send({"jsonrpc": "2.0", "method": "initialized", "params": {}})
            results = {}
            for step_index, step in enumerate(fixture["steps"]):
                recorded_client_methods = step.get("record_client_requests", [])
                client_request_start = len(recorded_client_requests)
                params = replace_root(step["params"], "{{ROOT_URI}}", root_uri)
                params = replace_root(params, "{{ROOT_PATH}}", str(root))
                if source := step.get("params_from_call_hierarchy"):
                    source_step = fixture["steps"][source["step"]]
                    item = results[source["step"]][source["index"]]
                    if source_step["method"] == "callHierarchy/incomingCalls":
                        item = item["from"]
                    elif source_step["method"] == "callHierarchy/outgoingCalls":
                        item = item["to"]
                    elif source_step["method"] != "textDocument/prepareCallHierarchy":
                        raise ValueError("unsupported hierarchy source")
                    params = {"item": item}
                if source := step.get("params_from_code_action"):
                    params = results[source["step"]][source["index"]]
                if source := step.get("params_from_code_lens"):
                    params = results[source["step"]][source["index"]]
                if source := step.get("params_from_completion"):
                    params = results[source["step"]]["items"][source["index"]]
                if step.get("notification"):
                    send({"jsonrpc": "2.0", "method": step["method"], "params": params})
                else:
                    oracle_method = step.get("oracle_method")
                    if oracle_method:
                        if oracle_method != "textDocument/publishDiagnostics" or step["method"] != "textDocument/diagnostic":
                            raise ValueError("unsupported oracle method adapter")
                        result = pull_from_published_diagnostics(params, step.get("oracle_wait_ms", 0))
                    else:
                        try:
                            result = request(step["method"], params)
                        except OracleError as error:
                            if "expected_error" not in step:
                                raise
                            step.pop("expected", None)
                            step["expected_error"] = replace_root(error.response, root_uri, "{{ROOT_URI}}")
                            continue
                        if "expected_error" in step:
                            raise ValueError("expected an oracle error but received success")
                    # Some reference commands return before their client request.
                    # Wait for an explicit count rather than recording a timing race.
                    wanted = step.get("oracle_client_request_count", 0)
                    deadline = time.monotonic() + 30
                    while len(recorded_client_requests) - client_request_start < wanted:
                        accept_message(received.get(timeout=max(0.001, deadline - time.monotonic())))
                    if recorded_client_methods:
                        step["expected_client_requests"] = replace_root(recorded_client_requests[client_request_start:], root_uri, "{{ROOT_URI}}")
                    # Standard libraries have an explicit read-only resource
                    # identity in the embedded facade. Preserve every range and
                    # translate only the pinned oracle's library URI prefix.
                    recorded_result = replace_root(result, args.tsserver.parent.resolve().as_uri() + "/lib.", "goya-lib:///lib.")
                    step["expected"] = replace_root(replace_root(recorded_result, root_uri, "{{ROOT_URI}}"), str(root), "{{ROOT_PATH}}")
                    results[step_index] = result
            request("shutdown", None)
            send({"jsonrpc": "2.0", "method": "exit"})
            proc.wait(timeout=10)
            if proc.returncode:
                raise ValueError(f"oracle exited {proc.returncode}")
        finally:
            if proc.poll() is None:
                proc.kill()
                proc.wait()
            proc.stdin.close()
            proc.stdout.close()
    args.out.write_text(json.dumps(fixture, indent=2, ensure_ascii=False) + "\n")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--node", default="node")
    for name in ["server", "tsserver", "server-package", "typescript-package", "fixture", "out"]:
        parser.add_argument("--" + name, type=Path, required=True)
    capture(parser.parse_args())
