# Notes

Changed: `feed/checks.go`, `feed/feed.go`, `cmd/sim/main.go` (one line). New: `feed/checks_test.go`, `NOTES.md`.
All changes are in one commit on top of the original one: `git show HEAD` shows the full diff.

- **Budget check** (`feed/checks.go`): one `Snapshot` request per app open. A deal is shown if one more booking fits its daily limit, its total limit and its partner's daily limit. All bookings count, confirmed or not.
- **No answer:** each server keeps its last snapshot and its own bookings since then. It may give away only `(105% of limit − snapshot count) / Servers`, so all servers together stay within 105% during an outage. No snapshot, or one older than a minute: show nothing.
- **Races:** own bookings get a sequence number, so a booking made while a request is in flight is not lost, and a late, older answer does not replace a newer snapshot.
- **`cmd/sim`:** the only change passes `Servers: 4`. The rule checks are unchanged, and the original simulator passes too.
- **Trade-offs:** in an outage, deals can disappear before they are full, and small limits (fewer than ~`Servers` stays left) are hidden at once. Rules 3 and 4 conflict there; I chose rule 3. The 105% bound assumes all servers lose the service at once with similar snapshots.
- **At scale:** a read-then-book check cannot be exact. I would reserve a stay atomically in the Deals Service (e.g. Redis `INCR` with a limit, with a TTL until the partner confirms), or lease each server a quota.
