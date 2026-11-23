package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/knadh/listmonk/internal/addrcheck"
)

// timingDNS answers after a delay: 20 ms for a working domain, 3 s of silence (then a
// timeout) for every twentieth one. It records how many lookups ran at once and when each started.
type timingDNS struct {
	mu      sync.Mutex
	running int
	maxRun  int
	starts  []time.Time
}

func (f *timingDNS) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	f.mu.Lock()
	f.running++
	if f.running > f.maxRun {
		f.maxRun = f.running
	}
	f.starts = append(f.starts, time.Now())
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.running--; f.mu.Unlock() }()
	var n int
	fmt.Sscanf(name, "d%d.", &n)
	if n%20 == 0 {
		time.Sleep(3 * time.Second)
		return nil, &net.DNSError{Err: "i/o timeout", Name: name, IsTimeout: true}
	}
	time.Sleep(20 * time.Millisecond)
	return []*net.MX{{Host: "mx." + name, Pref: 10}}, nil
}

func (f *timingDNS) LookupIPAddr(ctx context.Context, name string) ([]net.IPAddr, error) {
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

// TestGateImportTiming measures the gate on a file of 50,000 rows over 2,000 domains (LST-56).
// It runs only when chosen exactly (go test -run '^TestGateImportTiming$'): it takes about a minute.
func TestGateImportTiming(t *testing.T) {
	if f := flag.Lookup("test.run"); f == nil || f.Value.String() != "^TestGateImportTiming$" {
		t.Skip("run it with -run '^TestGateImportTiming$'")
	}
	e := newGateEnv(t)
	dns := &timingDNS{}
	e.a.addrCheck = addrcheck.NewChecker(addrcheck.Options{Resolver: dns})
	const rows, domains = 50000, 2000
	emails := make([]string, rows)
	for i := range emails {
		emails[i] = fmt.Sprintf("user%d@d%d.test", i, (i*7919)%domains)
	}
	start := time.Now()
	var worst time.Duration
	warned, rejected := 0, 0
	for from := 0; from < rows; from += 500 {
		t0 := time.Now()
		for _, v := range e.a.importVerify(emails[from : from+500]) {
			if v.Reject != "" {
				rejected++
			}
			if v.Warn != "" {
				warned++
			}
		}
		if d := time.Since(t0); d > worst {
			worst = d
		}
	}
	total := time.Since(start)
	// The busiest second: lookups started within any 1 s window.
	dns.mu.Lock()
	maxPerSec := 0
	for i := range dns.starts {
		n := 0
		for j := i; j < len(dns.starts) && dns.starts[j].Sub(dns.starts[i]) < time.Second; j++ {
			n++
		}
		if n > maxPerSec {
			maxPerSec = n
		}
	}
	t.Logf("TIMING rows=%d domains=%d total=%s worst_batch=%s lookups=%d max_concurrent=%d max_started_per_second=%d warned=%d rejected=%d",
		rows, domains, total.Round(time.Millisecond), worst.Round(time.Millisecond), len(dns.starts), dns.maxRun, maxPerSec, warned, rejected)
	dns.mu.Unlock()
	if dns.maxRun > 8 || maxPerSec > 51 {
		t.Fatalf("limits broken: %d concurrent, %d per second", dns.maxRun, maxPerSec)
	}
}
