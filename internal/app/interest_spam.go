package app

import "strings"

// interestSpamReason identifies high-confidence unsolicited submissions.
// A missing trade alone is never sufficient: trade is optional on this form.
func interestSpamReason(in interestSignup) string {
	if repeatedInterestNumber(in.Name, in.Message) {
		return "repeated_numeric_identifier"
	}

	message := strings.ToLower(in.Message)
	// Both outbound chat destinations occur in the same repeated campaign.
	if strings.Contains(message, "t.me/") && strings.Contains(message, "wa.me/") {
		return "bulk_outreach_links"
	}

	// Require website targeting, a sales category, and a solicitation. This
	// avoids rejecting contractors who genuinely ask for help organizing
	// marketing or website work.
	if !interestContainsAny(message, "getsideyard.com", "your website", "your business") ||
		!interestContainsAny(message, "seo", "search engine", "search index", "google rankings", "google 1st page", "geo visibility", "ai search visibility", "website design", "website redesign", "digital marketing", "website outreach", "accessibility audit", "accessibility-compliant", "video") ||
		!interestContainsAny(message, "we offer", "we provide", "our team", "our videos", "would you", "may i", "i can", "i'll", "pricing", "price list", "proposal", "send you", "quote", "packages", "list getsideyard.com", "we help businesses") {
		return ""
	}
	return "unsolicited_marketing"
}

func repeatedInterestNumber(name, message string) bool {
	for i := 0; i < len(name); {
		if name[i] < '0' || name[i] > '9' {
			i++
			continue
		}
		start := i
		for i < len(name) && name[i] >= '0' && name[i] <= '9' {
			i++
		}
		if i-start >= 7 && strings.Contains(message, name[start:i]) {
			return true
		}
	}
	return false
}

func interestContainsAny(message string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(message, value) {
			return true
		}
	}
	return false
}
