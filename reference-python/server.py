#!/usr/bin/env python3
"""Progress server: http://localhost:8765 — index.html + /progress.json."""
import http.server
import json
import os
import sqlite3
import time

HOME = os.path.expanduser("~/PhotosExport")
EXPORT = f"{HOME}/full"
ITEMS = json.load(open(f"{HOME}/items.json"))


def exported():
    try:
        db = sqlite3.connect(f"file:{EXPORT}/.osxphotos_export.db?mode=ro", uri=True, timeout=2)
        rows = db.execute("SELECT uuid, filepath, dest_size FROM export_data").fetchall()
    except sqlite3.Error:
        return {}
    out = {}
    for uuid, rel, size in rows:
        out.setdefault(uuid, []).append((os.path.join(EXPORT, rel), size or 0))
    return out


def results():
    by_path = {}
    d = f"{HOME}/results"
    for name in sorted(os.listdir(d)) if os.path.isdir(d) else []:
        try:
            data = json.load(open(os.path.join(d, name)))
        except (OSError, ValueError):
            continue
        for r in data.get("results", []):
            for p in r.get("paths") or [r["path"]]:
                prev = by_path.get(p)
                if not (prev and prev[0]):  # a success is never overwritten by a later failure
                    by_path[p] = (bool(r.get("success")), r.get("mediaKey") or str(r.get("error", ""))[:300])
    return by_path


def progress():
    exp, res = exported(), results()
    workers = []
    for name in sorted(os.listdir(HOME)):
        if name.startswith("state-w") and name.endswith(".json"):
            try:
                workers.append(json.load(open(os.path.join(HOME, name))))
            except (OSError, ValueError):
                pass
    try:
        pause = json.load(open(f"{HOME}/pause.json"))
    except (OSError, ValueError):
        pause = {}
    active = [w for w in workers if w.get("phase") in ("uploading", "backoff")]
    current = {u for w in active for u in w.get("batch", [])}
    phases = sorted({w.get("phase") for w in workers})
    state = {"phase": ", ".join(phases) + f" · {len(active)} of {len(workers)} workers active" if workers else "idle",
             "batch_index": sum(w.get("batch_index", 0) - (w.get("phase") != "finished") for w in workers if "batches" in w),
             "batches": sum(w.get("batches", 0) for w in workers), "attempt": "parallel"}
    if pause.get("until", 0) > time.time():
        state.update(phase="backoff", until=pause["until"])
    if pause.get("last_error"):
        state["last_error"] = pause["last_error"]
    stopped = [w for w in workers if w.get("phase") == "stopped"]
    if stopped:
        state["reason"] = f"{len(stopped)} worker(s) stopped: {stopped[0].get('reason')}"
    items, counts = [], {}
    bytes_total = bytes_done = 0
    for it in ITEMS:
        files = []
        for path, size in exp.get(it["u"], []):
            bytes_total += size
            r = res.get(path)
            st = "done" if r and r[0] else "failed" if r else "pending"
            if st == "done":
                bytes_done += size
            files.append([os.path.basename(path), st, r[1] if r else ""])
        sts = {f[1] for f in files}
        if not files:
            s = "not exported"
        elif it["u"] in current:
            s = "uploading"
        elif sts == {"done"}:
            s = "done"
        elif "failed" in sts:
            s = "failed"
        else:
            s = "exported"
        counts[s] = counts.get(s, 0) + 1
        items.append({**it, "s": s, "f": files})
    return {"now": time.time(), "state": state, "counts": counts, "total": len(ITEMS),
            "bytes_total": bytes_total, "bytes_done": bytes_done, "items": items}


class H(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *a, **kw):
        super().__init__(*a, directory=HOME, **kw)

    def do_GET(self):
        if self.path.startswith("/progress.json"):
            body = json.dumps(progress()).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        elif self.path in ("/", "/index.html"):
            super().do_GET()
        else:
            self.send_error(404)

    def log_message(self, *a):
        pass


if __name__ == "__main__":
    http.server.ThreadingHTTPServer(("0.0.0.0", 8765), H).serve_forever()
