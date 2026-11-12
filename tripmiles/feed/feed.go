// Package feed builds the list of deals that a traveller sees.
// The Offers team owns this package. The Offers team is your team.
package feed

import (
	"tripmiles/deals"
	"tripmiles/dealsvc"
)

// Feed runs on one app server. Each server has its own Feed and its own Deals Service Client.
type Feed struct {
	Deals    *deals.Store
	DealsSvc *dealsvc.Client
	Servers  int // number of app servers that share the limits; 0 means 1

	budget budgetState
}

// List runs each time a traveller opens the app.
// It returns the deals that the traveller can see.
func (f *Feed) List() []deals.Deal {
	return f.budgetChecks(f.Deals.All())
}

// Book records that a traveller booked a deal.
func (f *Feed) Book(d deals.Deal) {
	f.DealsSvc.RecordBooking(d.ID, d.PartnerID)
	f.budget.recordLocal(d.ID, d.PartnerID) // after RecordBooking, see budgetState
}
