#!/usr/bin/env python3
"""Write items.json: one row per visible library item, for the progress page.

Reads a copy of the Photos library database (the live one is locked by Photos).
Fields: u=uuid, n=original filename, d=date taken (local), v=video, l=Live Photo,
e=edited.
"""
import json
import os
import shutil
import sqlite3
import subprocess
import tempfile

LIBRARY = os.path.expanduser("~/Pictures/Photos Library.photoslibrary")
OUT = os.path.expanduser("~/PhotosExport/items.json")
SQL = """
SELECT z.ZUUID u, a.ZORIGINALFILENAME n,
       datetime(z.ZDATECREATED+978307200+IFNULL(a.ZTIMEZONEOFFSET,0),'unixepoch') d,
       z.ZKIND=1 v, z.ZKINDSUBTYPE=2 l, z.ZADJUSTMENTSSTATE>0 e
FROM ZASSET z JOIN ZADDITIONALASSETATTRIBUTES a ON a.ZASSET=z.Z_PK
WHERE z.ZTRASHEDSTATE=0 AND z.ZHIDDEN=0 AND z.ZVISIBILITYSTATE=0
ORDER BY z.ZDATECREATED;
"""

with tempfile.TemporaryDirectory() as tmp:
    for suffix in ("", "-wal", "-shm"):
        src = f"{LIBRARY}/database/Photos.sqlite{suffix}"
        if os.path.exists(src):
            shutil.copy(src, tmp)
    rows = subprocess.run(["/usr/bin/sqlite3", "-json", f"{tmp}/Photos.sqlite", SQL],
                          capture_output=True, text=True, check=True).stdout

os.makedirs(os.path.dirname(OUT), exist_ok=True)
with open(OUT, "w") as f:
    f.write(rows)
print(f"{len(json.loads(rows))} items -> {OUT}")
