package sdk

import (
	"testing"
	"time"
)

func TestSeverityOrdering(t *testing.T) {
	if !SeverityCritical.AtLeast(SeverityHigh) || SeverityMedium.AtLeast(SeverityHigh) {
		t.Error("ordering broken")
	}
	if SeverityNone.AtLeast(SeverityLow) || SeverityNone.AtLeast(SeverityNone) {
		t.Error("none must never trip a gate")
	}
	if !SeverityUnknown.AtLeast(SeverityUnknown) || SeverityUnknown.AtLeast(SeverityLow) {
		t.Error("unknown ranks between none and low")
	}
	if Severity("").Normalize() != SeverityUnknown || SeverityHigh.Normalize() != SeverityHigh {
		t.Error("normalize")
	}
	if MaxSeverity() != SeverityUnknown || MaxSeverity(SeverityLow, "", SeverityHigh, SeverityMedium) != SeverityHigh {
		t.Error("max")
	}
	if MaxSeverity(SeverityNone) != SeverityNone {
		t.Error("max of single none")
	}
	if len(AllSeverities) != 6 {
		t.Error("all severities")
	}
}

func TestParseSeverity(t *testing.T) {
	cases := map[string]Severity{
		"CRITICAL": SeverityCritical, "important": SeverityHigh, "High": SeverityHigh,
		"moderate": SeverityMedium, "medium": SeverityMedium, "low": SeverityLow,
		"": SeverityUnknown, "unknown": SeverityUnknown, "info": SeverityNone, "none": SeverityNone, "note": SeverityNone,
	}
	for in, want := range cases {
		got, err := ParseSeverity(in)
		if err != nil || got != want {
			t.Errorf("ParseSeverity(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseSeverity("fast"); err == nil {
		t.Error("expected error")
	}
	var s Severity
	if err := s.UnmarshalText([]byte("high")); err != nil || s != SeverityHigh {
		t.Error("unmarshal")
	}
	if err := s.UnmarshalText([]byte("bogus")); err == nil {
		t.Error("unmarshal bogus")
	}
}

func TestSeverityFromCVSS(t *testing.T) {
	cases := map[float64]Severity{9.8: SeverityCritical, 9.0: SeverityCritical, 7.5: SeverityHigh, 5.3: SeverityMedium, 2.1: SeverityLow, 0: SeverityNone}
	for score, want := range cases {
		if got := SeverityFromCVSS(score); got != want {
			t.Errorf("CVSS %v = %v want %v", score, got, want)
		}
	}
}

func TestClocksAndDurations(t *testing.T) {
	fixed := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	if (FixedClock{T: fixed}).Now() != fixed {
		t.Error("fixed clock")
	}
	if SystemClock.Now(SystemClock{}).Location() != time.UTC {
		t.Error("system clock must be UTC")
	}
	var d Duration
	if err := d.UnmarshalText([]byte("1m30s")); err != nil || d.Std() != 90*time.Second {
		t.Errorf("duration: %v %v", d, err)
	}
	if b, _ := d.MarshalText(); string(b) != "1m30s" {
		t.Errorf("marshal duration: %s", b)
	}
	if err := d.UnmarshalText([]byte("fast")); err == nil {
		t.Error("bad duration")
	}
	var date Date
	if err := date.UnmarshalText([]byte("2027-01-01")); err != nil || date.Year() != 2027 {
		t.Errorf("date: %v %v", date, err)
	}
	if b, _ := date.MarshalText(); string(b) != "2027-01-01" {
		t.Errorf("marshal date: %s", b)
	}
	if err := date.UnmarshalText([]byte("01/01/2027")); err == nil {
		t.Error("bad date")
	}
}
