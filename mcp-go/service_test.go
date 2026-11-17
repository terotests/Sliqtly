// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	in := "\ufeff# settings\r\nPORT=8080\r\n\n  SLIQTLY_URL = http://host:8080 \n#SLIQTLY_TOKEN=x\nSLIQTLY_BACKUP=\nSLIQTLY_IMPORT_DIRS=\"D:\\Photos,E:\\My Data\"\nQ='a b'\n"
	got, err := parseEnvFile(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, got, [][2]string{{"PORT", "8080"}, {"SLIQTLY_URL", "http://host:8080"}, {"SLIQTLY_BACKUP", ""}, {"SLIQTLY_IMPORT_DIRS", `D:\Photos,E:\My Data`}, {"Q", "a b"}})
	for _, bad := range []string{"PORT 8080\n", "=x\n", "1A=x\n", "A-B=x\n"} {
		if _, err := parseEnvFile(strings.NewReader(bad)); err == nil || !strings.Contains(err.Error(), "line 1") {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}

func TestLoadEnvFileKeepsSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sliqtly.env")
	os.WriteFile(path, []byte("SLIQTLY_TEST_A=file\nSLIQTLY_TEST_B=file\n"), 0o600)
	t.Setenv("SLIQTLY_TEST_A", "env")
	os.Unsetenv("SLIQTLY_TEST_B")
	t.Cleanup(func() { os.Unsetenv("SLIQTLY_TEST_B") })
	if err := loadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	eq(t, []string{os.Getenv("SLIQTLY_TEST_A"), os.Getenv("SLIQTLY_TEST_B")}, []string{"env", "file"})
	if err := loadEnvFile(path + ".none"); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestServiceDefaults(t *testing.T) {
	home := filepath.Join("C:", "ProgramData", "Sliqtly")
	look := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	// nothing set: decks and backups in the home folder, on the network
	eq(t, serviceDefaults(home, look(nil)), map[string]string{
		"SLIQTLY_DATA": filepath.Join(home, "data"), "SLIQTLY_BACKUP": filepath.Join(home, "backup"), "SLIQTLY_LISTEN": "network"})
	// SLIQTLY_BACKUP= turns backups off, SLIQTLY_DATA= is the default
	eq(t, serviceDefaults(home, look(map[string]string{"SLIQTLY_DATA": "", "SLIQTLY_BACKUP": "", "SLIQTLY_LISTEN": "local"})),
		map[string]string{"SLIQTLY_DATA": filepath.Join(home, "data")})
	eq(t, serviceDefaults(home, look(map[string]string{"SLIQTLY_DATA": `D:\decks`, "SLIQTLY_BACKUP": `E:\b`})),
		map[string]string{"SLIQTLY_LISTEN": "network"})
}

func TestServiceFolders(t *testing.T) {
	home := "H"
	eq(t, serviceFolders(home, nil), []string{filepath.Join("H", "log"), filepath.Join("H", "data"), filepath.Join("H", "backup")})
	eq(t, serviceFolders(home, [][2]string{{"SLIQTLY_DATA", `D:\decks`}, {"SLIQTLY_BACKUP", ""}}), []string{filepath.Join("H", "log"), `D:\decks`})
}

func TestWindowsKind(t *testing.T) {
	for _, c := range []struct {
		typ  uint32
		hw   bool
		desc string
		want string
	}{
		{6, true, "Intel(R) Ethernet Connection I219-V", "wired"},
		{71, true, "Intel(R) Wi-Fi 6 AX201 160MHz", "wifi"},
		{6, false, "Hyper-V Virtual Ethernet Adapter", "virtual"},
		{53, false, "WireGuard Tunnel", "virtual"},
		{24, false, "Software Loopback Interface 1", "loopback"},
		{6, true, "Remote NDIS based Internet Sharing Device", "cellular"},
		{6, true, "Apple Mobile Device Ethernet", "cellular"},
		{243, true, "Generic Mobile Broadband Adapter", "cellular"},
		{144, true, "1394 Net Adapter", "other"},
	} {
		eq(t, windowsKind(c.typ, c.hw, c.desc), c.want, c.desc)
	}
}
