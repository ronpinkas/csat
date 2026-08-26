#!/usr/bin/env python3
"""Repair CSAT responses whose subject was the survey ID instead of the caller's phone.

Background
----------
Until AI_AR_Integration commit d8eb862 (2026-08-26), /VC_postprocess minted CSAT
tokens with subject "14" (the survey ID) and no &set= parameter. Those responses
therefore landed under the DEFAULT question set with subject '14' instead of under
survey #14 with the caller's phone number.

This script does two things for every response row with subject '14':
  1. Recovers the caller's phone number from the VC_postprocess log:
       a. EXACT: the "SMS sent" log line contains the full SMS body, including the
          survey URL. The token in that URL is decrypted with CSAT_CRYPTO_SECRET,
          yielding "14|<subject_time>|<lang>". subject_time matches the response
          row exactly, and the same log line carries the destination phone.
       b. FALLBACK: for older log lines that lack the body (full-body logging
          began 2026-08-25 23:17 UTC), it matches the log line's timestamp to the
          response's subject_time within a +/- window, accepting only unambiguous
          matches (exactly one candidate phone in the window).
  2. Moves the row to survey #14 (UPDATE responses SET definition_id = 14) and,
     when a phone was recovered, sets subject to that phone.

The used_tokens table is deliberately left untouched: those tokens still decrypt
to subject '14', so the existing ('14', subject_time) rows are exactly what keeps
an already-used link from being re-submitted.

DRY-RUN BY DEFAULT. Nothing is written unless --apply is given.
With --apply, a timestamped copy of the DB file is made first.

Usage
-----
  python3 recover-vc-csat.py \
      --db /path/to/csat.db \
      --log /path/to/VC_postprocess.log [--log more.log ...] \
      --set 14 \
      --log-utc-offset -7 \
      [--secret-env CSAT_CRYPTO_SECRET] \
      [--window 180] [--move-unmatched] [--apply]

  --log accepts files or directories (directories are scanned for *.log*).
  --log-utc-offset is the log host's UTC offset in hours (PDT = -7); it only
  affects the FALLBACK timestamp matching, never the exact token matching.
  CSAT_CRYPTO_SECRET must be in the environment (or use --secret, less safe).

Run it on the CSAT host (needs the SQLite DB file); copy the VC_postprocess
log(s) over, or run it wherever both are reachable.
"""

import argparse
import base64
import hashlib
import json
import os
import re
import shutil
import sqlite3
import sys
import time
from datetime import datetime, timedelta, timezone

try:
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM
    HAVE_CRYPTO = True
except ImportError:
    HAVE_CRYPTO = False

BAD_SUBJECT = "14"

# "2026-08-25 16:20:11,123 - INFO - VC_POSTPROCESS | CSAT_SMS | ... | REQUEST_PAYLOAD | {...} | RESPONSE_JSON | {...}"
LINE_RE = re.compile(
    r"^(?P<ts>\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\S*\s.*?CSAT_SMS.*?"
    r"REQUEST_PAYLOAD \| (?P<payload>\{.*?\}) \| RESPONSE_JSON \|"
)
TOKEN_RE = re.compile(r"[?&]t=([A-Za-z0-9_\-]+)")


def decrypt_token(token, key):
    """Return (subject, subject_time, lang) or None."""
    try:
        raw = base64.urlsafe_b64decode(token + "=" * (-len(token) % 4))
        payload = AESGCM(key).decrypt(raw[:12], raw[12:], None).decode("utf-8")
        parts = payload.split("|")
        return parts[0], int(parts[1]), (parts[2] if len(parts) > 2 else "en")
    except Exception:
        return None


def iter_log_lines(paths):
    files = []
    for p in paths:
        if os.path.isdir(p):
            files += sorted(
                os.path.join(p, f) for f in os.listdir(p) if ".log" in f
            )
        else:
            files.append(p)
    for f in files:
        with open(f, "r", encoding="utf-8", errors="replace") as fh:
            for line in fh:
                yield line


def parse_logs(paths, key, utc_offset_hours):
    """Return (exact: {subject_time: phone}, fallback: [(epoch_utc, phone)])."""
    exact, fallback = {}, []
    tz = timezone(timedelta(hours=utc_offset_hours))
    for line in iter_log_lines(paths):
        m = LINE_RE.match(line)
        if not m:
            continue
        try:
            payload = json.loads(m.group("payload"))
        except ValueError:
            continue
        phone = payload.get("destination") or ""
        if not phone:
            continue
        tok = TOKEN_RE.search(payload.get("body") or "")
        decoded = decrypt_token(tok.group(1), key) if (tok and key) else None
        if decoded and decoded[0] == BAD_SUBJECT:
            st = decoded[1]
            if st in exact and exact[st] != phone:
                print(f"WARNING: two phones for subject_time={st}: "
                      f"{exact[st]} vs {phone} — keeping first", file=sys.stderr)
            else:
                exact.setdefault(st, phone)
        else:
            ts = datetime.strptime(m.group("ts"), "%Y-%m-%d %H:%M:%S")
            fallback.append((ts.replace(tzinfo=tz).timestamp(), phone))
    return exact, fallback


def match_fallback(subject_time, fallback, window):
    phones = {p for (ts, p) in fallback if abs(ts - subject_time) <= window}
    return phones.pop() if len(phones) == 1 else None


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--db", required=True, help="path to the CSAT SQLite database")
    ap.add_argument("--log", action="append", default=[],
                    help="VC_postprocess log file or directory (repeatable)")
    ap.add_argument("--set", type=int, default=14, dest="set_id",
                    help="target survey definition id (default 14)")
    ap.add_argument("--secret-env", default="CSAT_CRYPTO_SECRET",
                    help="env var holding the crypto secret")
    ap.add_argument("--secret", default=None, help="crypto secret (prefer --secret-env)")
    ap.add_argument("--log-utc-offset", type=float, default=0.0,
                    help="log host UTC offset in hours, e.g. -7 for PDT "
                         "(fallback matching only)")
    ap.add_argument("--window", type=int, default=180,
                    help="fallback matching window in seconds (default 180)")
    ap.add_argument("--move-unmatched", action="store_true",
                    help="move rows to the target survey even when no phone was recovered")
    ap.add_argument("--apply", action="store_true",
                    help="actually write changes (default is dry-run)")
    args = ap.parse_args()

    secret = args.secret or os.environ.get(args.secret_env, "")
    key = hashlib.sha256(secret.encode()).digest() if (secret and HAVE_CRYPTO) else None
    if not key:
        print("NOTE: no crypto secret and/or 'cryptography' package — "
              "exact token matching disabled, fallback only.", file=sys.stderr)

    exact, fallback = parse_logs(args.log, key, args.log_utc_offset) \
        if args.log else ({}, [])
    print(f"log: {len(exact)} exact token matches, "
          f"{len(fallback)} timestamp-only candidates")

    db = sqlite3.connect(args.db)
    db.execute("PRAGMA foreign_keys = ON")

    # sanity: target definition must exist
    row = db.execute("SELECT id, name FROM survey_definitions WHERE id = ?",
                     (args.set_id,)).fetchone()
    if not row:
        sys.exit(f"ERROR: survey definition {args.set_id} not found in this DB")
    print(f"target survey: #{row[0]} {row[1]!r}")

    rows = db.execute(
        "SELECT id, subject_time, lang, submitted_at, definition_id "
        "FROM responses WHERE subject = ? ORDER BY id", (BAD_SUBJECT,)).fetchall()
    print(f"found {len(rows)} responses with subject = '{BAD_SUBJECT}'\n")

    plan = []  # (response_id, phone_or_None, method)
    for rid, st, lang, _sub, def_id in rows:
        phone, method = exact.get(st), "exact"
        if not phone:
            phone = match_fallback(st, fallback, args.window)
            method = "fallback" if phone else "-"
        plan.append((rid, st, def_id, phone, method))
        when = datetime.fromtimestamp(st, tz=timezone.utc).strftime("%m-%d %H:%M:%SZ")
        print(f"  id={rid:<6} subject_time={when}  def {def_id}->{args.set_id}  "
              f"phone={phone or 'NOT RECOVERED':<15} ({method})")

    matched = [p for p in plan if p[3]]
    unmatched = [p for p in plan if not p[3]]
    print(f"\nsummary: {len(matched)} recoverable, {len(unmatched)} not recovered"
          f"{' (will move anyway)' if args.move_unmatched and unmatched else ''}")

    if not args.apply:
        print("\nDRY RUN — nothing written. Re-run with --apply to execute.")
        return

    backup = f"{args.db}.bak-{time.strftime('%Y%m%d-%H%M%S')}"
    shutil.copy2(args.db, backup)
    print(f"\nbackup written: {backup}")

    with db:  # one transaction
        for rid, _st, _d, phone, _m in matched:
            db.execute("UPDATE responses SET subject = ?, definition_id = ? "
                       "WHERE id = ? AND subject = ?",
                       (phone, args.set_id, rid, BAD_SUBJECT))
        if args.move_unmatched:
            for rid, _st, _d, _p, _m in unmatched:
                db.execute("UPDATE responses SET definition_id = ? "
                           "WHERE id = ? AND subject = ?",
                           (args.set_id, rid, BAD_SUBJECT))
    n = len(matched) + (len(unmatched) if args.move_unmatched else 0)
    print(f"applied: {n} rows updated. (used_tokens intentionally untouched.)")


if __name__ == "__main__":
    main()
