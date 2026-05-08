// cmd/seed — small CLI for development.
//
//   go run ./cmd/seed grant-admin <email> [super|admin|support]
//
// Promotes the user matching <email> to a platform admin with the given role
// (defaults to "super"). Creates the platform_admins row if it doesn't exist.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/example/gmcauditor/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://gmc:gmc@localhost:5432/gmcauditor?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()
	st := store.NewStore(pool)

	switch os.Args[1] {
	case "grant-admin":
		if len(os.Args) < 3 {
			usage()
		}
		email := os.Args[2]
		role := "super"
		if len(os.Args) >= 4 {
			role = os.Args[3]
		}
		u, err := st.Users.GetByEmail(ctx, pool, email)
		if err != nil {
			log.Fatalf("user not found: %s: %v", email, err)
		}
		a, err := st.PlatformAdmins.Grant(ctx, pool, u.ID, role)
		if err != nil {
			log.Fatalf("grant: %v", err)
		}
		fmt.Printf("granted %s = %s (id=%s)\n", email, a.Role, a.ID)
	case "all":
		if err := seedAll(ctx, pool); err != nil {
			log.Fatalf("seed all: %v", err)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  seed grant-admin <email> [super|admin|support]")
	fmt.Fprintln(os.Stderr, "  seed all      # populate dev DB with realistic fixture data")
	os.Exit(2)
}
