package main

import (
	"testing"
	"time"
)

// ── parseAmount ───────────────────────────────────────────────────────────────

func TestParseAmount(t *testing.T) {
	tests := []struct {
		input   string
		want    float64
		wantErr bool
	}{
		{"100.00", 100.00, false},
		{"-50.00", -50.00, false},
		{"0", 0, false},
		{"£100.00", 100.00, false},
		{"100.00 GBP", 100.00, false},
		{"-£1,234.56", -1234.56, false},
		{"1,000.00", 1000.00, false},
		{"", 0, false},
		{"  42.50  ", 42.50, false},
	}

	for _, tc := range tests {
		got, err := parseAmount(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseAmount(%q) = %v, want error", tc.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseAmount(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseAmount(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// ── parseMultiperiodCSV ───────────────────────────────────────────────────────

func TestParseMultiperiodCSV(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		// hledger -M -O csv -N output: header row + data rows
		csv := `"account","2026-01-01..2026-02-01","2026-02-01..2026-03-01"
"expenses:food","150.00","120.00"
"expenses:transport","50.00","45.00"
`
		result, err := parseMultiperiodCSV(csv)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["2026-01"] != 200.00 {
			t.Errorf("2026-01 = %v, want 200.00", result["2026-01"])
		}
		if result["2026-02"] != 165.00 {
			t.Errorf("2026-02 = %v, want 165.00", result["2026-02"])
		}
	})

	t.Run("empty output", func(t *testing.T) {
		result, err := parseMultiperiodCSV("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 0 {
			t.Errorf("expected empty map, got %v", result)
		}
	})

	t.Run("single account row", func(t *testing.T) {
		csv := `"account","2024-01-01..2024-02-01"
"income:salary","-3000.00"
`
		result, err := parseMultiperiodCSV(csv)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["2024-01"] != -3000.00 {
			t.Errorf("2024-01 = %v, want -3000.00", result["2024-01"])
		}
	})
}

// ── daysInMonth ───────────────────────────────────────────────────────────────

func TestDaysInMonth(t *testing.T) {
	tests := []struct {
		year  int
		month time.Month
		want  int
	}{
		{2024, time.February, 29}, // leap year
		{2023, time.February, 28}, // non-leap year
		{2024, time.January, 31},
		{2024, time.April, 30},
		{2024, time.December, 31},
	}

	for _, tc := range tests {
		got := daysInMonth(tc.year, tc.month)
		if got != tc.want {
			t.Errorf("daysInMonth(%d, %s) = %d, want %d", tc.year, tc.month, got, tc.want)
		}
	}
}

// ── normalisePeriod ───────────────────────────────────────────────────────────

func TestNormalisePeriod(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2026-01-01..2026-02-01", "2026-01"},
		{"2026-01", "2026-01"},
		{"Jan 2026", "Jan 2026"}, // no YYYY-MM match → unchanged
	}

	for _, tc := range tests {
		got := normalisePeriod(tc.input)
		if got != tc.want {
			t.Errorf("normalisePeriod(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
