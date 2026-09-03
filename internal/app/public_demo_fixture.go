package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wmichelin/clubdue/internal/servicework"
)

const (
	publicDemoPlanCount                 = 6
	publicDemoCustomerCount             = 30
	publicDemoPropertyCount             = 31
	publicDemoAgreementCount            = 26
	publicDemoScheduleCount             = 22
	publicDemoEquipmentCount            = 8
	publicDemoActiveAgreementCount      = 20
	publicDemoActiveScheduleCount       = 16
	publicDemoBaseOccurrenceCeiling     = 100
	publicDemoDueOccurrenceCeiling      = 60
	publicDemoSeasonalVisitCount        = 8
	publicDemoVisibleRenewalTaskCeiling = 15
)

type publicDemoPlanFixture struct {
	Key, Name, Description, BillingInterval string
	PriceCents, TermMonths, VisitsPerYear   int
	VisitWindows                            []string
}

type publicDemoCustomerFixture struct {
	Key, Name, Email, Phone string
}

type publicDemoPropertyFixture struct {
	Key, CustomerKey, Label                      string
	AddressLine1, AddressLine2, City, State, ZIP string
}

type publicDemoEquipmentFixture struct {
	Key, PropertyKey, Kind, Make, Model, Identifier string
	InstalledMonthsAgo                              int
}

type publicDemoAgreementFixture struct {
	Key, CustomerKey, PropertyKey, PlanKey, Notes string
	Status                                        AgreementStatus
	RenewalDays, VisitsEntitled, VisitsUsed       int
	PriceCents                                    int
}

type publicDemoScheduleFixture struct {
	Key, AgreementKey, ServiceName, CadenceUnit string
	CadenceInterval, StartsDaysAgo              int
	HasEnd                                      bool
	EndsDaysFromNow                             int
	Active                                      bool
}

type publicDemoRecurringOutcomeFixture struct {
	ScheduleKey, Status, Notes string
}

type publicDemoNonActionableHistoryFixture struct {
	ScheduleKey, Status, Notes string
	DaysAgo                    int
}

type publicDemoSeasonalHistoryFixture struct {
	AgreementKey, Outcome string
}

type publicDemoRenewalHistoryFixture struct {
	AgreementKey, Status, Notes string
	MilestoneDays               int
}

type publicDemoFixtureSpec struct {
	Plans                []publicDemoPlanFixture
	Customers            []publicDemoCustomerFixture
	Properties           []publicDemoPropertyFixture
	Equipment            []publicDemoEquipmentFixture
	Agreements           []publicDemoAgreementFixture
	Schedules            []publicDemoScheduleFixture
	RecurringOutcomes    []publicDemoRecurringOutcomeFixture
	NonActionableHistory []publicDemoNonActionableHistoryFixture
	SeasonalHistory      []publicDemoSeasonalHistoryFixture
	RenewalHistory       []publicDemoRenewalHistoryFixture
}

// publicDemoFixture is the single deterministic source of truth for a fresh
// public workspace. It is intentionally authored rather than randomized so a
// prospect, smoke test, and support conversation all see the same business.
var publicDemoFixture = publicDemoFixtureSpec{
	Plans: []publicDemoPlanFixture{
		{"weekly-lawn", "Weekly Lawn Care", "Weekly mowing, trimming, edging, and hard-surface blow-off.", "monthly", 19500, 12, 0, []string{}},
		{"biweekly-lawn", "Biweekly Lawn Care", "Every-other-week mowing and edging for slower-growth properties.", "monthly", 13500, 12, 0, []string{}},
		{"complete-property", "Complete Property Care", "Recurring lawn care plus spring and fall property-detail visits.", "monthly", 32500, 12, 2, []string{"spring", "fall"}},
		{"irrigation", "Irrigation Monitoring", "Monthly controller, zone, and visible leak inspection; repairs quoted separately.", "monthly", 8900, 12, 0, []string{}},
		{"seasonal-cleanup", "Seasonal Cleanup Plan", "Spring bed cleanup and fall leaf removal with haul-away.", "annual", 49500, 12, 2, []string{"spring", "fall"}},
		{"commercial-grounds", "Commercial Grounds Care", "Recurring grounds service for small commercial and shared-community sites.", "monthly", 65000, 12, 0, []string{}},
	},
	Customers: []publicDemoCustomerFixture{
		{"maya-thompson", "Maya Thompson", "maya.thompson@evergreen-demo.test", "(614) 555-0101"},
		{"daniel-brooks", "Daniel Brooks", "daniel.brooks@evergreen-demo.test", "(614) 555-0102"},
		{"avery-patel", "Avery Patel", "avery.patel@evergreen-demo.test", "(614) 555-0103"},
		{"elena-garcia", "Elena Garcia", "elena.garcia@evergreen-demo.test", "(614) 555-0104"},
		{"marcus-reed", "Marcus Reed", "marcus.reed@evergreen-demo.test", "(614) 555-0105"},
		{"priya-shah", "Priya Shah", "priya.shah@evergreen-demo.test", ""},
		{"noah-williams", "Noah Williams", "noah.williams@evergreen-demo.test", "(614) 555-0107"},
		{"grace-kim", "Grace Kim", "grace.kim@evergreen-demo.test", "(614) 555-0108"},
		{"luis-mendoza", "Luis Mendoza", "", "(614) 555-0109"},
		{"hannah-clark", "Hannah Clark", "hannah.clark@evergreen-demo.test", "(614) 555-0110"},
		{"omar-hassan", "Omar Hassan", "omar.hassan@evergreen-demo.test", "(614) 555-0111"},
		{"natalie-foster", "Natalie Foster", "natalie.foster@evergreen-demo.test", "(614) 555-0112"},
		{"theo-bennett", "Theo Bennett", "theo.bennett@evergreen-demo.test", "(614) 555-0113"},
		{"sofia-nguyen", "Sofia Nguyen", "sofia.nguyen@evergreen-demo.test", "(614) 555-0114"},
		{"jamal-carter", "Jamal Carter", "jamal.carter@evergreen-demo.test", "(614) 555-0115"},
		{"claire-donovan", "Claire Donovan", "claire.donovan@evergreen-demo.test", "(614) 555-0116"},
		{"ethan-price", "Ethan Price", "ethan.price@evergreen-demo.test", "(614) 555-0117"},
		{"ruby-chen", "Ruby Chen", "ruby.chen@evergreen-demo.test", "(614) 555-0118"},
		{"calvin-moore", "Calvin Moore", "calvin.moore@evergreen-demo.test", "(614) 555-0119"},
		{"leah-simmons", "Leah Simmons", "leah.simmons@evergreen-demo.test", "(614) 555-0120"},
		{"pine-porch", "Pine & Porch Cafe", "operations@pineandporch.test", "(614) 555-0121"},
		{"willowmere-hoa", "Willowmere Commons HOA", "board@willowmerecommons.test", "(614) 555-0122"},
		{"northstar-childcare", "Northstar Childcare Center", "office@northstarchildcare.test", "(614) 555-0123"},
		{"hearthside-vet", "Hearthside Veterinary Clinic", "facilities@hearthsidevet.test", "(614) 555-0124"},
		{"buckeye-key", "Buckeye Key Property Management", "routes@buckeyekey.test", "(614) 555-0125"},
		{"scioto-nest", "Scioto Nest Rentals", "maintenance@sciotonest.test", "(614) 555-0126"},
		{"gwen-wallace", "Gwen Wallace", "gwen.wallace@evergreen-demo.test", "(614) 555-0127"},
		{"rowan-ellis", "Rowan Ellis", "rowan.ellis@evergreen-demo.test", ""},
		{"mateo-russell", "Mateo Russell", "", "(740) 555-0129"},
		{"imani-brooks", "Imani Brooks", "imani.brooks@evergreen-demo.test", "(614) 555-0130"},
	},
	Properties: []publicDemoPropertyFixture{
		{"maya-home", "maya-thompson", "Home", "184 Willow Lane", "", "Columbus", "OH", "43215"},
		{"daniel-home", "daniel-brooks", "Home", "72 Garden Court", "", "Bexley", "OH", "43209"},
		{"avery-home", "avery-patel", "Home", "910 Sycamore Drive", "", "Dublin", "OH", "43017"},
		{"elena-home", "elena-garcia", "Home", "441 Alder Way", "", "Westerville", "OH", "43081"},
		{"marcus-home", "marcus-reed", "Home", "268 Hawthorn Avenue", "", "Gahanna", "OH", "43230"},
		{"priya-home", "priya-shah", "Home", "35 Briar Hollow Road", "", "Upper Arlington", "OH", "43221"},
		{"noah-home", "noah-williams", "Home", "827 Meadowcrest Drive", "", "Hilliard", "OH", "43026"},
		{"grace-home", "grace-kim", "Home", "116 Fernbank Court", "", "Worthington", "OH", "43085"},
		{"luis-home", "luis-mendoza", "Home", "593 Oakmere Street", "", "Grove City", "OH", "43123"},
		{"hannah-home", "hannah-clark", "Home", "204 River Birch Lane", "", "Grandview Heights", "OH", "43212"},
		{"omar-home", "omar-hassan", "Home", "761 Brookstone Way", "", "Reynoldsburg", "OH", "43068"},
		{"natalie-home", "natalie-foster", "Home", "49 Mulberry Place", "", "New Albany", "OH", "43054"},
		{"theo-home", "theo-bennett", "Home", "318 Juniper Ridge", "", "Pickerington", "OH", "43147"},
		{"sofia-home", "sofia-nguyen", "Home", "682 Cloverfield Avenue", "", "Columbus", "OH", "43214"},
		{"jamal-home", "jamal-carter", "Home", "927 Redbud Court", "", "Canal Winchester", "OH", "43110"},
		{"claire-home", "claire-donovan", "Home", "155 Orchard Bend", "", "Powell", "OH", "43065"},
		{"ethan-home", "ethan-price", "Home", "408 Linden Park Drive", "", "Columbus", "OH", "43206"},
		{"ruby-home", "ruby-chen", "Home", "733 Hemlock Lane", "", "Worthington", "OH", "43085"},
		{"ruby-rental", "ruby-chen", "Additional property — no current plan", "219 Maple Crossing", "", "Columbus", "OH", "43201"},
		{"calvin-home", "calvin-moore", "Home", "64 Prairie Rose Court", "", "Dublin", "OH", "43016"},
		{"leah-home", "leah-simmons", "Home", "506 Elmstead Avenue", "", "Westerville", "OH", "43082"},
		{"pine-storefront", "pine-porch", "Storefront", "81 Market Row", "Suite 104", "Columbus", "OH", "43215"},
		{"willow-common", "willowmere-hoa", "Common grounds", "1400 Willowmere Commons Boulevard", "", "Hilliard", "OH", "43026"},
		{"northstar-campus", "northstar-childcare", "Childcare grounds", "390 Sunbeam Drive", "", "Gahanna", "OH", "43230"},
		{"hearthside-clinic", "hearthside-vet", "Clinic grounds", "770 Hearthstone Avenue", "", "Grove City", "OH", "43123"},
		{"buckeye-north", "buckeye-key", "North portfolio", "1218 Crestview Lane", "", "Columbus", "OH", "43220"},
		{"buckeye-east", "buckeye-key", "Prospective east location", "642 Walnut Grove Road", "", "Bexley", "OH", "43209"},
		{"buckeye-south", "buckeye-key", "Prospective south location", "905 Bramble Street", "", "Grove City", "OH", "43123"},
		{"scioto-riverside", "scioto-nest", "Riverside rental", "303 Riverside Walk", "Unit A", "Dublin", "OH", "43017"},
		{"scioto-quarry", "scioto-nest", "Prospective quarry rental", "587 Quarry Bend", "", "Hilliard", "OH", "43026"},
		{"scioto-foxglove", "scioto-nest", "Prospective foxglove rental", "88 Foxglove Court", "", "Worthington", "OH", "43085"},
	},
	Equipment: []publicDemoEquipmentFixture{
		{"avery-controller", "avery-home", "Irrigation controller", "Hunter", "Pro-C", "DEMO-CTRL-0103", 30},
		{"hannah-controller", "hannah-home", "Irrigation controller", "Rain Bird", "ESP-TM2", "DEMO-CTRL-0110", 18},
		{"claire-controller", "claire-home", "Smart irrigation controller", "Rachio", "3", "DEMO-CTRL-0116", 26},
		{"willow-controller", "willow-common", "Commercial irrigation controller", "Rain Bird", "ESP-LXME", "DEMO-CTRL-0122", 42},
		{"northstar-controller", "northstar-campus", "Irrigation controller", "Hunter", "X2", "DEMO-CTRL-0123", 15},
		{"buckeye-controller", "buckeye-north", "Irrigation controller", "Orbit", "B-hyve", "DEMO-CTRL-0125", 22},
		{"scioto-backflow", "scioto-riverside", "Irrigation backflow assembly", "Watts", "800M4", "DEMO-BACKFLOW-0126", 48},
		{"maya-rain-sensor", "maya-home", "Wireless rain sensor", "Hunter", "Rain-Clik", "DEMO-SENSOR-0101", 20},
	},
	Agreements: []publicDemoAgreementFixture{
		{"maya", "maya-thompson", "maya-home", "weekly-lawn", "Fictional scope: front, side, and back lawn; include curb strip.", AgreementActive, 14, 0, 0, 20500},
		{"daniel", "daniel-brooks", "daniel-home", "biweekly-lawn", "Fictional scope: biweekly route with clippings hauled during leaf season.", AgreementActive, 30, 0, 0, 14500},
		{"avery", "avery-patel", "avery-home", "irrigation", "Fictional scope: controller and visible-zone inspection; quote repairs separately.", AgreementActive, 58, 0, 0, 9900},
		{"elena", "elena-garcia", "elena-home", "weekly-lawn", "Fictional scope: corner lot; edge both street-facing sidewalks.", AgreementActive, 82, 0, 0, 21500},
		{"marcus", "marcus-reed", "marcus-home", "biweekly-lawn", "Fictional scope: mow and edge; keep the west-side beds sharply defined.", AgreementActive, -6, 0, 0, 15000},
		{"priya", "priya-shah", "priya-home", "weekly-lawn", "Fictional scope: recurring service is paused while the yard renovation is underway.", AgreementActive, 120, 0, 0, 19500},
		{"noah", "noah-williams", "noah-home", "complete-property", "Fictional scope: full lawn route plus spring and fall bed detailing.", AgreementActive, 145, 2, 0, 34500},
		{"grace", "grace-kim", "grace-home", "seasonal-cleanup", "Fictional scope: spring bed reset and fall leaf haul-away.", AgreementActive, 175, 2, 0, 52500},
		{"luis", "luis-mendoza", "luis-home", "biweekly-lawn", "Fictional scope: front and side lawn only; backyard maintained separately.", AgreementActive, 205, 0, 0, 13500},
		{"hannah", "hannah-clark", "hannah-home", "irrigation", "Fictional scope: monthly controller review and visual leak check.", AgreementActive, 230, 0, 0, 8900},
		{"omar", "omar-hassan", "omar-home", "weekly-lawn", "Fictional scope: standard weekly mow, trim, edge, and blow-off.", AgreementActive, 255, 0, 0, 19500},
		{"natalie", "natalie-foster", "natalie-home", "weekly-lawn", "Fictional scope: weekly route with the rear garden path excluded.", AgreementActive, 280, 0, 0, 20500},
		{"theo", "theo-bennett", "theo-home", "biweekly-lawn", "Fictional scope: every-other-week mowing for the slower-growth lawn.", AgreementActive, 310, 0, 0, 15500},
		{"sofia", "sofia-nguyen", "sofia-home", "seasonal-cleanup", "Fictional scope: two seasonal cleanup visits with debris haul-away.", AgreementActive, 335, 2, 0, 47500},
		{"jamal", "jamal-carter", "jamal-home", "weekly-lawn", "Fictional scope: weekly lawn route; preserve the native planting border.", AgreementActive, 110, 0, 0, 18500},
		{"claire", "claire-donovan", "claire-home", "irrigation", "Fictional scope: controller programming check and zone walk-through.", AgreementActive, 200, 0, 0, 8900},
		{"ethan", "ethan-price", "ethan-home", "weekly-lawn", "Fictional scope: service proposal awaiting renewal confirmation.", AgreementPendingRenewal, 5, 0, 0, 19500},
		{"ruby", "ruby-chen", "ruby-home", "biweekly-lawn", "Fictional scope: prior biweekly service retained as account history.", AgreementLapsed, -30, 0, 0, 13500},
		{"calvin", "calvin-moore", "calvin-home", "irrigation", "Fictional scope: prior irrigation monitoring agreement retained as history.", AgreementLapsed, -12, 0, 0, 8900},
		{"leah", "leah-simmons", "leah-home", "weekly-lawn", "Fictional scope: cancelled recurring route retained for reporting.", AgreementCancelled, -45, 0, 0, 19500},
		{"pine", "pine-porch", "pine-storefront", "commercial-grounds", "Fictional scope: storefront beds, sidewalk edging, and litter sweep.", AgreementActive, 130, 0, 0, 47500},
		{"willow", "willowmere-hoa", "willow-common", "commercial-grounds", "Fictional scope: entrance beds, common lawn, and irrigation walk-through.", AgreementActive, 160, 0, 0, 65000},
		{"northstar", "northstar-childcare", "northstar-campus", "commercial-grounds", "Fictional scope: grounds proposal is pending renewal approval.", AgreementPendingRenewal, 21, 0, 0, 52500},
		{"hearthside", "hearthside-vet", "hearthside-clinic", "commercial-grounds", "Fictional scope: clinic lawn and parking-edge cleanup; account is past due.", AgreementPastDue, -15, 0, 0, 59000},
		{"buckeye", "buckeye-key", "buckeye-north", "complete-property", "Fictional scope: north location only; east and south records are not yet under plan.", AgreementActive, 190, 2, 0, 37500},
		{"scioto", "scioto-nest", "scioto-riverside", "commercial-grounds", "Fictional scope: Riverside rental only; other stored locations are prospective.", AgreementActive, 260, 0, 0, 72500},
	},
	Schedules: []publicDemoScheduleFixture{
		{"maya-weekly", "maya", "Weekly mow, trim, edge & blow", "week", 1, 28, true, 35, true},
		{"daniel-biweekly", "daniel", "Biweekly lawn service", "week", 2, 28, true, 35, true},
		{"avery-irrigation", "avery", "Monthly irrigation inspection", "month", 1, 60, true, 35, true},
		{"elena-weekly", "elena", "Weekly corner-lot lawn service", "week", 1, 25, true, 35, true},
		{"marcus-biweekly", "marcus", "Biweekly mow and bed edging", "week", 2, 24, true, 35, true},
		{"willow-monthly", "willow", "Monthly common-grounds inspection", "month", 1, 45, true, 35, true},
		{"priya-paused", "priya", "Weekly lawn service", "week", 1, 84, false, 0, false},
		{"pine-paused", "pine", "Weekly storefront grounds service", "week", 1, 56, false, 0, false},
		{"buckeye-monthly", "buckeye", "Monthly north-location property care", "month", 1, 60, true, 35, true},
		{"ethan-inactive", "ethan", "Weekly lawn service", "week", 1, 56, true, 120, true},
		{"ruby-inactive", "ruby", "Biweekly lawn service", "week", 2, 70, true, 60, true},
		{"hearthside-inactive", "hearthside", "Weekly clinic grounds service", "week", 1, 35, true, 60, true},
		{"noah-monthly", "noah", "Monthly complete-property visit", "month", 1, 50, true, 35, true},
		{"luis-biweekly", "luis", "Biweekly front-and-side lawn service", "week", 2, 20, true, 35, true},
		{"hannah-irrigation", "hannah", "Monthly irrigation inspection", "month", 1, 40, true, 35, true},
		{"omar-weekly", "omar", "Weekly mow, trim, edge & blow", "week", 1, 21, true, 35, true},
		{"natalie-weekly", "natalie", "Weekly lawn and path-edge service", "week", 1, 18, true, 35, true},
		{"theo-biweekly", "theo", "Biweekly slower-growth lawn service", "week", 2, 18, true, 35, true},
		{"jamal-weekly", "jamal", "Weekly lawn service", "week", 1, 15, true, 35, true},
		{"claire-irrigation", "claire", "Monthly irrigation inspection", "month", 1, 20, true, 35, true},
		{"scioto-monthly", "scioto", "Monthly Riverside rental grounds service", "month", 1, 10, true, 35, true},
		{"grace-ended", "grace", "Prior monthly bed-care route", "month", 1, 120, true, -14, false},
	},
	RecurringOutcomes: []publicDemoRecurringOutcomeFixture{
		{"maya-weekly", "due", "Fictional route item awaiting assignment"},
		{"daniel-biweekly", "due", "Fictional follow-up carried into today's route"},
		{"elena-weekly", "due", "Fictional weather delay needs a new route slot"},
		{"marcus-biweekly", "skipped", "Fictional rain delay; next recurring visit remains planned"},
	},
	NonActionableHistory: []publicDemoNonActionableHistoryFixture{
		{"priya-paused", "due", "Fictional work retained while this route is paused", 8},
		{"ethan-inactive", "completed", "Fictional final visit before renewal review", 18},
		{"ruby-inactive", "skipped", "Fictional service stopped when the prior plan lapsed", 12},
		{"hearthside-inactive", "due", "Fictional work held while the account is past due", 5},
	},
	SeasonalHistory: []publicDemoSeasonalHistoryFixture{
		{"noah", "completed"},
		{"grace", "completed"},
		{"buckeye", "completed"},
		{"sofia", "scheduled"},
	},
	RenewalHistory: []publicDemoRenewalHistoryFixture{
		{"maya", "contacted", "Fictional voicemail left; follow up in two business days", 14},
	},
}

func (f publicDemoFixtureSpec) validate(asOf time.Time) error {
	if len(f.Plans) != publicDemoPlanCount || len(f.Customers) != publicDemoCustomerCount ||
		len(f.Properties) != publicDemoPropertyCount || len(f.Agreements) != publicDemoAgreementCount ||
		len(f.Schedules) != publicDemoScheduleCount || len(f.Equipment) != publicDemoEquipmentCount ||
		len(f.RecurringOutcomes) != 4 || len(f.NonActionableHistory) != 4 ||
		len(f.SeasonalHistory) != 4 || len(f.RenewalHistory) != 1 {
		return fmt.Errorf("public demo fixture counts changed unexpectedly")
	}

	plans := make(map[string]publicDemoPlanFixture, len(f.Plans))
	for _, plan := range f.Plans {
		if plan.Key == "" || plan.Name == "" || plan.PriceCents <= 0 || plan.TermMonths <= 0 {
			return fmt.Errorf("invalid public demo plan %q", plan.Key)
		}
		if plan.BillingInterval != "monthly" && plan.BillingInterval != "annual" {
			return fmt.Errorf("invalid public demo billing interval for %q", plan.Key)
		}
		if plan.VisitsPerYear == 0 && len(plan.VisitWindows) != 0 ||
			plan.VisitsPerYear > 0 && len(plan.VisitWindows) == 0 {
			return fmt.Errorf("invalid public demo seasonal terms for %q", plan.Key)
		}
		for _, window := range plan.VisitWindows {
			if window != "spring" && window != "fall" {
				return fmt.Errorf("invalid public demo visit window for %q", plan.Key)
			}
		}
		if _, exists := plans[plan.Key]; exists {
			return fmt.Errorf("duplicate public demo plan key %q", plan.Key)
		}
		plans[plan.Key] = plan
	}

	customers := make(map[string]publicDemoCustomerFixture, len(f.Customers))
	emails := make(map[string]struct{})
	phones := make(map[string]struct{})
	hasEmailOnly, hasPhoneOnly := false, false
	for _, customer := range f.Customers {
		if customer.Key == "" || customer.Name == "" {
			return fmt.Errorf("invalid public demo customer %q", customer.Key)
		}
		if customer.Email != "" && (strings.Count(customer.Email, "@") != 1 || !strings.HasSuffix(strings.ToLower(customer.Email), ".test")) {
			return fmt.Errorf("public demo customer %q has non-reserved email", customer.Key)
		}
		if customer.Phone != "" && !isReservedPublicDemoPhone(customer.Phone) {
			return fmt.Errorf("public demo customer %q has non-fictional phone", customer.Key)
		}
		if customer.Email != "" {
			email := strings.ToLower(customer.Email)
			if _, exists := emails[email]; exists {
				return fmt.Errorf("duplicate public demo email %q", email)
			}
			emails[email] = struct{}{}
		}
		if customer.Phone != "" {
			if _, exists := phones[customer.Phone]; exists {
				return fmt.Errorf("duplicate public demo phone %q", customer.Phone)
			}
			phones[customer.Phone] = struct{}{}
		}
		if _, exists := customers[customer.Key]; exists {
			return fmt.Errorf("duplicate public demo customer key %q", customer.Key)
		}
		customers[customer.Key] = customer
		hasEmailOnly = hasEmailOnly || customer.Email != "" && customer.Phone == ""
		hasPhoneOnly = hasPhoneOnly || customer.Email == "" && customer.Phone != ""
	}
	if !hasEmailOnly || !hasPhoneOnly {
		return fmt.Errorf("public demo fixture must exercise incomplete contact records")
	}

	properties := make(map[string]publicDemoPropertyFixture, len(f.Properties))
	cities := make(map[string]struct{})
	propertyCounts := make(map[string]int)
	for _, property := range f.Properties {
		if _, ok := customers[property.CustomerKey]; !ok || property.Key == "" || property.AddressLine1 == "" ||
			property.City == "" || property.State != "OH" || property.ZIP == "" {
			return fmt.Errorf("invalid public demo property %q", property.Key)
		}
		if _, exists := properties[property.Key]; exists {
			return fmt.Errorf("duplicate public demo property key %q", property.Key)
		}
		properties[property.Key] = property
		cities[property.City] = struct{}{}
		propertyCounts[property.CustomerKey]++
	}
	if len(cities) < 4 {
		return fmt.Errorf("public demo fixture must span multiple service cities")
	}
	multiLocationCustomers := 0
	for _, count := range propertyCounts {
		if count > 1 {
			multiLocationCustomers++
		}
	}
	if multiLocationCustomers != 3 {
		return fmt.Errorf("public demo fixture has %d multi-location customers, want 3", multiLocationCustomers)
	}

	equipment := make(map[string]struct{}, len(f.Equipment))
	for _, asset := range f.Equipment {
		if asset.Key == "" || asset.Kind == "" || asset.Identifier == "" {
			return fmt.Errorf("invalid public demo site asset %q", asset.Key)
		}
		if _, ok := properties[asset.PropertyKey]; !ok {
			return fmt.Errorf("public demo site asset %q has unknown property", asset.Key)
		}
		if _, exists := equipment[asset.Key]; exists {
			return fmt.Errorf("duplicate public demo site asset key %q", asset.Key)
		}
		equipment[asset.Key] = struct{}{}
	}

	agreements := make(map[string]publicDemoAgreementFixture, len(f.Agreements))
	statusCounts := make(map[AgreementStatus]int)
	seasonalAgreements := 0
	for _, agreement := range f.Agreements {
		property, propertyOK := properties[agreement.PropertyKey]
		plan, planOK := plans[agreement.PlanKey]
		if agreement.Key == "" || !propertyOK || !planOK || property.CustomerKey != agreement.CustomerKey {
			return fmt.Errorf("invalid public demo agreement %q references", agreement.Key)
		}
		switch agreement.Status {
		case AgreementActive, AgreementPastDue, AgreementLapsed, AgreementCancelled, AgreementPendingRenewal:
		default:
			return fmt.Errorf("invalid public demo agreement status for %q", agreement.Key)
		}
		if agreement.PriceCents <= 0 || agreement.VisitsUsed < 0 || agreement.VisitsUsed > agreement.VisitsEntitled {
			return fmt.Errorf("invalid public demo agreement terms for %q", agreement.Key)
		}
		if agreement.VisitsEntitled != plan.VisitsPerYear {
			return fmt.Errorf("public demo agreement %q does not match its captured visit terms", agreement.Key)
		}
		lowerNotes := strings.ToLower(agreement.Notes)
		for _, unsafeDetail := range []string{"gate code", "alarm code", "medical", "pet"} {
			if strings.Contains(lowerNotes, unsafeDetail) {
				return fmt.Errorf("public demo agreement %q contains sensitive operational detail", agreement.Key)
			}
		}
		if _, exists := agreements[agreement.Key]; exists {
			return fmt.Errorf("duplicate public demo agreement key %q", agreement.Key)
		}
		agreements[agreement.Key] = agreement
		statusCounts[agreement.Status]++
		if agreement.Status == AgreementActive && agreement.VisitsEntitled > 0 {
			seasonalAgreements++
		}
	}
	if statusCounts[AgreementActive] != publicDemoActiveAgreementCount || statusCounts[AgreementPastDue] != 1 ||
		statusCounts[AgreementPendingRenewal] != 2 || statusCounts[AgreementLapsed] != 2 ||
		statusCounts[AgreementCancelled] != 1 || seasonalAgreements*2 != publicDemoSeasonalVisitCount {
		return fmt.Errorf("public demo agreement distribution changed unexpectedly")
	}

	schedules := make(map[string]publicDemoScheduleFixture, len(f.Schedules))
	activeSchedules, projectedOccurrences := 0, 0
	activeCadences := make(map[string]struct{})
	modeledActiveAgreements := make(map[string]bool)
	for _, schedule := range f.Schedules {
		agreement, ok := agreements[schedule.AgreementKey]
		if schedule.Key == "" || !ok {
			return fmt.Errorf("invalid public demo schedule %q references", schedule.Key)
		}
		if _, exists := schedules[schedule.Key]; exists {
			return fmt.Errorf("duplicate public demo schedule key %q", schedule.Key)
		}
		schedules[schedule.Key] = schedule
		startsOn := asOf.AddDate(0, 0, -schedule.StartsDaysAgo)
		var endsOn *time.Time
		if schedule.HasEnd {
			end := asOf.AddDate(0, 0, schedule.EndsDaysFromNow)
			endsOn = &end
		}
		input := servicework.ScheduleInput{
			AgreementID: "fixture", ServiceName: schedule.ServiceName,
			CadenceUnit: schedule.CadenceUnit, CadenceInterval: schedule.CadenceInterval,
			StartsOn: startsOn, EndsOn: endsOn,
		}
		if err := servicework.ValidateSchedule(input); err != nil {
			return fmt.Errorf("invalid public demo schedule %q: %w", schedule.Key, err)
		}
		if schedule.Active && agreement.Status == AgreementActive {
			count, err := servicework.ProjectedOccurrenceCount(input, asOf)
			if err != nil {
				return fmt.Errorf("project public demo schedule %q: %w", schedule.Key, err)
			}
			projectedOccurrences += count
			activeSchedules++
			activeCadences[schedule.CadenceUnit+"/"+strconv.Itoa(schedule.CadenceInterval)] = struct{}{}
		}
		if agreement.Status == AgreementActive {
			modeledActiveAgreements[schedule.AgreementKey] = true
		}
	}
	projectedOccurrences += len(f.NonActionableHistory)
	if activeSchedules != publicDemoActiveScheduleCount || projectedOccurrences == 0 || projectedOccurrences > publicDemoBaseOccurrenceCeiling {
		return fmt.Errorf("public demo fixture projects %d occurrences across %d active schedules", projectedOccurrences, activeSchedules)
	}
	for _, cadence := range []string{"week/1", "week/2", "month/1"} {
		if _, ok := activeCadences[cadence]; !ok {
			return fmt.Errorf("public demo fixture is missing %s recurrence", cadence)
		}
	}
	for key, agreement := range agreements {
		if agreement.Status == AgreementActive && agreement.VisitsEntitled > 0 {
			modeledActiveAgreements[key] = true
		}
		if agreement.Status == AgreementActive && !modeledActiveAgreements[key] {
			return fmt.Errorf("active public demo agreement %q has no modeled service commitment", key)
		}
	}

	recurringHistory := make(map[string]struct{}, len(f.RecurringOutcomes))
	for _, outcome := range f.RecurringOutcomes {
		schedule, ok := schedules[outcome.ScheduleKey]
		agreement := agreements[schedule.AgreementKey]
		if !ok || !schedule.Active || agreement.Status != AgreementActive ||
			(outcome.Status != "due" && outcome.Status != "skipped") || !strings.HasPrefix(outcome.Notes, "Fictional ") {
			return fmt.Errorf("invalid public demo recurring history for %q", outcome.ScheduleKey)
		}
		if _, exists := recurringHistory[outcome.ScheduleKey]; exists {
			return fmt.Errorf("duplicate public demo recurring history for %q", outcome.ScheduleKey)
		}
		recurringHistory[outcome.ScheduleKey] = struct{}{}
	}

	nonActionableHistory := make(map[string]struct{}, len(f.NonActionableHistory))
	for _, history := range f.NonActionableHistory {
		schedule, ok := schedules[history.ScheduleKey]
		agreement := agreements[schedule.AgreementKey]
		validStatus := history.Status == "due" || history.Status == "completed" || history.Status == "skipped"
		if !ok || schedule.Active && agreement.Status == AgreementActive || !validStatus ||
			history.DaysAgo <= 0 || history.DaysAgo > 30 || !strings.HasPrefix(history.Notes, "Fictional ") {
			return fmt.Errorf("invalid public demo non-actionable history for %q", history.ScheduleKey)
		}
		if _, exists := nonActionableHistory[history.ScheduleKey]; exists {
			return fmt.Errorf("duplicate public demo non-actionable history for %q", history.ScheduleKey)
		}
		nonActionableHistory[history.ScheduleKey] = struct{}{}
	}

	seasonalHistory := make(map[string]struct{}, len(f.SeasonalHistory))
	completedSeasonal, scheduledSeasonal := 0, 0
	for _, history := range f.SeasonalHistory {
		agreement, ok := agreements[history.AgreementKey]
		if !ok || agreement.Status != AgreementActive || agreement.VisitsEntitled == 0 ||
			(history.Outcome != "completed" && history.Outcome != "scheduled") {
			return fmt.Errorf("invalid public demo seasonal history for %q", history.AgreementKey)
		}
		if _, exists := seasonalHistory[history.AgreementKey]; exists {
			return fmt.Errorf("duplicate public demo seasonal history for %q", history.AgreementKey)
		}
		seasonalHistory[history.AgreementKey] = struct{}{}
		if history.Outcome == "completed" {
			completedSeasonal++
		} else {
			scheduledSeasonal++
		}
	}
	if completedSeasonal != 3 || scheduledSeasonal != 1 {
		return fmt.Errorf("public demo seasonal history distribution changed unexpectedly")
	}

	renewalHistory := make(map[string]struct{}, len(f.RenewalHistory))
	for _, history := range f.RenewalHistory {
		agreement, ok := agreements[history.AgreementKey]
		validMilestone := history.MilestoneDays == 90 || history.MilestoneDays == 60 ||
			history.MilestoneDays == 30 || history.MilestoneDays == 14 || history.MilestoneDays == 7
		key := history.AgreementKey + "/" + strconv.Itoa(history.MilestoneDays)
		if !ok || agreement.Status != AgreementActive || history.Status != "contacted" ||
			!validMilestone || !strings.HasPrefix(history.Notes, "Fictional ") {
			return fmt.Errorf("invalid public demo renewal history for %q", key)
		}
		if _, exists := renewalHistory[key]; exists {
			return fmt.Errorf("duplicate public demo renewal history for %q", key)
		}
		renewalHistory[key] = struct{}{}
	}
	return nil
}

func isReservedPublicDemoPhone(phone string) bool {
	var digits strings.Builder
	for _, character := range phone {
		if character >= '0' && character <= '9' {
			digits.WriteRune(character)
		}
	}
	normalized := digits.String()
	if len(normalized) != 10 || normalized[3:6] != "555" {
		return false
	}
	line, err := strconv.Atoi(normalized[6:])
	return err == nil && line >= 100 && line <= 199
}
