package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

const usage = `usage: migrate <command> [args]

commands:
  up                  apply all pending migrations
  down [n]            roll back n migrations (default: all)
  redo                down 1 then up 1
  version             show current schema version
  force <version>     force a version (clears the dirty flag)
  drop                drop everything in the database

env:
  DATABASE_URL        required, e.g. postgres://user:pass@host:5432/db?sslmode=disable
  MIGRATIONS_PATH     optional, default file://migrations
`

func main() {
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	source := os.Getenv("MIGRATIONS_PATH")
	if source == "" {
		source = "file://migrations"
	}

	m, err := migrate.New(source, dbURL)
	if err != nil {
		log.Fatalf("migrate init: %v", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			log.Printf("close source: %v", srcErr)
		}
		if dbErr != nil {
			log.Printf("close database: %v", dbErr)
		}
	}()

	switch args[0] {
	case "up":
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			log.Fatalf("up: %v", err)
		}
		printVersion(m, "up")

	case "down":
		if len(args) >= 2 {
			n, err := strconv.Atoi(args[1])
			if err != nil || n <= 0 {
				log.Fatalf("down: n must be a positive integer")
			}
			if err := m.Steps(-n); err != nil && !errors.Is(err, migrate.ErrNoChange) {
				log.Fatalf("down %d: %v", n, err)
			}
		} else {
			if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
				log.Fatalf("down: %v", err)
			}
		}
		printVersion(m, "down")

	case "redo":
		if err := m.Steps(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			log.Fatalf("redo down: %v", err)
		}
		if err := m.Steps(1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			log.Fatalf("redo up: %v", err)
		}
		printVersion(m, "redo")

	case "version":
		printVersion(m, "version")

	case "force":
		if len(args) < 2 {
			log.Fatal("force: missing <version> argument")
		}
		v, err := strconv.Atoi(args[1])
		if err != nil {
			log.Fatalf("force: invalid version: %v", err)
		}
		if err := m.Force(v); err != nil {
			log.Fatalf("force: %v", err)
		}
		printVersion(m, "force")

	case "drop":
		if err := m.Drop(); err != nil {
			log.Fatalf("drop: %v", err)
		}
		fmt.Println("dropped")

	default:
		flag.Usage()
		os.Exit(2)
	}
}

func printVersion(m *migrate.Migrate, op string) {
	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		fmt.Printf("%s: no migrations applied\n", op)
		return
	}
	if err != nil {
		log.Fatalf("version: %v", err)
	}
	fmt.Printf("%s: version=%d dirty=%t\n", op, v, dirty)
}
