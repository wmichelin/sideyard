package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestMalformedRecordRequestsDoNotReadDatabaseOrReportOutage(t *testing.T) {
	// A nil database makes any accidental lookup fail this boundary test.
	s := &Server{store: &Store{}}
	const shop = "12345678-1234-1234-1234-123456789012"
	for _, tc := range []struct {
		name, method, id string
		form             url.Values
		handler          http.HandlerFunc
		status           int
	}{
		{"equipment", "GET", shop + "suffix", nil, s.handleEquipmentRecord, 404},
		{"address", "GET", shop + " ", nil, s.handlePropertyAddress, 404},
		{"vehicle", "POST", shop, url.Values{"vehicle_id": {shop + "suffix"}, "name": {"Truck"}, "revision": {"1"}}, s.handleCustomerVehicle, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/record", strings.NewReader(tc.form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetPathValue("id", tc.id)
			r = r.WithContext(contextWithUser(context.Background(), &User{ShopID: shop, Role: "owner"}))
			w := httptest.NewRecorder()
			tc.handler(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d, want %d", w.Code, tc.status)
			}
		})
	}
}
