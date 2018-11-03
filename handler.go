package gost

import (
	"bufio"
	"crypto/tls"
	"net"
	"net/url"
	"time"

	"github.com/ginuerzh/gosocks4"
	"github.com/ginuerzh/gosocks5"
	"github.com/go-log/log"
)

// Handler is a proxy server handler
type Handler interface {
	Init(options ...HandlerOption)
	Handle(net.Conn)
}

// HandlerOptions describes the options for Handler.
type HandlerOptions struct {
	Addr      string
	Chain     *Chain
	Users     []*url.Userinfo
	TLSConfig *tls.Config
	Whitelist *Permissions
	Blacklist *Permissions
	Strategy  Strategy
	Bypass    *Bypass
	Retries   int
	Timeout   time.Duration
	Resolver  Resolver
	Hosts     *Hosts
}

// HandlerOption allows a common way to set handler options.
type HandlerOption func(opts *HandlerOptions)

// AddrHandlerOption sets the Addr option of HandlerOptions.
func AddrHandlerOption(addr string) HandlerOption {
	return func(opts *HandlerOptions) {
		opts.Addr = addr
	}
}

// ChainHandlerOption sets the Chain option of HandlerOptions.
func ChainHandlerOption(chain *Chain) HandlerOption {
	return func(opts *HandlerOptions) {
		opts.Chain = chain
	}
}

// UsersHandlerOption sets the Users option of HandlerOptions.
func UsersHandlerOption(users ...*url.Userinfo) HandlerOption {
	return func(opts *HandlerOptions) {
		opts.Users = users
	}
}

// TLSConfigHandlerOption sets the TLSConfig option of HandlerOptions.
func TLSConfigHandlerOption(config *tls.Config) HandlerOption {
	return func(opts *HandlerOptions) {
		opts.TLSConfig = config
	}
}

// WhitelistHandlerOption sets the Whitelist option of HandlerOptions.
func WhitelistHandlerOption(whitelist *Permissions) HandlerOption {
	return func(opts *HandlerOptions) {
		opts.Whitelist = whitelist
	}
}

// BlacklistHandlerOption sets the Blacklist option of HandlerOptions.
func BlacklistHandlerOption(blacklist *Permissions) HandlerOption {
	return func(opts *HandlerOptions) {
		opts.Blacklist = blacklist
	}
}

// BypassHandlerOption sets the bypass option of HandlerOptions.
func BypassHandlerOption(bypass *Bypass) HandlerOption {
	return func(opts *HandlerOptions) {
		opts.Bypass = bypass
	}
}

// StrategyHandlerOption sets the strategy option of HandlerOptions.
func StrategyHandlerOption(strategy Strategy) HandlerOption {
	return func(opts *HandlerOptions) {
		opts.Strategy = strategy
	}
}

// RetryHandlerOption sets the retry option of HandlerOptions.
func RetryHandlerOption(retries int) HandlerOption {
	return func(opts *HandlerOptions) {
		opts.Retries = retries
	}
}

// TimeoutHandlerOption sets the timeout option of HandlerOptions.
func TimeoutHandlerOption(timeout time.Duration) HandlerOption {
	return func(opts *HandlerOptions) {
		opts.Timeout = timeout
	}
}

// ResolverHandlerOption sets the resolver option of HandlerOptions.
func ResolverHandlerOption(resolver Resolver) HandlerOption {
	return func(opts *HandlerOptions) {
		opts.Resolver = resolver
	}
}

// HostsHandlerOption sets the Hosts option of HandlerOptions.
func HostsHandlerOption(hosts *Hosts) HandlerOption {
	return func(opts *HandlerOptions) {
		opts.Hosts = hosts
	}
}

type autoHandler struct {
	options *HandlerOptions
}

// AutoHandler creates a server Handler for auto proxy server.
func AutoHandler(opts ...HandlerOption) Handler {
	h := &autoHandler{}
	h.Init(opts...)
	return h
}

func (h *autoHandler) Init(options ...HandlerOption) {
	if h.options == nil {
		h.options = &HandlerOptions{}
	}
	for _, opt := range options {
		opt(h.options)
	}
}

func (h *autoHandler) Handle(conn net.Conn) {
	br := bufio.NewReader(conn)
	b, err := br.Peek(1)
	if err != nil {
		log.Log(err)
		conn.Close()
		return
	}

	cc := &bufferdConn{Conn: conn, br: br}
	var handler Handler
	switch b[0] {
	case gosocks4.Ver4:
		// SOCKS4(a) does not suppport authentication method,
		// so we ignore it when credentials are specified for security reason.
		if len(h.options.Users) > 0 {
			cc.Close()
			return
		}
		handler = &socks4Handler{options: h.options}
	case gosocks5.Ver5: // socks5
		handler = &socks5Handler{options: h.options}
	default: // http
		handler = &httpHandler{options: h.options}
	}
	handler.Init()
	handler.Handle(cc)
}

type bufferdConn struct {
	net.Conn
	br *bufio.Reader
}

func (c *bufferdConn) Read(b []byte) (int, error) {
	return c.br.Read(b)
}
