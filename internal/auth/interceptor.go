package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"
)

type claimsContextKey struct{}

// NewInterceptor returns a connect.Interceptor that verifies the JWT
// bearer token on every inbound RPC (unary and streaming alike) and
// attaches its claims to the request context. It only authenticates;
// per-slug authorization is left to each handler via RequireSlug.
//
// The same interceptor covers all three protocols the handler speaks
// (gRPC, gRPC-Web and Connect): each carries the token in a plain
// "Authorization" HTTP header, so unlike the grpc-go interceptors this
// replaced, there's no metadata type to go through.
func NewInterceptor(secret []byte) connect.Interceptor {
	return &interceptor{secret: secret}
}

type interceptor struct {
	secret []byte
}

func (i *interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		// WrapUnary covers outbound client calls as well as inbound
		// server ones; only the latter carry a token to verify.
		if req.Spec().IsClient {
			return next(ctx, req)
		}
		claims, err := authenticate(req.Header(), i.secret)
		if err != nil {
			return nil, err
		}
		return next(withClaims(ctx, claims), req)
	}
}

// WrapStreamingClient is a no-op: this process only ever serves streams,
// it doesn't open them as a Connect client (the station controller dials
// with grpc-go -- see internal/luastation/engine.go).
func (i *interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		claims, err := authenticate(conn.RequestHeader(), i.secret)
		if err != nil {
			return err
		}
		return next(withClaims(ctx, claims), conn)
	}
}

func authenticate(header http.Header, secret []byte) (*Claims, error) {
	value := header.Get("Authorization")
	if value == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing authorization header"))
	}

	token := strings.TrimPrefix(value, "Bearer ")
	claims, err := Verify(secret, token)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("invalid token: %w", err))
	}
	return claims, nil
}

func withClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey{}, claims)
}

// FromContext retrieves the JWT claims attached by the auth interceptor.
func FromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsContextKey{}).(*Claims)
	return claims, ok
}

// RequireSlug checks that the caller's claims (attached by the auth
// interceptor) authorize the given station slug, returning a
// CodePermissionDenied error if not.
func RequireSlug(ctx context.Context, slug string) error {
	claims, ok := FromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("no claims in context"))
	}
	if !claims.HasSlug(slug) {
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("token not authorized for slug %q", slug))
	}
	return nil
}

// RequireWrite checks that the caller's claims (attached by the auth
// interceptor) are not read-only, returning CodePermissionDenied if
// they are. Every write RPC (RegisterStation, UnregisterStation,
// QueueTrack, RemoveFromQueue, ClearQueue, Skip, SkipTo, Pause, Resume,
// Seek, SeekBy) calls this in addition to RequireSlug; the read-only
// RPCs (GetStatus, ListStations, SubscribeEvents, GetServerInfo,
// ListDirectory) don't, since a read-only token is exactly meant to
// allow those.
func RequireWrite(ctx context.Context) error {
	claims, ok := FromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("no claims in context"))
	}
	if claims.ReadOnly {
		return connect.NewError(connect.CodePermissionDenied, errors.New("token is read-only"))
	}
	return nil
}
