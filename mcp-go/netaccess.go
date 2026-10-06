// SPDX-License-Identifier: AGPL-3.0-or-later

// Who can reach the server: which addresses it listens on, and which
// computers' connections it takes.
//
//	local    this computer only (127.0.0.1, ::1): the default, so a server
//	         run on a laptop is not open to the café's Wi-Fi
//	wired    also other computers on a wired network (Ethernet): it listens
//	         on the addresses of the wired interfaces only, and takes a
//	         connection only from an address on one of their subnets. Wi-Fi,
//	         a phone's connection (USB, Bluetooth), a VPN and a virtual
//	         interface are never opened. The interfaces are looked at again
//	         every few seconds, so plugging a cable in or out takes effect
//	         without a restart.
//	network  every interface (":port"): a server, a container, Cloud Run
//
// "allow" narrows wired and network further to the listed address ranges
// (CIDR, e.g. 10.20.0.0/16 for the office), for other computers; this one
// is always let in.
//
// SLIQTLY_LISTEN (-listen) and SLIQTLY_ALLOW (-allow) set it for good; the
// settings page then shows it and cannot change it. Without them it is the
// folder's settings/network, which only this computer's own browser can
// change (/settings), or local.

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	accessLocal   = "local"
	accessWired   = "wired"
	accessNetwork = "network"
)

type netPolicy struct {
	Access string
	Allow  []string
	nets   []*net.IPNet
	Fixed  bool // set by -listen / -allow: not changed from the page
}

func newNetPolicy(access string, allow []string, fixed bool) (*netPolicy, error) {
	access = strings.ToLower(strings.TrimSpace(access))
	if access == "" {
		access = accessLocal
	}
	switch access {
	case accessLocal, accessWired, accessNetwork:
	default:
		return nil, fmt.Errorf("access %q: say local, wired or network", access)
	}
	p := &netPolicy{Access: access, Fixed: fixed, Allow: []string{}}
	for _, a := range allow {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if !strings.Contains(a, "/") {
			if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
				a += "/32"
			} else if ip != nil {
				a += "/128"
			}
		}
		_, n, err := net.ParseCIDR(a)
		if err != nil {
			return nil, fmt.Errorf("allow %q: not an address range like 10.20.0.0/16", a)
		}
		p.nets = append(p.nets, n)
		p.Allow = append(p.Allow, n.String())
	}
	return p, nil
}

func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' })
}

// an interface of this computer, as the policy sees it
type netIface struct {
	Name  string   `json:"name"`
	Kind  string   `json:"kind"` // wired, wifi, cellular, virtual, other
	Addrs []string `json:"addresses"`
	nets  []*net.IPNet
}

// the interfaces that are up, with addresses, loopback left out
func listIfaces() []netIface {
	kinds := interfaceKinds()
	ifs, _ := net.Interfaces()
	out := []netIface{}
	for _, it := range ifs {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := it.Addrs()
		ni := netIface{Name: it.Name, Kind: kinds[it.Name], Addrs: []string{}}
		if ni.Kind == "" {
			ni.Kind = "other"
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.IsLinkLocalUnicast() || n.IP.IsLoopback() {
				continue
			}
			ni.nets = append(ni.nets, n)
			ni.Addrs = append(ni.Addrs, n.IP.String())
		}
		if len(ni.nets) > 0 {
			out = append(out, ni)
		}
	}
	return out
}

// exposure keeps the server's listeners in line with the policy
type exposure struct {
	srv    *http.Server
	port   string
	policy atomic.Pointer[netPolicy]
	// the interfaces the last look found, for the filter and the page
	ifaces     atomic.Pointer[[]netIface]
	ifaceKinds func() []netIface // listIfaces; a test's stand-in

	mu   sync.Mutex
	open map[string]net.Listener
	// the connections taken, so that one the policy no longer takes (a
	// kept-alive page, an event stream) is closed when the policy or the
	// interfaces change
	conns  map[*keptConn]struct{}
	logged map[string]time.Time // refused addresses, logged once a minute
	poke   chan struct{}
	// where calls' TCP connections go (meet.go); nil: closed
	ice atomic.Pointer[chanListener]
	// https:// on the same port (owncert.go); nil: http:// only
	tls atomic.Pointer[tls.Config]
}

func newExposure(srv *http.Server, port string, p *netPolicy) *exposure {
	x := &exposure{srv: srv, port: port, open: map[string]net.Listener{}, conns: map[*keptConn]struct{}{}, logged: map[string]time.Time{}, poke: make(chan struct{}, 1), ifaceKinds: listIfaces}
	x.policy.Store(p)
	empty := []netIface{}
	x.ifaces.Store(&empty)
	return x
}

// setPolicy takes effect at once
func (x *exposure) setPolicy(p *netPolicy) {
	x.policy.Store(p)
	select {
	case x.poke <- struct{}{}:
	default:
	}
}

func (x *exposure) wanted() (addrs []string, what string) {
	p := x.policy.Load()
	ifs := x.ifaceKinds()
	x.ifaces.Store(&ifs)
	loop := []string{net.JoinHostPort("127.0.0.1", x.port), net.JoinHostPort("::1", x.port)}
	switch p.Access {
	case accessNetwork:
		return []string{":" + x.port}, "every network"
	case accessWired:
		addrs = loop
		names := []string{}
		for _, it := range ifs {
			if it.Kind != "wired" {
				continue
			}
			names = append(names, it.Name)
			for _, n := range it.nets {
				addrs = append(addrs, net.JoinHostPort(n.IP.String(), x.port))
			}
		}
		if len(names) == 0 {
			return addrs, "this computer only: no wired network now"
		}
		return addrs, "this computer and the wired network (" + strings.Join(names, ", ") + ")"
	}
	return loop, "this computer only"
}

// sync opens what the policy wants and closes the rest. The first call
// fails when not even 127.0.0.1 can be listened on (the port is taken).
func (x *exposure) sync(first bool) error {
	want, what := x.wanted()
	x.mu.Lock()
	defer x.mu.Unlock()
	keep := map[string]bool{}
	for _, a := range want {
		keep[a] = true
	}
	changed := false
	for a, l := range x.open {
		if !keep[a] {
			l.Close()
			delete(x.open, a)
			changed = true
		}
	}
	for _, a := range want {
		if _, ok := x.open[a]; ok {
			continue
		}
		l, err := net.Listen("tcp", a)
		if err != nil {
			// no IPv6 here, or an address that went away meanwhile
			if strings.HasPrefix(a, "[::1]") || (!first && a != ":"+x.port && !strings.HasPrefix(a, "127.0.0.1:")) {
				continue
			}
			if first {
				return err
			}
			log.Printf("listen %s: %v", a, err)
			continue
		}
		x.open[a] = l
		changed = true
		go func() {
			if err := x.srv.Serve(newSniffed(&filtered{Listener: l, x: x}, x)); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				log.Printf("serve %s: %v", a, err)
			}
		}()
	}
	if changed {
		log.Printf("listening on %s: %s", strings.Join(x.listening(), ", "), what)
	}
	for c := range x.conns {
		if !x.admits(c.ip) {
			c.Conn.Close()
			delete(x.conns, c)
		}
	}
	return nil
}

// the addresses listened on, sorted
func (x *exposure) listening() []string {
	out := []string{}
	for a := range x.open {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// own: ip is one of this computer's addresses (a connection from it, to
// 127.0.0.1, is this computer's: a call's TCP comes so from a browser here)
func (x *exposure) own(ip net.IP) bool {
	for _, it := range *x.ifaces.Load() {
		for _, n := range it.nets {
			if n.IP.Equal(ip) {
				return true
			}
		}
	}
	return false
}

// reachedOn: ip is one of this computer's addresses the server is reached
// on now (calls offer only those)
func (x *exposure) reachedOn(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	for a := range x.open {
		h, _, err := net.SplitHostPort(a)
		if err != nil {
			continue
		}
		if h == "" || ip.Equal(net.ParseIP(h)) {
			return true
		}
	}
	return false
}

func (x *exposure) Listening() []string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.listening()
}

// run keeps the listeners in line until ctx ends, looking at the
// interfaces every few seconds
func (x *exposure) run(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-x.poke:
		}
		x.sync(false)
	}
}

// admits says whether a connection from ip is taken
func (x *exposure) admits(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || x.own(ip) {
		return true
	}
	p := x.policy.Load()
	switch p.Access {
	case accessNetwork:
	case accessWired:
		// from a computer on one of the wired subnets: a packet that came
		// in another way (the Wi-Fi) for the wired address is not one
		on := false
		for _, it := range *x.ifaces.Load() {
			if it.Kind != "wired" {
				continue
			}
			for _, n := range it.nets {
				if n.Contains(ip) {
					on = true
				}
			}
		}
		if !on {
			return false
		}
	default:
		return false
	}
	if len(p.nets) == 0 {
		return true
	}
	for _, n := range p.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// a listener that closes the connections the policy does not take, before
// a byte of HTTP is read
type filtered struct {
	net.Listener
	x *exposure
}

func (f *filtered) Accept() (net.Conn, error) {
	for {
		c, err := f.Listener.Accept()
		if err != nil {
			return nil, err
		}
		var ip net.IP
		if a, ok := c.RemoteAddr().(*net.TCPAddr); ok {
			ip = a.IP
		}
		if f.x.admits(ip) {
			k := &keptConn{Conn: c, ip: ip, x: f.x}
			f.x.mu.Lock()
			f.x.conns[k] = struct{}{}
			f.x.mu.Unlock()
			return k, nil
		}
		c.Close()
		f.x.refused(ip)
	}
}

// a connection taken, known to the exposure until it closes
type keptConn struct {
	net.Conn
	ip net.IP
	x  *exposure
}

func (k *keptConn) Close() error {
	k.x.mu.Lock()
	delete(k.x.conns, k)
	k.x.mu.Unlock()
	return k.Conn.Close()
}

func (x *exposure) refused(ip net.IP) {
	key := ip.String()
	x.mu.Lock()
	last := x.logged[key]
	now := time.Now()
	if now.Sub(last) > time.Minute {
		x.logged[key] = now
		x.mu.Unlock()
		log.Printf("refused a connection from %s (access: %s)", key, x.policy.Load().Access)
		return
	}
	x.mu.Unlock()
}

// --- calls over the HTTP port

// sniffed hands a connection to calls (meet.go) when it is one: ICE over
// TCP (RFC 4571) starts with a two-byte length, and the first message, a
// STUN request, is far under 256 bytes, so its first byte is 0. TLS starts
// with 22 (a handshake record): https:// with the server's own certificate
// (owncert.go). HTTP starts with a letter. The first byte is waited for apart from
// Accept, so a page slow to send does not hold up the others.
type sniffed struct {
	net.Listener
	x    *exposure
	ch   chan net.Conn
	errc chan error
	done chan struct{}
	shut sync.Once
}

func newSniffed(l net.Listener, x *exposure) *sniffed {
	s := &sniffed{Listener: l, x: x, ch: make(chan net.Conn), errc: make(chan error, 1), done: make(chan struct{})}
	go s.loop()
	return s
}

const sniffWait = 30 * time.Second

func (s *sniffed) Accept() (net.Conn, error) {
	select {
	case c := <-s.ch:
		return c, nil
	case err := <-s.errc:
		return nil, err
	}
}

func (s *sniffed) Close() error {
	err := s.Listener.Close()
	s.shut.Do(func() { close(s.done) })
	return err
}

func (s *sniffed) loop() {
	for {
		c, err := s.Listener.Accept()
		if err != nil {
			s.errc <- err
			return
		}
		go s.route(c)
	}
}

func (s *sniffed) route(c net.Conn) {
	var b [1]byte
	c.SetReadDeadline(time.Now().Add(sniffWait))
	if _, err := io.ReadFull(c, b[:]); err != nil {
		c.Close()
		return
	}
	c.SetReadDeadline(time.Time{})
	var pc net.Conn = &peeked{Conn: c, first: b[:]}
	switch b[0] {
	case 0:
		if l := s.x.ice.Load(); l != nil && l.give(pc) {
			return
		}
		c.Close()
		return
	case 22:
		cfg := s.x.tls.Load()
		if cfg == nil {
			c.Close()
			return
		}
		pc = tls.Server(pc, cfg)
	}
	select {
	case s.ch <- pc:
	case <-s.done:
		c.Close()
	}
}

// a connection with its first bytes read already
type peeked struct {
	net.Conn
	first []byte
}

func (p *peeked) Read(b []byte) (int, error) {
	if len(p.first) > 0 {
		n := copy(b, p.first)
		p.first = p.first[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}

// chanListener is the listener calls' TCP connections come out of
type chanListener struct {
	ch   chan net.Conn
	done chan struct{}
	shut sync.Once
	port int
}

func (l *chanListener) give(c net.Conn) bool {
	select {
	case l.ch <- c:
		return true
	case <-l.done:
		return false
	case <-time.After(5 * time.Second):
		return false
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.shut.Do(func() { close(l.done) })
	return nil
}

// every address it is reached on, at the HTTP port
func (l *chanListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4zero, Port: l.port} }

// iceListener: a new listener for calls' TCP connections, in place of the
// one before
func (x *exposure) iceListener() *chanListener {
	port, _ := strconv.Atoi(x.port)
	l := &chanListener{ch: make(chan net.Conn), done: make(chan struct{}), port: port}
	if old := x.ice.Swap(l); old != nil {
		old.Close()
	}
	return l
}
