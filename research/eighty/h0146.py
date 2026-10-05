import sqlite3
import sys

def main():
    try:
        db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
        try:
            schema = "\n".join(
                f"{name} {sql or ''}"
                for name, sql in db.execute(
                    "SELECT name, sql FROM sqlite_master WHERE type='table'"
                )
            ).lower()
        finally:
            db.close()
    except sqlite3.Error:
        print("INSUFFICIENT=1")
        return 0

    markers = (
        "schedule", "13d", "beneficial ownership", "item 4", "item4",
        "amendment", "activist",
    )
    if not any(m in schema for m in markers):
        print("INSUFFICIENT=1")
        return 0

    # A bare marker is still not enough: the documented tables expose no filing
    # date, amendment status, beneficial ownership percentage, Item 4 intent,
    # concurrent-event flags, earnings calendar, book equity, or going concern.
    print("INSUFFICIENT=1")
    return 0

if __name__ == "__main__":
    sys.exit(main())