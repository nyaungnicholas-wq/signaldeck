import sqlite3
import sys

try:
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
except Exception as e:
    print("INSUFFICIENT=1")
    sys.exit(0)

print("INSUFFICIENT=1")
sys.exit(0)