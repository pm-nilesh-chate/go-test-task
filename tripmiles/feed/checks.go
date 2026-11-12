package feed

import (
	"sync"
	"time"

	"tripmiles/deals"
	"tripmiles/dealsvc"
)

// When the Deals Service does not answer, we decide from the last snapshot
// if it is younger than this. Otherwise we show nothing.
const maxSnapshotAge = time.Minute

// budgetState is what one server remembers between app opens.
type budgetState struct {
	mu sync.Mutex

	last    *dealsvc.Snapshot
	lastSeq uint64    // seq when the request for last started
	takenAt time.Time // time when the request for last started

	// Own bookings that last may not count yet. Each booking gets a seq after
	// it is recorded in the service, so seq <= lastSeq means "in last".
	seq     uint64
	pending []localBooking

	now func() time.Time // tests only
}

type localBooking struct {
	seq               uint64
	dealID, partnerID string
}

func (b *budgetState) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

func (b *budgetState) recordLocal(dealID, partnerID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	b.pending = append(b.pending, localBooking{b.seq, dealID, partnerID})
}

// store keeps snap unless a newer snapshot is already stored, and drops the
// bookings that snap counts. A booking made during the request stays pending:
// counting it twice is safe, losing it is not.
func (b *budgetState) store(snap dealsvc.Snapshot, seqAtStart uint64, startedAt time.Time) {
	if b.last != nil && seqAtStart < b.lastSeq {
		return
	}
	b.last, b.lastSeq, b.takenAt = &snap, seqAtStart, startedAt
	keep := b.pending[:0]
	for _, p := range b.pending {
		if p.seq > seqAtStart {
			keep = append(keep, p)
		}
	}
	b.pending = keep
}

func (f *Feed) budgetChecks(ds []deals.Deal) []deals.Deal {
	var dealIDs, partnerIDs []string
	seen := map[string]bool{}
	for _, d := range ds {
		dealIDs = append(dealIDs, d.ID)
		if !seen[d.PartnerID] {
			seen[d.PartnerID] = true
			partnerIDs = append(partnerIDs, d.PartnerID)
		}
	}

	b := &f.budget
	b.mu.Lock()
	seqAtStart, startedAt := b.seq, b.clock()
	b.mu.Unlock()

	snap, err := f.DealsSvc.Snapshot(dealIDs, partnerIDs)

	b.mu.Lock()
	defer b.mu.Unlock()

	if err == nil {
		b.store(snap, seqAtStart, startedAt)
		// We see every booking, so this is just "one more booking fits".
		return f.filter(ds, snap, nil, nil, 1)
	}
	if b.last == nil || b.clock().Sub(b.takenAt) > maxSnapshotAge {
		return []deals.Deal{}
	}
	ownDeal, ownPartner := map[string]int64{}, map[string]int64{}
	for _, p := range b.pending {
		ownDeal[p.dealID]++
		ownPartner[p.partnerID]++
	}
	return f.filter(ds, *b.last, ownDeal, ownPartner, f.servers())
}

func (f *Feed) filter(ds []deals.Deal, snap dealsvc.Snapshot, ownDeal, ownPartner map[string]int64, servers int64) []deals.Deal {
	out := []deals.Deal{}
	for _, d := range ds {
		n := snap.Deals[d.ID]
		partnerLimit := f.Deals.Partner(d.PartnerID).DailyLimit
		if fits(n.BookingsToday, ownDeal[d.ID], d.DailyLimit, servers) &&
			fits(n.BookingsTotal, ownDeal[d.ID], d.TotalLimit, servers) &&
			fits(snap.PartnerBookingsToday[d.PartnerID], ownPartner[d.PartnerID], partnerLimit, servers) {
			out = append(out, d)
		}
	}
	return out
}

func (f *Feed) servers() int64 {
	return max(int64(f.Servers), 1)
}

// fits reports whether this server may give away one more stay.
// used is the count in the snapshot, own is what this server gave away since.
//
// The other servers' bookings since the snapshot are unknown, so each server
// takes at most 1/servers of what was left (with the 5% that rule 3 allows).
// If all servers lose the service at once with snapshots of a similar age,
// together they stay within 105%. A partial outage or very different snapshot
// ages can go over that; only a reservation in the Deals Service prevents it.
// Small limits pay the price: with fewer than about `servers` stays left,
// a deal is hidden for the whole outage.
func fits(used, own, limit, servers int64) bool {
	if limit <= 0 {
		return true
	}
	left := limit*105/100 - used
	return used+own+1 <= limit && (own+1)*servers <= left
}
