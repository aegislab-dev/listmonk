package addrcheck

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// DomainStatus is what DNS says about a domain's ability to receive mail.
type DomainStatus string

const (
	// StatusOK: the domain has a mail server (MX), or an address record that mail falls
	// back to (RFC 5321 section 5.1).
	StatusOK DomainStatus = "ok"
	// StatusDead: the domain does not exist, or it has neither MX nor an address record.
	// The Go resolver reports both the same way, so they are one status.
	StatusDead DomainStatus = "dead"
	// StatusNullMX: the domain says it accepts no mail ("MX 0 .", RFC 7505).
	StatusNullMX DomainStatus = "nullmx"
	// StatusUnknown: the lookup timed out or failed. Not a verdict. It is remembered only
	// briefly (Options.UnknownTTL) so that a silent domain is not waited for again by every
	// batch.
	StatusUnknown DomainStatus = "unknown"
	// StatusPending: the lookup did not finish within the caller's budget. It goes on in
	// the background, so a later call gets the answer from the cache.
	StatusPending DomainStatus = "pending"
)

// Resolver is the part of net.Resolver the checker uses. Tests replace it.
type Resolver interface {
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	// LookupIP is asked for "ip4" and "ip6" one at a time: the Go resolver's LookupIPAddr
	// reports "not found" when one family has no records and the other failed, which would
	// make a domain that only had a hiccup look dead.
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
}

// Options sets the limits of a Checker. Zero values take the defaults.
type Options struct {
	Resolver      Resolver
	LookupTimeout time.Duration // one DNS query; default 3s
	Concurrency   int           // lookups at once; default 8
	OKTTL         time.Duration // how long a working domain is remembered; default 7 days
	BadTTL        time.Duration // how long a dead or null-MX domain is remembered; default 1 day
	UnknownTTL    time.Duration // how long a domain that did not answer is remembered; default 60s
	MaxPerSecond  int           // lookups started per second, all callers together; default 50
	MaxEntries    int           // cached domains; default 50,000
	MaxQueued     int           // domains waiting for a lookup; default 20,000
	Now           func() time.Time
}

type cacheEntry struct {
	status  DomainStatus
	stored  time.Time
	expires time.Time
}

type call struct {
	done   chan struct{}
	status DomainStatus
}

type queued struct {
	domain string
	stored time.Time
}

// Checker looks domains up in DNS and remembers the answers in memory. The cache is per
// process and empty after a restart.
type Checker struct {
	opt Options

	mu       sync.Mutex
	cache    map[string]cacheEntry
	order    []queued         // cache entries, oldest first, for eviction
	inflight map[string]*call // domains queued or being looked up
	queue    []string
	workers  int

	rateMu   sync.Mutex
	starts   []time.Time // when the last MaxPerSecond lookups started, oldest first
}

// NewChecker returns a checker with the given limits.
func NewChecker(opt Options) *Checker {
	if opt.Resolver == nil {
		// The Go resolver gives the same error details on every platform.
		opt.Resolver = &net.Resolver{PreferGo: true}
	}
	if opt.LookupTimeout <= 0 {
		opt.LookupTimeout = 3 * time.Second
	}
	if opt.Concurrency <= 0 {
		opt.Concurrency = 8
	}
	if opt.OKTTL <= 0 {
		opt.OKTTL = 7 * 24 * time.Hour
	}
	if opt.BadTTL <= 0 {
		opt.BadTTL = 24 * time.Hour
	}
	if opt.UnknownTTL <= 0 {
		opt.UnknownTTL = 60 * time.Second
	}
	if opt.MaxPerSecond <= 0 {
		opt.MaxPerSecond = 50
	}
	if opt.MaxEntries <= 0 {
		opt.MaxEntries = 50000
	}
	if opt.MaxQueued <= 0 {
		opt.MaxQueued = 20000
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	return &Checker{opt: opt, cache: map[string]cacheEntry{}, inflight: map[string]*call{}}
}

// CheckDomains returns the status of every given domain. Answers come from the cache
// where it has them. The rest are looked up, at most Concurrency at a time and once per
// domain however many callers ask, and the call waits for them no longer than budget.
// Domains that are not ready by then (or when ctx ends) are StatusPending.
func (c *Checker) CheckDomains(ctx context.Context, domains []string, budget time.Duration) map[string]DomainStatus {
	type waiter struct {
		domain string
		call   *call
	}
	out := make(map[string]DomainStatus, len(domains))
	var waits []waiter

	c.mu.Lock()
	now := c.opt.Now()
	for _, d := range domains {
		if _, seen := out[d]; seen {
			continue
		}
		if e, ok := c.cache[d]; ok && now.Before(e.expires) {
			out[d] = e.status
			continue
		}
		out[d] = StatusPending
		cl, ok := c.inflight[d]
		if !ok {
			if len(c.inflight) >= c.opt.MaxQueued {
				continue
			}
			cl = &call{done: make(chan struct{})}
			c.inflight[d] = cl
			c.queue = append(c.queue, d)
			if c.workers < c.opt.Concurrency {
				c.workers++
				go c.work()
			}
		}
		waits = append(waits, waiter{d, cl})
	}
	c.mu.Unlock()

	if len(waits) == 0 {
		return out
	}
	timer := time.NewTimer(budget)
	defer timer.Stop()
	late := false
	for _, w := range waits {
		if late {
			// Out of time: take only what has already finished.
			select {
			case <-w.call.done:
				out[w.domain] = w.call.status
			default:
			}
			continue
		}
		select {
		case <-w.call.done:
			out[w.domain] = w.call.status
		case <-timer.C:
			late = true
		case <-ctx.Done():
			late = true
		}
	}
	return out
}

// work looks queued domains up until the queue is empty.
func (c *Checker) work() {
	for {
		c.mu.Lock()
		if len(c.queue) == 0 {
			c.queue = nil
			c.workers--
			c.mu.Unlock()
			return
		}
		d := c.queue[0]
		c.queue = c.queue[1:]
		cl := c.inflight[d]
		c.mu.Unlock()

		c.waitSlot()
		st := c.resolve(d)

		c.mu.Lock()
		delete(c.inflight, d)
		c.store(d, st)
		c.mu.Unlock()
		cl.status = st
		close(cl.done)
	}
}

// waitSlot blocks until the caller may start a lookup: at most MaxPerSecond lookups start in
// any second, all callers together, so that a big import cannot flood the resolver of the host.
// The start times of the last MaxPerSecond lookups are kept, and a lookup starts only when the
// oldest of them is a second old (a millisecond more, so that both ends of a second are not
// counted): this holds also after the process was paused and many callers wake together.
// Concurrency still limits how many run at once.
func (c *Checker) waitSlot() {
	window := time.Second + time.Millisecond
	for {
		c.rateMu.Lock()
		now := time.Now()
		if len(c.starts) < c.opt.MaxPerSecond || now.Sub(c.starts[0]) >= window {
			if len(c.starts) >= c.opt.MaxPerSecond {
				c.starts = c.starts[1:]
			}
			c.starts = append(c.starts, now)
			c.rateMu.Unlock()
			return
		}
		wait := window - now.Sub(c.starts[0])
		c.rateMu.Unlock()
		time.Sleep(wait)
	}
}

// store remembers an answer. The caller holds the lock.
func (c *Checker) store(domain string, st DomainStatus) {
	now := c.opt.Now()
	ttl := c.opt.BadTTL
	switch st {
	case StatusOK:
		ttl = c.opt.OKTTL
	case StatusUnknown:
		ttl = c.opt.UnknownTTL
	}
	c.cache[domain] = cacheEntry{status: st, stored: now, expires: now.Add(ttl)}
	c.order = append(c.order, queued{domain, now})
	// Drop the oldest answers over the limit. An order row whose time differs from the
	// cache entry is left over from an earlier answer for the same domain.
	for len(c.cache) > c.opt.MaxEntries && len(c.order) > 0 {
		old := c.order[0]
		c.order = c.order[1:]
		if e, ok := c.cache[old.domain]; ok && e.stored.Equal(old.stored) {
			delete(c.cache, old.domain)
		}
	}
	if len(c.order) > 2*c.opt.MaxEntries {
		live := c.order[:0]
		for _, o := range c.order {
			if e, ok := c.cache[o.domain]; ok && e.stored.Equal(o.stored) {
				live = append(live, o)
			}
		}
		c.order = live
	}
}

// resolve asks DNS about one domain. The name is looked up with a trailing dot so that
// the search domains of the host's resolv.conf are never tried.
func (c *Checker) resolve(domain string) DomainStatus {
	name := domain + "."

	ctx, cancel := context.WithTimeout(context.Background(), c.opt.LookupTimeout)
	mx, err := c.opt.Resolver.LookupMX(ctx, name)
	cancel()
	if len(mx) > 0 {
		for _, r := range mx {
			if r.Host != "." && r.Host != "" {
				return StatusOK
			}
		}
		return StatusNullMX
	}
	if err != nil && !notFound(err) {
		return StatusUnknown
	}

	// No MX: mail goes to the domain's own address (A or AAAA), if it has one. Each family is
	// asked on its own: the domain is dead only when both say the name has no such record, and
	// unknown when one family failed and the other has none.
	failed := 0
	for _, network := range []string{"ip4", "ip6"} {
		ctx, cancel = context.WithTimeout(context.Background(), c.opt.LookupTimeout)
		ips, err := c.opt.Resolver.LookupIP(ctx, network, name)
		cancel()
		switch {
		case len(ips) > 0:
			return StatusOK
		case err == nil || notFound(err):
			// no record of this family
		default:
			failed++
		}
	}
	if failed > 0 {
		return StatusUnknown
	}
	return StatusDead
}

// notFound reports whether DNS answered that the name or the record type does not exist,
// as opposed to not answering.
func notFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound && !dnsErr.IsTimeout && !dnsErr.IsTemporary
}
