import sqlite3
import sys

def main():
    try:
        db = sqlite3.connect("file:data/signaldeck.db?mode=ro", uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return 0

    try:
        # The documented schema contains no Form 4 insider-purchase data.
        # Without transaction code P, filer role, 10b5-1/option-exercise flags,
        # and the required exclusion calendar, the entry/abstain rules cannot be
        # computed without fabricating inputs.
        print("INSUFFICIENT=1")
        return 0
    finally:
        db.close()

if __name__ == "__main__":
    sys.exit(main())