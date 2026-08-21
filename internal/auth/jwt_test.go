package auth

import (
	"context"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	secret := []byte("test-secret")

	token, err := Sign(secret, []string{"station-a", "station-b"}, "tester", time.Hour, false)
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

func TestVerifyRejectsWrongSecret(t *testing.T) {
	token, err := Sign([]byte("secret-a"), []string{"station-a"}, "tester", time.Hour, false)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := Verify([]byte("secret-b"), token); err == nil {
		t.Error("expected Verify to fail with the wrong secret")
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	secret := []byte("test-secret")

	token, err := Sign(secret, []string{"station-a"}, "tester", -time.Hour, false)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := Verify(secret, token); err == nil {
		t.Error("expected Verify to fail for an expired token")
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
