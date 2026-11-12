package feed

import (
	"testing"
	"time"

	"tripmiles/deals"
	"tripmiles/dealsvc"
)

func oneDealFeed(d deals.Deal) (*Feed, *dealsvc.Service) {
	svc := dealsvc.NewService()
	d.ID, d.PartnerID = "d", "p"
	return &Feed{Deals: deals.NewStore([]deals.Deal{d}, nil), DealsSvc: svc.NewClient()}, svc
}

func TestDealIsHiddenWhenItsTotalLimitIsReached(t *testing.T) {
	f, _ := oneDealFeed(deals.Deal{TotalLimit: 5})
	book(f, "d", 4)
	if !shows(f.List(), "d") {
		t.Fatal("4 of 5 used: want shown")
	}
	book(f, "d", 1)
	if shows(f.List(), "d") {
		t.Fatal("5 of 5 used: want hidden")
	}
}

func TestUnconfirmedBookingsCountForThePartnerLimit(t *testing.T) {
	f, _ := newFeed()
	book(f, "nordic-oslo", 15)
	book(f, "nordic-stockholm", 15)
	list := f.List()
	if shows(list, "nordic-oslo") || shows(list, "nordic-stockholm") {
		t.Fatal("30 unconfirmed of partner limit 30: want hidden")
	}
}

func TestNoAnswerCountsOwnBookingsSinceTheSnapshot(t *testing.T) {
	f, svc := oneDealFeed(deals.Deal{DailyLimit: 20})
	book(f, "d", 10)
	f.List()
	book(f, "d", 9)
	svc.FailEveryNth = 1
	if !shows(f.List(), "d") {
		t.Fatal("10 + 9 of 20: want shown")
	}
	book(f, "d", 1)
	if shows(f.List(), "d") {
		t.Fatal("10 + 10 of 20: want hidden")
	}
}

func TestNoAnswerSplitsWhatIsLeftBetweenServers(t *testing.T) {
	svc := dealsvc.NewService()
	store := deals.NewStore([]deals.Deal{{ID: "d", PartnerID: "p", DailyLimit: 100}}, nil)
	var feeds []*Feed
	for range 4 {
		f := &Feed{Deals: store, DealsSvc: svc.NewClient(), Servers: 4}
		f.List()
		feeds = append(feeds, f)
	}
	svc.FailEveryNth = 1
	for _, f := range feeds {
		for shows(f.List(), "d") {
			book(f, "d", 1)
		}
	}
	if got := svc.TrueNumbers("d").BookingsToday; got < 90 || got > 105 {
		t.Fatalf("4 servers gave away %d of 100 during the outage, want 90..105", got)
	}
}

func TestNoAnswerAndNoSnapshotShowsNothing(t *testing.T) {
	f, svc := newFeed()
	svc.FailEveryNth = 1
	if got := len(f.List()); got != 0 {
		t.Fatalf("got %d deals, want 0", got)
	}
}

func TestNoAnswerWithOldSnapshotShowsNothing(t *testing.T) {
	f, svc := newFeed()
	now := time.Unix(0, 0)
	f.budget.now = func() time.Time { return now }
	f.List()
	svc.FailEveryNth = 1
	now = now.Add(maxSnapshotAge + time.Second)
	if got := len(f.List()); got != 0 {
		t.Fatalf("got %d deals, want 0", got)
	}
}

func TestBookingDuringARequestIsNotLost(t *testing.T) {
	f, _ := oneDealFeed(deals.Deal{DailyLimit: 10})
	seqAtStart := f.budget.seq
	snap, _ := f.DealsSvc.Snapshot([]string{"d"}, nil)
	book(f, "d", 1) // after the snapshot was read, before it is stored
	f.budget.store(snap, seqAtStart, time.Now())
	if len(f.budget.pending) != 1 {
		t.Fatal("the booking is neither in the snapshot nor pending")
	}
}

func TestOlderAnswerDoesNotReplaceNewerSnapshot(t *testing.T) {
	f, _ := oneDealFeed(deals.Deal{DailyLimit: 10})
	old, _ := f.DealsSvc.Snapshot([]string{"d"}, nil)
	book(f, "d", 3)
	f.List()
	f.budget.store(old, 0, time.Now())
	if got := f.budget.last.Deals["d"].BookingsToday; got != 3 {
		t.Fatalf("cached bookings = %d, want 3", got)
	}
}

func TestFits(t *testing.T) {
	for _, c := range []struct {
		used, own, limit, servers int64
		want                      bool
	}{
		{1000, 0, 0, 1, true}, // no limit
		{19, 0, 20, 1, true},  // the last stay
		{20, 0, 20, 1, false},
		{0, 25, 100, 4, true}, // (25+1)*4 = 104 <= 105
		{0, 26, 100, 4, false},
		{90, 3, 100, 4, false}, // (3+1)*4 = 16 > 15
	} {
		if got := fits(c.used, c.own, c.limit, c.servers); got != c.want {
			t.Errorf("fits(%d, %d, %d, %d) = %v, want %v", c.used, c.own, c.limit, c.servers, got, c.want)
		}
	}
}
