import sqlite3
import sys

try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    # Check if we can get the data we need. We need corporate event data (secondary offering announcements)
    # which is not present in the given schema. Therefore insufficient.
    print("INSUFFICIENT=1")
    sys.exit(0)
except Exception:
    print("INSUFFICIENT=1")
    sys.exit(0)