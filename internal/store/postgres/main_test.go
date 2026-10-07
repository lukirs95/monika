//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/lukirs95/monika/internal/store/postgres/pgtest"
)

var server *pgtest.Server

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	var err error
	server, err = pgtest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer server.Close()
	return m.Run()
}
