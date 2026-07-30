#!/usr/bin/env python3
"""Seed the local dev database with 1-star responses so the dashboard's
"Rated 1" section has something to show.

Local-only helper. It writes directly to csat-local.db (which is gitignored) and
never touches a deployed database.

    python3 scripts/seed-lowratings.py            # 5 rows into the newest set
    python3 scripts/seed-lowratings.py --set 1    # pick a question set
    python3 scripts/seed-lowratings.py --clean    # remove rows this script made

Stop the server first if it's running, or just re-load the dashboard after.
"""

import argparse
import json
import os
import sqlite3
import sys
import time

DB = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "csat-local.db")
MARKER = "seed-lowrating-"  # subject prefix, so --clean can find its own rows

# (comment text, hours ago). An empty comment exercises the "(no comment)" path.
SAMPLES = [
    ("Waited 40 minutes on hold and then got disconnected. Nobody called back.", 2),
    ("", 6),
    ("The rep was rude and kept interrupting me. Still no resolution.", 20),
    ("Third time calling about the same issue. Nothing has been fixed.", 30),
    ("", 50),
]


def numeric_question(defn):
    """The question the dashboard treats as the rating: first stars, else first scale/nps."""
    for q in defn["questions"]:
        if q["type"] == "stars":
            return q
    for q in defn["questions"]:
        if q["type"] in ("scale", "nps"):
            return q
    return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--set", type=int, help="survey definition id (default: highest)")
    ap.add_argument("--clean", action="store_true", help="delete rows this script created")
    args = ap.parse_args()

    if not os.path.exists(DB):
        sys.exit("no %s — start the server once (make run) to create it" % DB)
    con = sqlite3.connect(DB)
    con.execute("PRAGMA foreign_keys = ON")

    if args.clean:
        ids = [r[0] for r in con.execute(
            "SELECT id FROM responses WHERE subject LIKE ?", (MARKER + "%",))]
        con.executemany("DELETE FROM answers WHERE response_id = ?", [(i,) for i in ids])
        con.executemany("DELETE FROM responses WHERE id = ?", [(i,) for i in ids])
        con.commit()
        print("removed %d seeded response(s)" % len(ids))
        return

    sets = con.execute("SELECT id, name, json FROM survey_definitions ORDER BY id").fetchall()
    if not sets:
        sys.exit("no question sets in the db yet — open /survey once")
    chosen = next((s for s in sets if s[0] == args.set), None) if args.set else sets[-1]
    if chosen is None:
        sys.exit("no set with id %s (have: %s)" % (args.set, [s[0] for s in sets]))

    set_id, name, body = chosen
    defn = json.loads(body)
    q = numeric_question(defn)
    if q is None:
        sys.exit("set %d (%r) has no stars/scale/nps question to rate" % (set_id, name))
    low = q.get("min", 1) if q["type"] != "stars" else 1
    text_keys = [x["key"] for x in defn["questions"] if x["type"] == "text"]

    now = int(time.time())
    made = 0
    for i, (comment, hours_ago) in enumerate(SAMPLES):
        ts = now - hours_ago * 3600
        subject = "%s%d" % (MARKER, i + 1)
        cur = con.execute(
            "INSERT INTO responses(subject, subject_time, lang, submitted_at, definition_id, incomplete)"
            " VALUES(?, ?, 'en', ?, ?, 0)", (subject, ts, ts, set_id))
        rid = cur.lastrowid
        con.execute("INSERT INTO answers(response_id, question_key, num) VALUES(?, ?, ?)",
                    (rid, q["key"], low))
        if comment and text_keys:
            con.execute("INSERT INTO answers(response_id, question_key, text) VALUES(?, ?, ?)",
                        (rid, text_keys[-1], comment))
        made += 1
    con.commit()
    print("seeded %d responses rated %d on %r (set %d — %s)"
          % (made, low, q["key"], set_id, name or "unnamed"))
    print("open http://localhost:8080/dashboard and set the date range to include today")


if __name__ == "__main__":
    main()
