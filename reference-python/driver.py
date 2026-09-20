#!/usr/bin/env python3
"""Upload an osxphotos export to Google Photos in batches with gotohp.

  driver.py plan                 write plan.json: pending items split into batches
  driver.py worker K N           upload batches j where j % N == K (run N of these at once)

Batches are groups of library items (all files of an item go together, so a
Live Photo's still and video are paired). Each batch's result is saved under
results/, so a new plan skips what already succeeded. A rate limit or a run of
Google 500/503s pauses every worker through pause.json (60 s doubling to
10 min). A worker stops after 3 of its batches in a row fail completely.
"""
import json
import os
import sqlite3
import subprocess
import sys
import time

HOME = os.path.expanduser("~/PhotosExport")
EXPORT = f"{HOME}/full"
RESULTS = f"{HOME}/results"
PLAN = f"{HOME}/plan.json"
PAUSE = f"{HOME}/pause.json"
GOTOHP = [f"{HOME}/bin/gotohp-cli-macos-universal", "-c", f"{HOME}/gotohp.config"]
BATCH = 40


def write_json(path, obj):
    tmp = f"{path}.{os.getpid()}.tmp"
    with open(tmp, "w") as f:
        json.dump(obj, f)
    os.replace(tmp, path)


def read_json(path, default):
    try:
        with open(path) as f:
            return json.load(f)
    except (OSError, ValueError):
        return default


def exported():
    """uuid -> [absolute file paths] from osxphotos' export database."""
    db = sqlite3.connect(f"file:{EXPORT}/.osxphotos_export.db?mode=ro", uri=True)
    out = {}
    for uuid, rel in db.execute("SELECT uuid, filepath FROM export_data ORDER BY id"):
        p = os.path.join(EXPORT, rel)
        if os.path.exists(p):
            out.setdefault(uuid, []).append(p)
    return out


def succeeded():
    done = set()
    for name in os.listdir(RESULTS):
        for r in read_json(os.path.join(RESULTS, name), {}).get("results", []):
            if r.get("success"):
                done.update(r.get("paths") or [r["path"]])
    return done


def plan():
    files_by_uuid, done = exported(), succeeded()
    batches, current = [], []
    for uuid, files in files_by_uuid.items():
        todo = [f for f in files if f not in done]
        if todo:
            current.append({"uuid": uuid, "files": todo})
            if len(current) == BATCH:
                batches.append(current)
                current = []
    if current:
        batches.append(current)
    write_json(PLAN, {"created": time.time(), "batches": batches})
    print(f"{len(batches)} batches, {sum(len(b) for b in batches)} items")


def run_batch(files):
    cmd = GOTOHP + ["upload", *files, "--pair-live-photos", "--upload-incomplete-live-photos",
                    "--update-existing-photos-to-live", "--threads", "2", "--no-tui"]
    p = subprocess.run(cmd, capture_output=True, text=True)
    out = p.stdout
    try:
        result = json.loads(out[out.index("{"):out.rindex("}") + 1])
    except ValueError:
        result = {"results": [{"path": f, "success": False,
                               "error": (p.stderr or out)[-500:]} for f in files]}
    result["returncode"] = p.returncode
    result["stderr_tail"] = p.stderr[-2000:]
    return result


def wait_for_pause(state_path, state):
    while (until := read_json(PAUSE, {}).get("until", 0)) > time.time():
        write_json(state_path, {**state, "phase": "backoff", "until": until, "updated": time.time()})
        time.sleep(min(10, until - time.time() + 0.1))


def worker(k, n):
    state_path = f"{HOME}/state-w{k}.json"
    batches = read_json(PLAN, {"batches": []})["batches"]
    mine = [(j, b) for j, b in enumerate(batches) if j % n == k]
    dead = 0
    for pos, (j, batch) in enumerate(mine, 1):
        state = {"worker": k, "batch": [it["uuid"] for it in batch], "batch_index": pos, "batches": len(mine)}
        wait_for_pause(state_path, state)
        write_json(state_path, {**state, "phase": "uploading", "updated": time.time()})
        result = run_batch([f for it in batch for f in it["files"]])
        write_json(f"{RESULTS}/batch-{time.strftime('%Y%m%d-%H%M%S')}-w{k}-{j:04d}.json", result)

        res = result.get("results", [])
        failed = [r for r in res if not r.get("success")]
        errors = " ".join(str(r.get("error", "")) for r in failed) + result["stderr_tail"]
        rate_limited = any(s in errors.lower() for s in ("429", "rate limit", "ratelimit", "rate_limit", "resource_exhausted", "too many requests"))
        overloaded = sum(1 for r in failed if "status 500" in str(r.get("error")) or "status 503" in str(r.get("error"))) >= 3
        all_failed = bool(res) and len(failed) == len(res)
        dead = dead + 1 if all_failed else 0
        if dead >= 3:
            write_json(state_path, {**state, "phase": "stopped", "updated": time.time(),
                                    "reason": "3 batches in a row failed completely",
                                    "last_error": errors[:500]})
            sys.exit(1)
        if rate_limited or overloaded or all_failed:
            pause = read_json(PAUSE, {})
            backoff = min(max(pause.get("backoff", 0) * 2, 60), 600)
            write_json(PAUSE, {"until": time.time() + backoff, "backoff": backoff, "last_error": errors[:500]})
        elif not failed and read_json(PAUSE, {}).get("backoff"):
            write_json(PAUSE, {"until": 0, "backoff": 0})
    write_json(state_path, {"worker": k, "phase": "finished", "batch_index": len(mine),
                            "batches": len(mine), "updated": time.time()})


if __name__ == "__main__":
    if sys.argv[1] == "plan":
        plan()
    else:
        worker(int(sys.argv[2]), int(sys.argv[3]))
