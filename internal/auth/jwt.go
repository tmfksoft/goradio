// Package auth handles JWT signing/verification and slug-scoped
// authorization for the audio server's gRPC control plane.
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT payload used across GoRadio: the bearer may control any
// station slug listed in Slugs.
type Claims struct {
	jwt.RegisteredClaims
	Slugs []string `json:"slugs"`
}

// Sign mints an HS256 JWT authorizing the given slugs.
func Sign(secret []byte, slugs []string, subject string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		Slugs: slugs,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// Verify checks the token's signature and expiry and returns its claims.
func Verify(secret []byte, tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// HasSlug reports whether the claims authorize the given station slug.
func (c *Claims) HasSlug(slug string) bool {
	for _, s := range c.Slugs {
		if s == slug {
			return true
		}
	}
	return false
}
