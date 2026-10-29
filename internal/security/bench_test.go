package security

import (
	"fmt"
	"testing"
)

// BenchmarkRateLimiterAllowExisting measures the steady-state path for a tracked client.
func BenchmarkRateLimiterAllowExisting(b *testing.B) {
	limiter := NewRateLimiter(100, true)
	limiter.SetMaxClients(10000)
	limiter.Allow("192.0.2.1")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		limiter.Allow("192.0.2.1")
	}
}

// BenchmarkRateLimiterSaturatedNewClient measures the eviction path when the tracked-client
// table is full and every connection comes from a previously unseen source IP. A bounded
// table must not degrade to a linear scan of every tracked client per connection.
func BenchmarkRateLimiterSaturatedNewClient(b *testing.B) {
	const maxClients = 10000

	limiter := NewRateLimiter(100, true)
	limiter.SetMaxClients(maxClients)

	for i := 0; i < maxClients; i++ {
		limiter.Allow(fmt.Sprintf("198.51.%d.%d", i/256, i%256))
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		limiter.Allow(fmt.Sprintf("203.0.%d.%d", (i/256)%256, i%256))
	}
}

// BenchmarkRateLimiterParallel measures contention on the shared limiter state.
func BenchmarkRateLimiterParallel(b *testing.B) {
	limiter := NewRateLimiter(1000000, true)
	limiter.SetMaxClients(100000)

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			limiter.Allow("192.0.2.1")
		}
	})
}

func BenchmarkIPWhitelistAllowed(b *testing.B) {
	whitelist, err := NewIPWhitelist([]string{"10.0.0.0/8", "192.168.0.0/16", "172.16.0.0/12"})
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if !whitelist.IsAllowed("10.1.2.3") {
			b.Fatal("expected allowed")
		}
	}
}
