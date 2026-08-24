package cli

import (
	"flag"
	"fmt"
	"time"

	"github.com/goradioserver/goradio/internal/auth"
)

// TokenGen implements `radio tokengen [-secret ...] [-subject ...] [-ttl ...] [-readonly] <slug...>`.
func TokenGen(args []string) error {
	fs := flag.NewFlagSet("tokengen", flag.ContinueOnError)
	secret := fs.String("secret", "", "HS256 signing secret (required; must match the audio server's auth.jwt_secret)")
	subject := fs.String("subject", "tokengen", "JWT subject claim")
	ttl := fs.Duration("ttl", 24*time.Hour, "token time-to-live")
	readOnly := fs.Bool("readonly", false, "mint a read-only token: GetStatus/SubscribeEvents only, every write RPC is rejected")
	if err := fs.Parse(args); err != nil {
		return err
	}

	slugs := fs.Args()
	if len(slugs) == 0 {
		return fmt.Errorf("at least one station slug is required, e.g. radio tokengen -secret ... myfm otherfm (glob patterns like '*' or 'test-*' are also accepted)")
	}
	if *secret == "" {
		return fmt.Errorf("-secret is required")
	}

	token, err := auth.Sign([]byte(*secret), slugs, *subject, *ttl, *readOnly)
	if err != nil {
		return fmt.Errorf("sign token: %w", err)
	}

	fmt.Println(token)
	return nil
}
