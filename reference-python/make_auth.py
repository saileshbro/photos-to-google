#!/usr/bin/env python3
"""EmbeddedSetup oauth_token (from the clipboard) -> gpmc/gotohp auth_data file.

Same wire format as PhotosBackup's TokenExchange.swift (ported from gotohp).
The token is read from the clipboard, never printed, and the clipboard is
cleared afterwards. auth_data is written to ~/.config/gpmc/auth_data (0600).
"""
import os
import secrets
import subprocess
import sys
import urllib.parse
import urllib.request

AUTH_URL = "https://android.clients.google.com/auth"
ANDROID_SIG = "38918a453d07199354f8b19af05ec6562ced5788"
PHOTOS_SIG = "24bb24c05e47e0aefa68a58a766179d9b613a600"
OUT = os.path.expanduser("~/.config/gpmc/auth_data")


def post(pairs):
    req = urllib.request.Request(
        AUTH_URL,
        data=urllib.parse.urlencode(pairs).encode(),
        headers={
            "Content-Type": "application/x-www-form-urlencoded",
            "User-Agent": "GoogleAuth/1.4",
            "Accept-Encoding": "identity",
        },
    )
    try:
        body = urllib.request.urlopen(req, timeout=60).read().decode()
    except urllib.error.HTTPError as e:
        body = e.read().decode()
    return dict(line.split("=", 1) for line in body.splitlines() if "=" in line)


token = subprocess.run(["pbpaste"], capture_output=True, text=True).stdout.strip()
if not token.startswith("oauth2_4/"):
    sys.exit("Clipboard does not hold an oauth_token (expected it to start with 'oauth2_4/').")

android_id = secrets.token_hex(8)
r1 = post([
    ("accountType", "HOSTED_OR_GOOGLE"), ("Email", "oauth-token@example.com"),
    ("has_permission", "1"), ("add_account", "1"), ("ACCESS_TOKEN", "1"),
    ("Token", token), ("service", "ac2dm"), ("source", "android"),
    ("androidId", android_id), ("device_country", "us"), ("operatorCountry", "us"),
    ("lang", "en"), ("sdk_version", "17"), ("google_play_services_version", "240913000"),
    ("client_sig", ANDROID_SIG), ("callerSig", ANDROID_SIG), ("droidguard_results", "dummy123"),
])
subprocess.run(["pbcopy"], input="", text=True)  # clear the clipboard either way
if "Token" not in r1:
    sys.exit(f"Master-token exchange failed: {r1.get('Error', 'no Token in response')}. "
             "The oauth_token is single-use and short-lived; sign in again and retry.")
email = r1.get("Email", "unknown")

auth_pairs = [
    ("androidId", android_id), ("app", "com.google.android.apps.photos"),
    ("callerPkg", "com.google.android.apps.photos"), ("callerSig", PHOTOS_SIG),
    ("client_sig", PHOTOS_SIG), ("device_country", "us"), ("Email", email),
    ("google_play_services_version", "240913000"), ("lang", "en_US"),
    ("oauth2_foreground", "1"), ("operatorCountry", "us"), ("sdk_version", "33"),
    ("service", "oauth2:openid https://www.googleapis.com/auth/mobileapps.native "
                "https://www.googleapis.com/auth/photos.native"),
    ("source", "android"), ("Token", r1["Token"]),
]
r2 = post(auth_pairs)
if "Auth" not in r2:
    sys.exit(f"Photos credential check failed: {r2.get('Error', 'no Auth in response')}.")

os.makedirs(os.path.dirname(OUT), exist_ok=True)
fd = os.open(OUT, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as f:
    f.write(urllib.parse.urlencode(auth_pairs))
print(f"OK: credentials for {email} saved to {OUT}")
