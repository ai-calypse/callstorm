"""Build a clean checkout, SIGKILL a busy worker, and verify durable evidence.

Requires Docker, kind, kubectl, Go, and Python 3. Creates a separate kind cluster;
never changes the caller's current kubectl context or existing Callstorm cluster.
"""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import socket
import subprocess
import tarfile
import time
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def run(*args, data=None, timeout=300):
    print("+ " + " ".join(str(a) for a in args), flush=True)
    result = subprocess.run(args, cwd=ROOT, input=data, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                            timeout=timeout)
    if result.returncode:
        raise RuntimeError(result.stdout + result.stderr)
    return result.stdout


def eventually(fn, timeout=180):
    end = time.monotonic() + timeout
    last = None
    while time.monotonic() < end:
        try:
            value = fn()
            if value:
                return value
        except (OSError, ValueError) as err:
            last = err
        time.sleep(2)
    raise TimeoutError(f"condition not met: {last}")


def fetch(port, path):
    with urllib.request.urlopen(f"http://127.0.0.1:{port}{path}", timeout=5) as r:
        return r.read()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cluster", default="callstorm-verify")
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--require-clean", action="store_true")
    args = parser.parse_args()
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    if args.require_clean and run("git", "status", "--porcelain").strip():
        raise RuntimeError("verification requires a clean checkout")
    if args.cluster in run("kind", "get", "clusters").split():
        raise RuntimeError("verification cluster already exists; choose another name")
    image = "callstorm:verify-" + str(int(time.time()))
    evidence = {"image": image, "cluster": args.cluster,
                "commit": run("git", "rev-parse", "HEAD").strip()}
    run("go", "test", "./...", timeout=600)
    run("go", "vet", "./...", timeout=300)
    run("docker", "build", "-t", image, ".", timeout=1200)
    context = "kind-" + args.cluster
    # kind normally changes current-context; give it a dedicated kubeconfig.
    kubeconfig = out / "kubeconfig"
    run("kind", "create", "cluster", "--name", args.cluster,
        "--kubeconfig", str(kubeconfig), "--wait", "120s", timeout=240)
    run("kind", "load", "docker-image", image, "--name", args.cluster, timeout=300)

    def k(*parts, data=None, timeout=180):
        return run("kubectl", "--kubeconfig", str(kubeconfig), "--context", context,
                   "--request-timeout=30s", *parts, data=data, timeout=timeout)

    forwards = []

    def forward(service, remote):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        log = open(out / f"{service}-{port}.log", "w")
        proc = subprocess.Popen(
            ["kubectl", "--kubeconfig", str(kubeconfig), "--context", context,
             "-n", "callstorm", "port-forward", "svc/" + service,
             f"{port}:{remote}", "--address=127.0.0.1"],
            stdout=log, stderr=log,
            creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
        forwards.append((proc, log))
        eventually(lambda: socket_ready(port), 30)
        return port

    try:
        k("apply", "-f", "deploy/k8s/00-namespace.yaml")
        secret = {"apiVersion": "v1", "kind": "Secret",
                  "metadata": {"name": "callstorm-credentials", "namespace": "callstorm"},
                  "stringData": {"deepgram-api-key": "reference-fixture-no-provider-calls"}}
        k("apply", "-f", "-", data=json.dumps(secret))
        manifest = run("kubectl", "kustomize", "deploy").replace("callstorm:dev", image)
        k("apply", "--dry-run=server", "-f", "-", data=manifest)
        k("apply", "-f", "-", data=manifest)
        for name in ["redpanda", "refagent", "callstorm-reports", "callstorm-worker", "prometheus", "grafana"]:
            k("-n", "callstorm", "rollout", "status", "deployment/" + name,
              "--timeout=180s", timeout=200)
        prom = forward("prometheus", 9090)
        grafana = forward("grafana", 3000)
        reports = forward("callstorm-reports", 8090)

        def targets():
            values = json.loads(fetch(prom, "/api/v1/targets"))["data"]["activeTargets"]
            workers = [v for v in values if v["labels"].get("job") == "callstorm-workers"]
            return workers if len(workers) == 2 and all(v["health"] == "up" for v in workers) else None

        evidence["scrape_targets"] = eventually(targets)
        evidence["grafana"] = json.loads(fetch(grafana, "/api/health"))
        dashboard = json.loads(fetch(grafana, "/api/dashboards/uid/callstorm-live"))
        assert dashboard["meta"]["canEdit"] is False, "anonymous Grafana user can edit"
        job_name = "callstorm-recovery"
        job = (ROOT / "deploy/k8s/50-dispatch-job.yaml").read_text()
        job = job.replace("callstorm:dev", image).replace("name: callstorm-dispatch", "name: " + job_name)
        job = job.replace("/profiles/k8s-demo.json", "/profiles/k8s-recovery.json")
        k("apply", "-f", "-", data=job)

        def busy():
            query = urllib.parse.quote("callstorm_active_calls > 0")
            values = json.loads(fetch(prom, "/api/v1/query?query=" + query))["data"]["result"]
            return values[0] if values else None

        active = eventually(busy, 180)
        pod = active["metric"]["pod"]
        details = json.loads(k("-n", "callstorm", "get", "pod", pod, "-o", "json"))
        node = details["spec"]["nodeName"]
        container_id = details["status"]["containerStatuses"][0]["containerID"].split("://", 1)[1]
        evidence["killed_worker"] = {"pod": pod, "container": container_id,
                                     "active_calls": active["value"], "signal": "SIGKILL"}
        # Kill the actual process in the kind node's runtime; force-deleting a
        # Pod object alone would not prove the old process has stopped.
        run("docker", "exec", node, "ctr", "-n", "k8s.io", "tasks", "kill",
            "--signal", "SIGKILL", container_id)
        print("Killed busy worker " + pod, flush=True)

        def finished():
            status = json.loads(k("-n", "callstorm", "get", "job", job_name, "-o", "json"))["status"]
            if status.get("failed", 0):
                raise RuntimeError(k("-n", "callstorm", "logs", "job/" + job_name))
            return status.get("succeeded", 0) == 1

        eventually(finished, 600)
        (out / "dispatcher.log").write_text(k("-n", "callstorm", "logs", "job/" + job_name))
        runs = json.loads(fetch(reports, "/api/runs.json"))
        selected = [r for r in runs if r["profile"] == "k8s-recovery"]
        assert len(selected) == 1, selected
        run_id = selected[0]["id"]
        report_bytes = fetch(reports, "/api/runs/" + run_id + ".json")
        report = json.loads(report_bytes)
        integrity = report["integrity"]["dispatch"]
        assert integrity["dispatched"] == integrity["received"] == 48, integrity
        assert integrity["missing"] == 0, integrity
        evidence["dispatch"] = integrity
        evidence["run_id"] = run_id
        archive_path = "/api/runs/" + run_id + "/archive.tar.gz"
        archive = fetch(reports, archive_path)
        (out / (run_id + ".tar.gz")).write_bytes(archive)
        with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as tf:
            members = tf.getnames()
            assert run_id + "-calls.jsonl" in members, members
            assert tf.extractfile(run_id + ".json").read() == report_bytes
        evidence["archive_members"] = members
        # Delete the completed dispatcher and replace the report-serving pod.
        # The PVC must retain exactly the same source evidence through both.
        k("-n", "callstorm", "delete", "job", job_name)
        k("-n", "callstorm", "rollout", "restart", "deployment/callstorm-reports")
        k("-n", "callstorm", "rollout", "status", "deployment/callstorm-reports", "--timeout=120s")
        reports = forward("callstorm-reports", 8090)
        assert fetch(reports, "/api/runs/" + run_id + ".json") == report_bytes
        assert fetch(reports, archive_path) == archive
        evidence["retrieval_after_job_deletion_and_server_restart"] = True
        evidence["archive_sha256"] = hashlib.sha256(archive).hexdigest()
        evidence["worker_pods_after_recovery"] = json.loads(k("-n", "callstorm", "get", "pods", "-l", "app=callstorm-worker", "-o", "json"))
        (out / "evidence.json").write_text(json.dumps(evidence, indent=2))
        print("PASS: 48/48 distinct results, zero missing, archive survived Job deletion and server restart.", flush=True)
        print("Evidence: " + str(out), flush=True)
    finally:
        for proc, log in forwards:
            proc.terminate()
            proc.wait(timeout=10)
            log.close()


def socket_ready(port):
    with socket.create_connection(("127.0.0.1", port), timeout=1):
        return True


if __name__ == "__main__":
    main()
