package jobs

import (
	"strings"
	"testing"
)

func TestValidateJobInput(t *testing.T) {
	valid := Input{CustomerID: "11111111-1111-4111-8111-111111111111", Kind: "job", Title: "  Repair faucet  "}
	for _, tc := range []struct {
		name   string
		change func(*Input)
		valid  bool
	}{
		{"unplanned job", func(*Input) {}, true},
		{"survey", func(in *Input) { in.Kind = "survey"; in.Date = "2028-02-29" }, true},
		{"invalid customer", func(in *Input) { in.CustomerID = "bad" }, false},
		{"invalid property", func(in *Input) { in.PropertyID = "bad" }, false},
		{"unknown kind", func(in *Input) { in.Kind = "annual" }, false},
		{"blank title", func(in *Input) { in.Title = "  " }, false},
		{"title limit", func(in *Input) { in.Title = strings.Repeat("a", 161) }, false},
		{"details limit", func(in *Input) { in.Details = strings.Repeat("a", 4001) }, false},
		{"null", func(in *Input) { in.Details = "bad\x00details" }, false},
		{"bad date", func(in *Input) { in.Date = "2026-02-29" }, false},
		{"zero year", func(in *Input) { in.Date = "0000-01-01" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := valid
			tc.change(&in)
			got, err := Validate(in)
			if (err == nil) != tc.valid {
				t.Fatalf("input=%+v error=%v", in, err)
			}
			if err == nil && got.Title != "Repair faucet" {
				t.Fatal("title not trimmed")
			}
		})
	}
}
