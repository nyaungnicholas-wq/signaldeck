# WAITLIST — the decision, with the facts it turns on

OWNER_ACTIONS item 7. Measured 2026-09-15.

## What is true today

| | |
| --- | --- |
| Table | `waitlist(email TEXT PRIMARY KEY, created_ts INTEGER, source TEXT)` |
| **Rows collected, ever** | **0** |
| Mail sent from it | none — the daemon has no sender wired to this table |
| Third-party access | none |
| Route | `POST /api/waitlist`, the only anonymous WRITE on a published deployment |
| Privacy / retention / removal text | **none, before this pass** |

The handler itself is careful: it is documented as a trust boundary, normalises
and length-bounds the address, and refuses control characters specifically to
stop CR/LF header injection reaching any future mail envelope. The defect was
never the validation. It was that the page took a personal detail and said
nothing about what became of it.

## What I changed, and why it is safe under either decision

Added a collection notice beside the form. Every clause is checked against the
code, not aspirational: what is stored, that nothing else is, that none has been
sent, and that removal deletes the row rather than flagging it.

This is the fix that is correct whichever way you decide. It is required if
collection stays, and it is harmless if you later turn collection off. It commits
you to nothing — in particular it does not assert that you intend to email
anyone.

## The decision that is still yours

**Do you intend to email these people?**

**If no — turn collection off.** This is the cheapest option and, at 0 rows,
costs nothing and surprises nobody: there is no list to lose and no one to
notify. Reversible at any time.

**If yes — two things are still missing**, and the notice above is not a
substitute for either:

1. **A retention period.** "Until the record opens" is a purpose, not a period.
   Decide how long an address is kept if that never happens.
2. **A removal path a stranger can actually use.** The notice says "contact the
   operator" because I do not have an address to publish and will not invent one.
   Put a real one there, or add a removal endpoint.

## What I deliberately did not do

I did not disable collection, and I did not choose a retention period. Both
depend on whether you intend to email anyone, which is a fact about your plans
rather than about the code. No test email was sent to any real address.
