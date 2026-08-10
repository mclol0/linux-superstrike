package main

import (
	"testing"

	"superstrike/internal/hidpp"
)

func TestResolveActiveProfileSectorPrefersReportedSector(t *testing.T) {
	profiles := []hidpp.Profile{{Sector: 1, Enabled: true}, {Sector: 2, Enabled: true}}
	if got := resolveActiveProfileSector(2, 1, profiles); got != 2 {
		t.Fatalf("got sector %d, want reported sector 2", got)
	}
}

func TestResolveActiveProfileSectorKeepsRememberedSectorInHostMode(t *testing.T) {
	profiles := []hidpp.Profile{{Sector: 1, Enabled: true}, {Sector: 2, Enabled: true}}
	if got := resolveActiveProfileSector(0, 2, profiles); got != 2 {
		t.Fatalf("got sector %d, want remembered sector 2", got)
	}
}

func TestResolveActiveProfileSectorFallsBackToFirstEnabled(t *testing.T) {
	profiles := []hidpp.Profile{{Sector: 1, Enabled: false}, {Sector: 2, Enabled: true}}
	if got := resolveActiveProfileSector(0, 9, profiles); got != 2 {
		t.Fatalf("got sector %d, want first enabled sector 2", got)
	}
}
