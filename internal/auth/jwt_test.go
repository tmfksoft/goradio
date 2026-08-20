package auth

import (
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	secret := []byte("test-secret")

	token, err := Sign(secret, []string{"station-a", "station-b"}, "tester", time.Hour)
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
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	token, err := Sign([]byte("secret-a"), []string{"station-a"}, "tester", time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := Verify([]byte("secret-b"), token); err == nil {
		t.Error("expected Verify to fail with the wrong secret")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	secret := []byte("test-secret")

	token, err := Sign(secret, []string{"station-a"}, "tester", -time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := Verify(secret, token); err == nil {
		t.Error("expected Verify to fail for an expired token")
	}
}
