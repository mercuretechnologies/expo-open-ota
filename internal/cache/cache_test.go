package cache

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseSentinelAddrsTrimsWhitespaceAndDropsEmptyEntries(t *testing.T) {
	got := parseSentinelAddrs(" sentinel-0:26379, sentinel-1:26379,,\t sentinel-2:26379 , ")
	want := []string{"sentinel-0:26379", "sentinel-1:26379", "sentinel-2:26379"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseSentinelAddrs() = %#v, want %#v", got, want)
	}
}

func TestParseSentinelAddrsReturnsEmptyForBlankInput(t *testing.T) {
	got := parseSentinelAddrs(" , \t,")

	if len(got) != 0 {
		t.Fatalf("parseSentinelAddrs() = %#v, want empty", got)
	}
}

// Two Gets racing on an expired key used to delete under the read lock: a
// concurrent map read+write, which the runtime kills unrecoverably. Run with
// -race; the old code fails the detector, the write-lock upgrade is clean.
func TestLocalCacheConcurrentExpiredGets(t *testing.T) {
	c := NewLocalCache()
	zero := 0
	require.NoError(t, c.Set("expired-key", "value", &zero))
	time.Sleep(5 * time.Millisecond)

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Get("expired-key")
		}()
	}
	wg.Wait()
	require.Equal(t, "", c.Get("expired-key"))
}
