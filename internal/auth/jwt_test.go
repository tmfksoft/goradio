package auth

import (
	"context"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	secret := []byte("test-secret")

	token, err := Sign(secret, []string{"station-a", "station-b"}, nil, "tester", time.Hour, false)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	claims, err := Verify(secret, token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if !claims.HasSlug("station-a") {
		t.Error("expected claims to authorize station-a")
	}
	if !claims.HasSlug("station-b") {
		t.Error("expected claims to authorize station-b")
	}
	if claims.HasSlug("station-c") {
		t.Error("did not expect claims to authorize station-c")
	}
	if claims.ReadOnly {
		t.Error("expected claims to not be read-only")
	}
}

func TestHasSlugWildcard(t *testing.T) {
	all := &Claims{Slugs: []string{"*"}}
	if !all.HasSlug("station-a") {
		t.Error("expected \"*\" to authorize any slug")
	}
	if !all.HasSlug("anything-else") {
		t.Error("expected \"*\" to authorize any slug")
	}

	prefix := &Claims{Slugs: []string{"test-*"}}
	if !prefix.HasSlug("test-a") {
		t.Error("expected \"test-*\" to authorize test-a")
	}
	if prefix.HasSlug("station-a") {
		t.Error("did not expect \"test-*\" to authorize station-a")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	token, err := Sign([]byte("secret-a"), []string{"station-a"}, nil, "tester", time.Hour, false)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := Verify([]byte("secret-b"), token); err == nil {
		t.Error("expected Verify to fail with the wrong secret")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	secret := []byte("test-secret")

	token, err := Sign(secret, []string{"station-a"}, nil, "tester", -time.Hour, false)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := Verify(secret, token); err == nil {
		t.Error("expected Verify to fail for an expired token")
	}
}

func TestHasDirUnrestrictedByDefault(t *testing.T) {
	c := &Claims{} // no Dirs at all -- the pre-this-feature default
	if !c.HasDir("GTASA/KROSE") {
		t.Error("expected empty Dirs to authorize any directory")
	}
	if !c.HasDir("") {
		t.Error("expected empty Dirs to authorize the root")
	}
}

func TestHasDirRecursiveContainment(t *testing.T) {
	c := &Claims{Dirs: []string{"GTASA/KROSE"}}

	if !c.HasDir("GTASA/KROSE") {
		t.Error("expected exact match to be authorized")
	}
	if !c.HasDir("GTASA/KROSE/song.ogg") {
		t.Error("expected a file inside the allowed directory to be authorized")
	}
	if !c.HasDir("GTASA/KROSE/Adverts/ad.ogg") {
		t.Error("expected a nested subdirectory's contents to be authorized")
	}
	if c.HasDir("GTASA/RadioX") {
		t.Error("did not expect a sibling directory to be authorized")
	}
	if c.HasDir("GTASA/KROSE-other") {
		t.Error("did not expect a same-prefix-but-different directory to be authorized (no separator boundary)")
	}
	if c.HasDir("GTASA") {
		t.Error("did not expect the parent of an allowed directory to itself be authorized")
	}
}

func TestHasDirGlob(t *testing.T) {
	c := &Claims{Dirs: []string{"GTASA/*"}}
	if !c.HasDir("GTASA/KROSE") {
		t.Error("expected \"GTASA/*\" to authorize GTASA/KROSE")
	}
	if c.HasDir("GTAVC/Emotion") {
		t.Error("did not expect \"GTASA/*\" to authorize a different top-level game dir")
	}
}

// The root/ancestor edge case flagged in the design as the one most
// likely to be gotten wrong: CanBrowse("") must be true whenever anything
// at all is allowed, even though HasDir("") on its own is false for a
// restricted token -- otherwise a scoped token could never list the root
// to discover the one subdirectory it does have.
func TestCanBrowseAncestorPath(t *testing.T) {
	c := &Claims{Dirs: []string{"GTASA/KROSE"}}

	if !c.CanBrowse("") {
		t.Error("expected the root to be browsable when something is allowed under it")
	}
	if !c.CanBrowse("GTASA") {
		t.Error("expected GTASA to be browsable as an ancestor of GTASA/KROSE")
	}
	if !c.CanBrowse("GTASA/KROSE") {
		t.Error("expected the allowed directory itself to be browsable")
	}
	if c.CanBrowse("GTAVC") {
		t.Error("did not expect an unrelated top-level directory to be browsable")
	}
	if c.CanBrowse("GTASA/RadioX") {
		t.Error("did not expect a sibling of the allowed directory to be browsable")
	}
}

func TestRequireWrite(t *testing.T) {
	readOnly := &Claims{ReadOnly: true}
	readWrite := &Claims{ReadOnly: false}

	if err := RequireWrite(withClaims(context.Background(), readOnly)); err == nil {
		t.Error("expected RequireWrite to reject a read-only token")
	}
	if err := RequireWrite(withClaims(context.Background(), readWrite)); err != nil {
		t.Errorf("expected RequireWrite to accept a read-write token, got: %v", err)
	}
	if err := RequireWrite(context.Background()); err == nil {
		t.Error("expected RequireWrite to reject a context with no claims")
	}
}
