// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import "testing"

func TestNewRegistryIncludesHDB(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := registry.Lookup("HDB"); !ok {
		t.Fatal("expected HDB definition to be registered")
	}
	if _, ok := registry.Lookup("MTV"); !ok {
		t.Fatal("expected MTV definition to be registered")
	}
	if _, ok := registry.Lookup("ANT"); !ok {
		t.Fatal("expected ANT definition to be registered")
	}
	if _, ok := registry.Lookup("AR"); !ok {
		t.Fatal("expected AR definition to be registered")
	}
	if _, ok := registry.Lookup("ASC"); !ok {
		t.Fatal("expected ASC definition to be registered")
	}
	if _, ok := registry.Lookup("BHD"); !ok {
		t.Fatal("expected BHD definition to be registered")
	}
	if _, ok := registry.Lookup("BHDTV"); !ok {
		t.Fatal("expected BHDTV definition to be registered")
	}
	if _, ok := registry.Lookup("BJS"); !ok {
		t.Fatal("expected BJS definition to be registered")
	}
	if _, ok := registry.Lookup("BT"); !ok {
		t.Fatal("expected BT definition to be registered")
	}
	if _, ok := registry.Lookup("DC"); !ok {
		t.Fatal("expected DC definition to be registered")
	}
	if _, ok := registry.Lookup("FF"); !ok {
		t.Fatal("expected FF definition to be registered")
	}
	if _, ok := registry.Lookup("FL"); !ok {
		t.Fatal("expected FL definition to be registered")
	}
	if _, ok := registry.Lookup("GPW"); !ok {
		t.Fatal("expected GPW definition to be registered")
	}
	if _, ok := registry.Lookup("ACM"); !ok {
		t.Fatal("expected ACM definition to be registered")
	}
	if _, ok := registry.Lookup("HDS"); !ok {
		t.Fatal("expected HDS definition to be registered")
	}
	if _, ok := registry.Lookup("HDT"); !ok {
		t.Fatal("expected HDT definition to be registered")
	}
	if _, ok := registry.Lookup("IS"); !ok {
		t.Fatal("expected IS definition to be registered")
	}
	if _, ok := registry.Lookup("NBL"); !ok {
		t.Fatal("expected NBL definition to be registered")
	}
	if _, ok := registry.Lookup("PTS"); !ok {
		t.Fatal("expected PTS definition to be registered")
	}
	if _, ok := registry.Lookup("RTF"); !ok {
		t.Fatal("expected RTF definition to be registered")
	}
	if _, ok := registry.Lookup("SPD"); !ok {
		t.Fatal("expected SPD definition to be registered")
	}
	if _, ok := registry.Lookup("THR"); !ok {
		t.Fatal("expected THR definition to be registered")
	}
	if _, ok := registry.Lookup("TL"); !ok {
		t.Fatal("expected TL definition to be registered")
	}
	if _, ok := registry.Lookup("TVC"); !ok {
		t.Fatal("expected TVC definition to be registered")
	}
	if _, ok := registry.Lookup("FLD"); !ok {
		t.Fatal("expected FLD definition to be registered")
	}
	if _, ok := registry.Lookup("AZ"); !ok {
		t.Fatal("expected AZ definition to be registered")
	}
	if _, ok := registry.Lookup("CZ"); !ok {
		t.Fatal("expected CZ definition to be registered")
	}
	if _, ok := registry.Lookup("PHD"); !ok {
		t.Fatal("expected PHD definition to be registered")
	}
}
