package auth

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type claimsContextKey struct{}

// UnaryServerInterceptor verifies the JWT bearer token on every unary RPC
// and attaches its claims to the request context. It only authenticates;
// per-slug authorization is left to each handler via RequireSlug.
func UnaryServerInterceptor(secret []byte) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		claims, err := authenticate(ctx, secret)
		if err != nil {
			return nil, err
		}
		return handler(withClaims(ctx, claims), req)
	}
}

// StreamServerInterceptor is the streaming-RPC equivalent of
// UnaryServerInterceptor.
func StreamServerInterceptor(secret []byte) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		claims, err := authenticate(ss.Context(), secret)
		if err != nil {
			return err
		}
		return handler(srv, &authenticatedStream{ServerStream: ss, ctx: withClaims(ss.Context(), claims)})
	}
}

type authenticatedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authenticatedStream) Context() context.Context { return s.ctx }

func authenticate(ctx context.Context, secret []byte) (*Claims, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}

	values := md.Get("authorization")
	if len(values) == 0 {
		return nil, status.Error(codes.Unauthenticated, "missing authorization header")
	}

	token := strings.TrimPrefix(values[0], "Bearer ")
	claims, err := Verify(secret, token)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid token: %v", err)
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
// codes.PermissionDenied error if not.
func RequireSlug(ctx context.Context, slug string) error {
	claims, ok := FromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "no claims in context")
	}
	if !claims.HasSlug(slug) {
		return status.Errorf(codes.PermissionDenied, "token not authorized for slug %q", slug)
	}
	return nil
}

// RequireWrite checks that the caller's claims (attached by the auth
// interceptor) are not read-only, returning codes.PermissionDenied if
// they are. Every write RPC (RegisterStation, UnregisterStation,
// QueueTrack, RemoveFromQueue, ClearQueue, Skip, SkipTo) calls this in
// addition to RequireSlug; GetStatus and SubscribeEvents don't, since a
// read-only token is exactly meant to allow those.
func RequireWrite(ctx context.Context) error {
	claims, ok := FromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "no claims in context")
	}
	if claims.ReadOnly {
		return status.Error(codes.PermissionDenied, "token is read-only")
	}
	return nil
}
