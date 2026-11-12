// Command sim runs one busy day of TripMiles. Then it checks the four rules in README.md.
//
//	go run ./cmd/sim
package main

import (
	"fmt"
	"os"
	"sort"
	"sync"

	"tripmiles/deals"
	"tripmiles/dealsvc"
	"tripmiles/feed"
)

const (
	appOpens     = 3000 // number of app opens in the day
	servers      = 4    // number of app servers; all use one Deals Service
	unconfirmed  = 150  // partners confirm all bookings except the newest 150
	failEvery    = 100  // the service does not answer every 100th request
	msPerRequest = 1    // one request to the service takes about 1 ms
)

func main() {
	store := dayDeals()
	svc := dealsvc.NewService()
	svc.FailEveryNth = failEvery

	r := playTheDay(store, svc)
	ok := checkTheRules(store, svc, r)
	if !ok {
		os.Exit(1)
	}
}

// dayDeals returns the deals for the simulated day. They are the 6 deals from
// README.md. The limits are larger because the day has more traffic.
func dayDeals() *deals.Store {
	return deals.NewStore(
		[]deals.Deal{
			{ID: "skyline-lisbon", Name: "Skyline Air: Berlin to Lisbon", PartnerID: "skyline", DailyLimit: 450},
			{ID: "skyline-rome", Name: "Skyline Air: Berlin to Rome", PartnerID: "skyline", DailyLimit: 400},
			{ID: "harbor-paris", Name: "Harbor Air: Hamburg to Paris", PartnerID: "harbor", DailyLimit: 250},
			{ID: "harbor-vienna", Name: "Harbor Air: Hamburg to Vienna", PartnerID: "harbor", DailyLimit: 400},
			{ID: "nordic-oslo", Name: "Nordic Air: Munich to Oslo", PartnerID: "nordic", DailyLimit: 400},
			{ID: "nordic-stockholm", Name: "Nordic Air: Munich to Stockholm", PartnerID: "nordic", DailyLimit: 400},
		},
		[]deals.Partner{
			{ID: "skyline", Name: "Skyline Air"},
			{ID: "harbor", Name: "Harbor Air"},
			{ID: "nordic", Name: "Nordic Air", DailyLimit: 550},
		},
	)
}

type results struct {
	requestsPerOpen     []int // the number of requests that each app open sent
	affordableButHidden int   // the number of app opens that removed an affordable deal
	firstLimitReachedAt int   // the minute of the day; -1 if no limit was reached
}

// playTheDay sends the app opens to the 4 servers.
// Every second traveller books the first deal on the list, if the list is not empty.
func playTheDay(store *deals.Store, svc *dealsvc.Service) results {
	var (
		mu sync.Mutex
		r  = results{firstLimitReachedAt: -1}
		wg sync.WaitGroup
	)
	for s := 0; s < servers; s++ {
		wg.Add(1)
		go func(server int) {
			defer wg.Done()
			f := &feed.Feed{Deals: store, DealsSvc: svc.NewClient(), Servers: servers}
			for open := server; open < appOpens; open += servers {
				minute := open * 24 * 60 / appOpens

				before := f.DealsSvc.Calls()
				list := f.List()
				sent := f.DealsSvc.Calls() - before
				hidden := staysLeftButHidden(store, svc, list)

				reached := false
				if len(list) > 0 && open%2 == 0 { // every second traveller books
					f.Book(list[0])
					reached = limitReached(svc, list[0])
				}
				svc.ConfirmAllButNewest(unconfirmed)

				mu.Lock()
				r.requestsPerOpen = append(r.requestsPerOpen, sent)
				if hidden {
					r.affordableButHidden++
				}
				if reached && (r.firstLimitReachedAt == -1 || minute < r.firstLimitReachedAt) {
					r.firstLimitReachedAt = minute
				}
				mu.Unlock()
			}
		}(s)
	}
	wg.Wait()
	return r
}

// staysLeftButHidden returns true if the list does not have a deal that clearly has stays left.
// A deal clearly has stays left if it has used less than 80% of its daily limit, and its
// partner has used less than 80% of its partner daily limit.
func staysLeftButHidden(store *deals.Store, svc *dealsvc.Service, list []deals.Deal) bool {
	shown := map[string]bool{}
	for _, d := range list {
		shown[d.ID] = true
	}
	for _, d := range store.All() {
		dealBooked := svc.TrueNumbers(d.ID).BookingsToday
		limit := store.Partner(d.PartnerID).DailyLimit
		partnerBooked := svc.TruePartnerBookingsToday(d.PartnerID)
		staysLeft := dealBooked*10 < d.DailyLimit*8 && (limit == 0 || partnerBooked*10 < limit*8)
		if staysLeft && !shown[d.ID] {
			return true
		}
	}
	return false
}

func limitReached(svc *dealsvc.Service, d deals.Deal) bool {
	return svc.TrueNumbers(d.ID).BookingsToday+1 > d.DailyLimit
}

func checkTheRules(store *deals.Store, svc *dealsvc.Service, r results) bool {
	total := 0
	for _, n := range r.requestsPerOpen {
		total += n
	}
	sort.Ints(r.requestsPerOpen)
	p99ms := r.requestsPerOpen[len(r.requestsPerOpen)*99/100] * msPerRequest
	avgRequests := float64(total) / float64(len(r.requestsPerOpen))

	worstName, worstBooked, worstLimit := "", int64(0), int64(1)
	consider := func(name string, booked, limit int64) {
		if limit > 0 && booked*worstLimit > worstBooked*limit {
			worstName, worstBooked, worstLimit = name, booked, limit
		}
	}
	partners := map[string]bool{}
	for _, d := range store.All() {
		consider(d.Name, svc.TrueNumbers(d.ID).BookingsToday, d.DailyLimit)
		partners[d.PartnerID] = true
	}
	for id := range partners {
		p := store.Partner(id)
		consider(p.Name+" (partner daily limit)", svc.TruePartnerBookingsToday(id), p.DailyLimit)
	}
	overPct := float64(worstBooked-worstLimit) * 100 / float64(worstLimit)

	fmt.Printf("Simulated day: %d app opens on %d servers. Confirmations arrive late.\n", appOpens, servers)
	fmt.Printf("The Deals Service does not answer 1 in %d requests.\n\n", failEvery)

	ok := check("1. Each app open sends at most 1 request",
		total <= len(r.requestsPerOpen),
		fmt.Sprintf("Each app open sent %.1f requests on average.", avgRequests))
	ok = check("2. 99 of 100 app opens take 15 ms or less",
		p99ms <= 15,
		fmt.Sprintf("The slowest 1 in 100 app opens took %d ms.", p99ms)) && ok
	ok = check("3. No deal or partner goes over 105% of its daily limit",
		overPct <= 5,
		fmt.Sprintf("%s gave away %d stays. Its limit is %d. That is %.1f%% over.",
			worstName, worstBooked, worstLimit, overPct)) && ok
	ok = check("4. Deals with stays left stay on the list",
		r.affordableButHidden == 0,
		fmt.Sprintf("%d app opens removed a deal that had used less than 80%% of its limit.", r.affordableButHidden)) && ok

	if r.firstLimitReachedAt >= 0 {
		fmt.Printf("\nINFO   The first deal reached its daily limit at %02d:%02d.\n", r.firstLimitReachedAt/60, r.firstLimitReachedAt%60)
	}
	return ok
}

func check(rule string, pass bool, detail string) bool {
	if pass {
		fmt.Printf("CHECK  %-58s PASS\n", rule)
	} else {
		fmt.Printf("CHECK  %-58s FAIL  %s\n", rule, detail)
	}
	return pass
}
