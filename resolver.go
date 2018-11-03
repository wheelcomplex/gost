package gost

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/go-log/log"
)

var (
	// DefaultResolverTimeout is the default timeout for name resolution.
	DefaultResolverTimeout = 30 * time.Second
	// DefaultResolverTTL is the default cache TTL for name resolution.
	DefaultResolverTTL = 60 * time.Second
)

// Resolver is a name resolver for domain name.
// It contains a list of name servers.
type Resolver interface {
	// Resolve returns a slice of that host's IPv4 and IPv6 addresses.
	Resolve(host string) ([]net.IP, error)
}

// ReloadResolver is resolover that support live reloading
type ReloadResolver interface {
	Resolver
	Reloader
}

// NameServer is a name server.
// Currently supported protocol: TCP, UDP and TLS.
type NameServer struct {
	Addr     string
	Protocol string
	Hostname string // for TLS handshake verification
}

func (ns NameServer) String() string {
	addr := ns.Addr
	prot := ns.Protocol
	host := ns.Hostname
	if _, port, _ := net.SplitHostPort(addr); port == "" {
		addr = net.JoinHostPort(addr, "53")
	}
	if prot == "" {
		prot = "udp"
	}
	return fmt.Sprintf("%s/%s %s", addr, prot, host)
}

type resolverCacheItem struct {
	IPs []net.IP
	ts  int64
}

type resolver struct {
	Resolver *net.Resolver
	Servers  []NameServer
	mCache   *sync.Map
	Timeout  time.Duration
	TTL      time.Duration
	period   time.Duration
}

// NewResolver create a new Resolver with the given name servers and resolution timeout.
func NewResolver(timeout, ttl time.Duration, servers ...NameServer) ReloadResolver {
	r := &resolver{
		Servers: servers,
		Timeout: timeout,
		TTL:     ttl,
		mCache:  &sync.Map{},
	}
	r.init()
	return r
}

func (r *resolver) init() {
	if r.Timeout <= 0 {
		r.Timeout = DefaultResolverTimeout
	}
	if r.TTL == 0 {
		r.TTL = DefaultResolverTTL
	}

	r.Resolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (conn net.Conn, err error) {
			for _, ns := range r.Servers {
				conn, err = r.dial(ctx, ns)
				if err == nil {
					break
				}
				log.Logf("[resolver] %s : %s", ns, err)
			}
			return
		},
	}
}

func (r *resolver) dial(ctx context.Context, ns NameServer) (net.Conn, error) {
	var d net.Dialer

	addr := ns.Addr
	if _, port, _ := net.SplitHostPort(addr); port == "" {
		addr = net.JoinHostPort(addr, "53")
	}
	switch strings.ToLower(ns.Protocol) {
	case "tcp":
		return d.DialContext(ctx, "tcp", addr)
	case "tls":
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		cfg := &tls.Config{
			ServerName: ns.Hostname,
		}
		if cfg.ServerName == "" {
			cfg.InsecureSkipVerify = true
		}
		return tls.Client(conn, cfg), nil
	case "udp":
		fallthrough
	default:
		return d.DialContext(ctx, "udp", addr)
	}
}

func (r *resolver) Resolve(name string) (ips []net.IP, err error) {
	if r == nil {
		return
	}
	timeout := r.Timeout

	if ip := net.ParseIP(name); ip != nil {
		return []net.IP{ip}, nil
	}

	ips = r.loadCache(name)
	if len(ips) > 0 {
		if Debug {
			log.Logf("[resolver] cache hit: %s %v", name, ips)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	addrs, err := r.Resolver.LookupIPAddr(ctx, name)
	for _, addr := range addrs {
		ips = append(ips, addr.IP)
	}
	r.storeCache(name, ips)
	if len(ips) > 0 && Debug {
		log.Logf("[resolver] %s %v", name, ips)
	}
	return
}

func (r *resolver) loadCache(name string) []net.IP {
	ttl := r.TTL
	if ttl < 0 {
		return nil
	}

	if v, ok := r.mCache.Load(name); ok {
		item, _ := v.(*resolverCacheItem)
		if item == nil || time.Since(time.Unix(item.ts, 0)) > ttl {
			return nil
		}
		return item.IPs
	}

	return nil
}

func (r *resolver) storeCache(name string, ips []net.IP) {
	ttl := r.TTL
	if ttl < 0 || name == "" || len(ips) == 0 {
		return
	}
	r.mCache.Store(name, &resolverCacheItem{
		IPs: ips,
		ts:  time.Now().Unix(),
	})
}

func (r *resolver) Reload(rd io.Reader) error {
	var nss []NameServer

	scanner := bufio.NewScanner(rd)
	for scanner.Scan() {
		line := scanner.Text()
		if n := strings.IndexByte(line, '#'); n >= 0 {
			line = line[:n]
		}
		line = strings.Replace(line, "\t", " ", -1)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ss []string
		for _, s := range strings.Split(line, " ") {
			if s = strings.TrimSpace(s); s != "" {
				ss = append(ss, s)
			}
		}

		if len(ss) == 0 {
			continue
		}

		if len(ss) >= 2 {
			// timeout option
			if strings.ToLower(ss[0]) == "timeout" {
				r.Timeout, _ = time.ParseDuration(ss[1])
				continue
			}

			// ttl option
			if strings.ToLower(ss[0]) == "ttl" {
				r.TTL, _ = time.ParseDuration(ss[1])
				continue
			}

			// reload option
			if strings.ToLower(ss[0]) == "reload" {
				r.period, _ = time.ParseDuration(ss[1])
				continue
			}
		}

		var ns NameServer
		switch len(ss) {
		case 1:
			ns.Addr = ss[0]
		case 2:
			ns.Addr = ss[0]
			ns.Protocol = ss[1]
		default:
			ns.Addr = ss[0]
			ns.Protocol = ss[1]
			ns.Hostname = ss[2]
		}
		nss = append(nss, ns)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	r.Servers = nss
	return nil
}

func (r *resolver) Period() time.Duration {
	return r.period
}

func (r *resolver) String() string {
	if r == nil {
		return ""
	}

	b := &bytes.Buffer{}
	fmt.Fprintf(b, "Timeout %v\n", r.Timeout)
	fmt.Fprintf(b, "TTL %v\n", r.TTL)
	fmt.Fprintf(b, "Reload %v\n", r.period)
	for i := range r.Servers {
		fmt.Fprintln(b, r.Servers[i])
	}
	return b.String()
}
