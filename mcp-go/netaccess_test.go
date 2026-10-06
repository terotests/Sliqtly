// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHardwarePorts(t *testing.T) {
	out := `
Hardware Port: Wi-Fi
Device: en0
Ethernet Address: aa:bb:cc:dd:ee:ff

Hardware Port: Thunderbolt Bridge
Device: bridge0
Ethernet Address: N/A

Hardware Port: USB 10/100/1000 LAN
Device: en7
Ethernet Address: 11:22:33:44:55:66

Hardware Port: iPhone USB
Device: en8
Ethernet Address: N/A

Hardware Port: Bluetooth PAN
Device: en9

VLAN Configurations
===================
`
	eq(t, parseHardwarePorts(out), map[string]string{"en0": "wifi", "bridge0": "wired", "en7": "wired", "en8": "cellular", "en9": "cellular"})
}

func TestSysfsKinds(t *testing.T) {
	root := t.TempDir()
	mk := func(name string, files ...string) {
		for _, f := range files {
			p := filepath.Join(root, name, f)
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte("1\n"), 0o644)
		}
	}
	mk("eth0", "type", "device/vendor")
	mk("wlan0", "type", "device/vendor", "wireless/x")
	mk("docker0", "type")
	mk("wwan0", "type", "device/vendor")
	mk("usb0", "type", "device/vendor")
	drivers := filepath.Join(root, "_drivers", "rndis_host")
	os.MkdirAll(drivers, 0o755)
	os.Symlink(drivers, filepath.Join(root, "usb0", "device", "driver"))
	got := sysfsKinds(root, os.ReadDir)
	delete(got, "_drivers")
	eq(t, got, map[string]string{"eth0": "wired", "wlan0": "wifi", "docker0": "virtual", "wwan0": "cellular", "usb0": "cellular"})
}

func TestNetPolicy(t *testing.T) {
	p, err := newNetPolicy("", nil, false)
	if err != nil || p.Access != accessLocal {
		t.Fatal(p, err)
	}
	if _, err := newNetPolicy("everywhere", nil, false); err == nil {
		t.Fatal("an unknown access was taken")
	}
	if _, err := newNetPolicy("wired", []string{"10.0.0.0/33"}, false); err == nil {
		t.Fatal("a bad range was taken")
	}
	p, _ = newNetPolicy("Wired", splitList("10.20.0.0/16, 192.168.5.7"), false)
	eq(t, []any{p.Access, p.Allow}, []any{"wired", []string{"10.20.0.0/16", "192.168.5.7/32"}})
}

func fakeIfaces(ifs ...netIface) func() []netIface {
	for i := range ifs {
		for _, a := range ifs[i].Addrs {
			_, n, _ := net.ParseCIDR(a)
			ip, _, _ := net.ParseCIDR(a)
			n.IP = ip
			ifs[i].nets = append(ifs[i].nets, n)
		}
	}
	return func() []netIface { return ifs }
}

func TestWiredOnly(t *testing.T) {
	p, _ := newNetPolicy("wired", nil, false)
	x := newExposure(&http.Server{}, "8080", p)
	x.ifaceKinds = fakeIfaces(
		netIface{Name: "en7", Kind: "wired", Addrs: []string{"10.20.3.4/16"}},
		netIface{Name: "en0", Kind: "wifi", Addrs: []string{"192.168.1.20/24"}},
		netIface{Name: "en8", Kind: "cellular", Addrs: []string{"172.20.10.2/28"}},
	)
	addrs, what := x.wanted()
	eq(t, addrs, []string{"127.0.0.1:8080", "[::1]:8080", "10.20.3.4:8080"})
	match(t, what, `wired network \(en7\)`)
	eq(t, x.admits(net.ParseIP("127.0.0.1")), true)
	eq(t, x.admits(net.ParseIP("10.20.9.9")), true)
	// someone on the Wi-Fi, or behind the phone, who found the wired address
	eq(t, x.admits(net.ParseIP("192.168.1.66")), false)
	eq(t, x.admits(net.ParseIP("172.20.10.5")), false)

	// narrowed to the office's range
	p, _ = newNetPolicy("wired", []string{"10.20.5.0/24"}, false)
	x.setPolicy(p)
	eq(t, x.admits(net.ParseIP("10.20.9.9")), false)
	eq(t, x.admits(net.ParseIP("10.20.5.9")), true)

	// on Wi-Fi only: nothing but this computer
	x.ifaceKinds = fakeIfaces(netIface{Name: "en0", Kind: "wifi", Addrs: []string{"192.168.1.20/24"}})
	addrs, what = x.wanted()
	eq(t, addrs, []string{"127.0.0.1:8080", "[::1]:8080"})
	match(t, what, `no wired network`)

	p, _ = newNetPolicy("local", nil, false)
	x.setPolicy(p)
	eq(t, x.admits(net.ParseIP("10.20.5.9")), false)
}

// this machine's address that is not loopback, to connect to as if from
// another computer
func outsideIP(t *testing.T) string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			return n.IP.String()
		}
	}
	t.Skip("no address besides loopback")
	return ""
}

func freePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

func reach(addr string) string {
	// a new connection each time: one kept alive was let in under the last policy
	c := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	res, err := c.Get("http://" + addr + "/")
	if err != nil {
		return "no"
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return string(b)
}

func TestListening(t *testing.T) {
	ip := outsideIP(t)
	port := freePort(t)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })}
	defer srv.Close()
	p, _ := newNetPolicy("", nil, false)
	x := newExposure(srv, port, p)
	if err := x.sync(true); err != nil {
		t.Fatal(err)
	}
	eq(t, reach("127.0.0.1:"+port), "ok")
	// the default: not on any other address
	eq(t, reach(net.JoinHostPort(ip, port)), "no")
	for _, a := range x.Listening() {
		if !strings.HasPrefix(a, "127.0.0.1:") && !strings.HasPrefix(a, "[::1]:") {
			t.Fatal("listening on " + a)
		}
	}

	// every interface
	p, _ = newNetPolicy("network", nil, false)
	x.setPolicy(p)
	if err := x.sync(false); err != nil {
		t.Fatal(err)
	}
	eq(t, x.Listening(), []string{":" + port})
	eq(t, reach(net.JoinHostPort(ip, port)), "ok")
	eq(t, reach("127.0.0.1:"+port), "ok")

	// a page kept connected from the other address
	kept, err := net.Dial("tcp", net.JoinHostPort(ip, port))
	if err != nil {
		t.Fatal(err)
	}
	defer kept.Close()
	io.WriteString(kept, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	buf := make([]byte, 512)
	n, _ := kept.Read(buf)
	match(t, string(buf[:n]), `ok$`)

	// every interface, but only from a range this connection is not in:
	// a new one is closed before a byte of HTTP, the kept one is closed
	p, _ = newNetPolicy("network", []string{"203.0.113.0/24"}, false)
	x.setPolicy(p)
	x.sync(false)
	kept.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := kept.Read(buf); err == nil {
		t.Fatal("the kept connection is still open")
	}
	eq(t, reach(net.JoinHostPort(ip, port)), "no")
	eq(t, reach("127.0.0.1:"+port), "ok")

	// and back to this computer only
	p, _ = newNetPolicy("local", nil, false)
	x.setPolicy(p)
	x.sync(false)
	eq(t, reach(net.JoinHostPort(ip, port)), "no")
	eq(t, reach("127.0.0.1:"+port), "ok")
}

func TestNetworkSettingsAPI(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	ls := srv.Config.Handler.(*localServer)
	p, _ := newNetPolicy("", nil, false)
	ls.expo = newExposure(&http.Server{}, freePort(t), p)

	code, body := req(t, "GET", srv.URL+"/api/settings/network", "", "")
	eq(t, code, 200)
	match(t, body, `"access":"local"`)
	match(t, body, `"editable":true`)

	code, body = req(t, "PUT", srv.URL+"/api/settings/network", "application/json", `{"access":"wired","allow":["10.20.0.0/16"]}`)
	eq(t, code, 200)
	match(t, body, `"access":"wired"`)
	eq(t, ls.expo.policy.Load().Access, "wired")
	got, _ := loadNetPolicy(t.Context(), ls.env.DB)
	eq(t, []any{got.Access, got.Allow}, []any{"wired", []string{"10.20.0.0/16"}})

	code, body = req(t, "PUT", srv.URL+"/api/settings/network", "application/json", `{"access":"anywhere"}`)
	eq(t, code, 400)

	// from another computer: seen, not changed
	r := httptest.NewRequest("PUT", "http://localhost/api/settings/network", strings.NewReader(`{"access":"network"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "10.20.3.9:51000"
	w := httptest.NewRecorder()
	ls.ServeHTTP(w, r)
	eq(t, w.Code, 403)
	eq(t, ls.expo.policy.Load().Access, "wired")
	r = httptest.NewRequest("GET", "http://localhost/api/settings/network", nil)
	r.RemoteAddr = "10.20.3.9:51000"
	w = httptest.NewRecorder()
	ls.ServeHTTP(w, r)
	match(t, w.Body.String(), `"editable":false`)

	// set where the server was started: not from the page
	p, _ = newNetPolicy("network", nil, true)
	ls.expo.setPolicy(p)
	code, body = req(t, "PUT", srv.URL+"/api/settings/network", "application/json", `{"access":"local"}`)
	eq(t, code, 409)
	match(t, body, `SLIQTLY_LISTEN`)
}
