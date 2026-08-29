package main

import "fmt"

type MemberStatus string

const (
	StatusPaid    MemberStatus = "paid"
	StatusPartial MemberStatus = "partial"
	StatusOverdue MemberStatus = "overdue"
)

type Member struct {
	ID         string
	Name       string
	Email      string
	DuesAmount float64
	AmountPaid float64
	DueDate    string
	Status     MemberStatus
}

type MemberRow struct {
	Member
	Balance       float64
	BalanceDue    bool
	StatusLabel   string
	DuesFormatted string
	PaidFormatted string
	BalFormatted  string
}

type Summary struct {
	TotalExpected  float64
	TotalCollected float64
	PaidCount      int
	OverdueCount   int
	Outstanding    float64
	CollectionRate int
	ExpectedFmt    string
	CollectedFmt   string
	OutstandingFmt string
}

type PageData struct {
	Summary Summary
	Members []MemberRow
}

var sampleMembers = []Member{
	{ID: "1", Name: "Alex Rivera", Email: "alex@example.com", DuesAmount: 120, AmountPaid: 120, DueDate: "2026-01-15", Status: StatusPaid},
	{ID: "2", Name: "Jordan Lee", Email: "jordan@example.com", DuesAmount: 120, AmountPaid: 60, DueDate: "2026-01-15", Status: StatusPartial},
	{ID: "3", Name: "Sam Chen", Email: "sam@example.com", DuesAmount: 120, AmountPaid: 0, DueDate: "2026-01-15", Status: StatusOverdue},
	{ID: "4", Name: "Taylor Brooks", Email: "taylor@example.com", DuesAmount: 120, AmountPaid: 120, DueDate: "2026-01-15", Status: StatusPaid},
	{ID: "5", Name: "Morgan Walsh", Email: "morgan@example.com", DuesAmount: 120, AmountPaid: 0, DueDate: "2026-01-15", Status: StatusOverdue},
}

var statusLabels = map[MemberStatus]string{
	StatusPaid:    "Paid",
	StatusPartial: "Partial",
	StatusOverdue: "Overdue",
}

func formatCurrency(amount float64) string {
	return fmt.Sprintf("$%.2f", amount)
}

func summarize(members []Member) Summary {
	var totalExpected, totalCollected float64
	var paidCount, overdueCount int

	for _, m := range members {
		totalExpected += m.DuesAmount
		totalCollected += m.AmountPaid
		switch m.Status {
		case StatusPaid:
			paidCount++
		case StatusOverdue:
			overdueCount++
		}
	}

	outstanding := totalExpected - totalCollected
	rate := 0
	if totalExpected > 0 {
		rate = int(totalCollected/totalExpected*100 + 0.5)
	}

	return Summary{
		TotalExpected:  totalExpected,
		TotalCollected: totalCollected,
		PaidCount:      paidCount,
		OverdueCount:   overdueCount,
		Outstanding:    outstanding,
		CollectionRate: rate,
		ExpectedFmt:    formatCurrency(totalExpected),
		CollectedFmt:   formatCurrency(totalCollected),
		OutstandingFmt: formatCurrency(outstanding),
	}
}

func buildPageData(members []Member) PageData {
	summary := summarize(members)
	rows := make([]MemberRow, len(members))

	for i, m := range members {
		balance := m.DuesAmount - m.AmountPaid
		rows[i] = MemberRow{
			Member:        m,
			Balance:       balance,
			BalanceDue:    balance > 0,
			StatusLabel:   statusLabels[m.Status],
			DuesFormatted: formatCurrency(m.DuesAmount),
			PaidFormatted: formatCurrency(m.AmountPaid),
			BalFormatted:  formatCurrency(balance),
		}
	}

	return PageData{Summary: summary, Members: rows}
}
