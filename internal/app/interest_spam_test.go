package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
)

func TestInterestSpamMatchesObservedCampaigns(t *testing.T) {
	tests := []struct {
		name, person, message, want string
	}{
		{"numeric 1", "NATREGTEGH2948352NERTYTRY", "MEYJTJ2948352MARETRYTR", "repeated_numeric_identifier"},
		{"numeric 2", "NAERTREGE1784856NEYRTHYT", "MERTHYTJTJ1784856MAERWETT", "repeated_numeric_identifier"},
		{"numeric 3", "NAYUYUTY1304427NEHTYHYHTR", "MERTHYTJTJ1304427MAVNGHJTH", "repeated_numeric_identifier"},
		{"numeric 4", "NAEWTRER1543722NERTHRTYHR", "MEYJTJ1543722MARETRYTR", "repeated_numeric_identifier"},
		{"bulk-message campaign one", "Andrew", "Send business messages using our service. Telegram https://t.me/example WhatsApp https://wa.me/123", "bulk_outreach_links"},
		{"bulk-message campaign two", "David", "We provide website outreach. Telegram https://t.me/example WhatsApp https://wa.me/123", "bulk_outreach_links"},
		{"GEO pitch", "Nathan", "Hi getsideyard.com. We offer a free GEO visibility check and SEO service. Would you like me to share it?", "unsolicited_marketing"},
		{"website design pitch", "Santiago", "Hi getsideyard.com. Our team provides website design and SEO. Would you like pricing options?", "unsolicited_marketing"},
		{"video pitch", "Joanna", "I just visited getsideyard.com. Our videos cost $195 for a 30-second video.", "unsolicited_marketing"},
		{"SEO packages pitch", "Aman", "Hello getsideyard.com. We can place your website on Google 1st page. I can send SEO packages.", "unsolicited_marketing"},
		{"index submission pitch", "Carlos", "Include getsideyard.com in Google's Search Index. List getsideyard.com now at this site.", "unsolicited_marketing"},
		{"accessibility pitch", "Sharma", "Can everyone use your website getsideyard.com? We conduct accessibility audits. Would you like a consultation?", "unsolicited_marketing"},
		{"SEO proposal pitch", "Jack", "I looked over your website. I have a brief SEO strategy and pricing proposal.", "unsolicited_marketing"},
		{"search marketing pitch", "Rosabel", "Hi getsideyard.com. We help businesses rank higher using SEO and GEO. May I send you a quote?", "unsolicited_marketing"},
		{"SEO plan pitch", "Anshika", "Hello getsideyard.com. I can improve Google rankings. I'll prepare a complete SEO plan.", "unsolicited_marketing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := interestSpamReason(interestSignup{Name: tc.person, Message: tc.message})
			if got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInterestSpamPreservesLegitimateInterest(t *testing.T) {
	tests := []struct{ name, trade, message string }{
		{"plumbing workflow", "Plumbing", "Scheduling repeat plumbing calls and invoices is difficult."},
		{"blank trade allowed", "", "I own a cleaning business and need a way to track recurring jobs."},
		{"SEO job management", "", "I do SEO work and need help scheduling client projects."},
		{"website mentioned without solicitation", "", "Would getsideyard.com help manage our existing website SEO work?"},
		{"chat platform usage", "", "I use Telegram and WhatsApp to coordinate technicians."},
		{"unrelated number", "", "We do about 1234567 visits a year; reminders would help."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := interestSpamReason(interestSignup{Name: "Taylor", Trade: tc.trade, Message: tc.message}); got != "" {
				t.Fatalf("unexpected spam reason %q", got)
			}
		})
	}
}

func TestInterestSpamSuppressedWithoutPersistence(t *testing.T) {
	tests := []struct {
		name, person, message string
		wantSaves int
	}{
		{"marketing spam", "Taylor", "Hi getsideyard.com. We offer SEO services. May I send you a quote?", 0},
		{"random identifier", "NATREGTEGH2948352NERTYTRY", "MEYJTJ2948352MARETRYTR", 0},
		{"legitimate interest", "Taylor", "Keeping track of my weekly jobs.", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeInterestRecorder{}
			s := interestTestServer(fake)
			values := url.Values{
				"name": {tc.person}, "email": {"taylor@example.com"},
				"csrf_token": {"test-csrf-token"}, "message": {tc.message},
			}
			w := httptest.NewRecorder()
			s.handler(fstest.MapFS{}).ServeHTTP(w, interestRequest(values))
			if w.Code != http.StatusSeeOther || len(fake.inputs) != tc.wantSaves {
				t.Fatalf("status=%d saves=%d; want status=303 saves=%d", w.Code, len(fake.inputs), tc.wantSaves)
			}
			if loc := w.Header().Get("Location"); loc != "/interest/thanks" {
				t.Fatalf("redirect to %q, want /interest/thanks", loc)
			}
		})
	}
}

func TestInterestSpamStillValidatesInput(t *testing.T) {
	fake := &fakeInterestRecorder{}
	s := interestTestServer(fake)
	values := url.Values{
		"name": {"Taylor"}, "email": {"invalid"},
		"csrf_token": {"test-csrf-token"},
		"message": {"Hi getsideyard.com. We offer SEO services. May I send you a quote?"},
	}
	w := httptest.NewRecorder()
	s.handler(fstest.MapFS{}).ServeHTTP(w, interestRequest(values))
	if w.Code != http.StatusUnprocessableEntity || len(fake.inputs) != 0 ||
		!strings.Contains(w.Body.String(), "valid email") {
		t.Fatalf("invalid spam submission response status=%d", w.Code)
	}
}
