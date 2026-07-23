package clickhouse

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The missing-database guard runs before any network I/O, so these need no
// server: a DSN naming no database would silently land in ClickHouse's
// `default` and must be rejected at boot instead.
func TestNewClickHouseEngineRejectsMissingDatabase(t *testing.T) {
	for name, dsn := range map[string]string{
		"no database":    "clickhouse://user:password@localhost:9000",
		"trailing slash": "clickhouse://user:password@localhost:9000/",
		"unparseable":    "://not-a-dsn",
	} {
		t.Run(name, func(t *testing.T) {
			engine, err := NewClickHouseEngine(context.Background(), dsn)
			require.Error(t, err)
			require.Nil(t, engine)
		})
	}
}
