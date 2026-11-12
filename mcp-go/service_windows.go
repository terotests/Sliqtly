// SPDX-License-Identifier: AGPL-3.0-or-later

// The server as a Windows service, what sliqtly.service is on Linux:
//
//	sliqtly-server service install   (as administrator; the .msi runs it)
//	sliqtly-server service uninstall (the decks stay)
//	sliqtly-server service start|stop|restart|status
//	sliqtly-server service run       (the service manager starts this)
//
// The service runs as an account of its own, NT SERVICE\Sliqtly (like
// systemd's DynamicUser), with %ProgramData%\Sliqtly: sliqtly.env (the
// settings), data\, backup\ and log\sliqtly.log. Administrators and the
// system can read the folder, the service only reads its settings and
// writes data, backup and log; other users cannot see in, so the access
// token in data\access-token and in the log stays theirs.

package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

//go:embed packaging/windows/sliqtly.env
var defaultServiceEnv []byte

const (
	serviceAccount  = `NT SERVICE\` + serviceName
	serviceDisplay  = "Sliqtly server"
	serviceAbout    = "Sliqtly presentations: the MCP server with the editor and player. Settings in %ProgramData%\\Sliqtly\\sliqtly.env."
	serviceFirewall = "Sliqtly server"
	// how long a stop waits for saves under way and the rooms to be written
	serviceStopWait = 30 * time.Second
)

// serviceHome is %ProgramData%\Sliqtly
func serviceHome() string {
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, serviceName)
}

func serviceCmd(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "usage: sliqtly-server service install|uninstall|start|stop|restart|status|run [flags]")
		return 2
	}
	fs := flag.NewFlagSet("service "+args[0], flag.ContinueOnError)
	fs.SetOutput(errOut)
	config := fs.String("config", "", "install, run: the settings file (default %ProgramData%\\Sliqtly\\sliqtly.env)")
	start := fs.Bool("start", true, "install: start the service once it is installed")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	home := serviceHome()
	if *config == "" {
		*config = filepath.Join(home, "sliqtly.env")
	}
	var err error
	switch args[0] {
	case "run":
		return serviceRun(home, *config, errOut)
	case "install":
		err = serviceInstall(home, *config, *start, out)
	case "uninstall":
		err = serviceUninstall(out)
	case "start":
		err = withService(func(s *mgr.Service) error { return startService(s) })
	case "stop":
		err = withService(func(s *mgr.Service) error { return stopService(s) })
	case "restart":
		err = withService(func(s *mgr.Service) error {
			if err := stopService(s); err != nil {
				return err
			}
			return startService(s)
		})
	case "status":
		err = withService(func(s *mgr.Service) error {
			st, err := s.Query()
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "%s: %s\nsettings: %s\nlog: %s\n", serviceName, stateName(st.State), *config, filepath.Join(home, "log", "sliqtly.log"))
			return nil
		})
	default:
		fmt.Fprintln(errOut, "usage: sliqtly-server service install|uninstall|start|stop|restart|status|run [flags]")
		return 2
	}
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			err = fmt.Errorf("%w (run it as administrator)", err)
		}
		fmt.Fprintln(errOut, "service "+args[0]+":", err)
		return 1
	}
	return 0
}

// serviceInstall makes the service, or points the one there at this
// program (an upgrade), makes the folders with their access, writes the
// settings file unless there is one, lets the port in through the firewall
// and starts the service
func serviceInstall(home, config string, start bool, out io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	runArgs := []string{"service", "run"}
	if config != filepath.Join(home, "sliqtly.env") {
		runArgs = append(runArgs, "-config", config)
	}
	cfg := mgr.Config{
		DisplayName:      serviceDisplay,
		Description:      serviceAbout,
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: true,
		ServiceStartName: serviceAccount,
		SidType:          windows.SERVICE_SID_TYPE_UNRESTRICTED,
	}
	s, err := m.OpenService(serviceName)
	if err == nil {
		// there already: stopped while its program and settings change
		if err := stopService(s); err != nil {
			s.Close()
			return err
		}
		have, err := s.Config()
		if err == nil {
			cfg.ServiceType, cfg.ErrorControl = have.ServiceType, have.ErrorControl
			cfg.BinaryPathName = commandLine(exe, runArgs)
			err = s.UpdateConfig(cfg)
		}
		if err != nil {
			s.Close()
			return err
		}
		fmt.Fprintf(out, "service %s updated: %s\n", serviceName, exe)
	} else {
		s, err = m.CreateService(serviceName, exe, cfg, runArgs...)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "service %s installed: %s\n", serviceName, exe)
	}
	defer s.Close()
	// restarted after a failure, as Restart=on-failure, also when it
	// stopped with an error of its own rather than a crash
	restart := mgr.RecoveryAction{Type: mgr.ServiceRestart, Delay: 2 * time.Second}
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{restart, restart, restart}, 24*60*60); err != nil {
		return err
	}
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return err
	}
	if err := prepareServiceHome(home, config); err != nil {
		return err
	}
	if err := allowThroughFirewall(exe); err != nil {
		// the server still serves this computer; say how to let others in
		fmt.Fprintf(out, "firewall: %v (other computers cannot connect until port is allowed)\n", err)
	}
	if !start {
		return nil
	}
	if err := startService(s); err != nil {
		return err
	}
	fmt.Fprintf(out, "service %s running; settings %s, log %s\n", serviceName, config, filepath.Join(home, "log", "sliqtly.log"))
	return nil
}

// prepareServiceHome: %ProgramData%\Sliqtly for administrators and the
// system only, the service reading it; the folders it writes (log, data,
// backup, also ones the settings name elsewhere) changeable by the service
func prepareServiceHome(home, config string) error {
	sid, _, _, err := windows.LookupSID("", serviceAccount)
	if err != nil {
		return fmt.Errorf("the service's account %s: %w", serviceAccount, err)
	}
	if err := os.MkdirAll(home, 0o750); err != nil {
		return err
	}
	// SY, BA: full control; the service: read and run; inherited by what
	// is in it, nothing inherited from ProgramData (Users may read that)
	if err := protectFolder(home, "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;"+sid.String()+")"); err != nil {
		return err
	}
	if _, err := os.Stat(config); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(config), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(config, defaultServiceEnv, 0o640); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	settings, err := readSettings(config)
	if err != nil {
		return err
	}
	for _, dir := range serviceFolders(home, settings) {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
		if err := grantModify(dir, sid); err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
	}
	return nil
}

func readSettings(path string) ([][2]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s, err := parseEnvFile(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// protectFolder gives path the access sddl names and nothing inherited
// from the folder above; what is in path inherits it
func protectFolder(path, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// grantModify lets sid read, write and delete in path, beside the access
// path has; given again, it stays one entry
func grantModify(path string, sid *windows.SID) error {
	have, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	old, _, err := have.DACL()
	if err != nil {
		return err
	}
	dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.ACCESS_MASK(0x1301bf), // modify
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, old)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// allowThroughFirewall lets other computers reach the program on domain
// and private networks, not public ones (a cafe's Wi-Fi); who may connect
// is still the server's to say (SLIQTLY_LISTEN, SLIQTLY_ALLOW, the token)
func allowThroughFirewall(exe string) error {
	removeFirewallRule()
	return netsh(`advfirewall firewall add rule name="` + serviceFirewall + `" dir=in action=allow enable=yes profile=domain,private program="` + exe + `"`)
}

func removeFirewallRule() {
	netsh(`advfirewall firewall delete rule name="` + serviceFirewall + `"`)
}

// netsh runs netsh.exe with its own quoting: arguments as one command line
func netsh(args string) error {
	path := filepath.Join(os.Getenv("SystemRoot"), "System32", "netsh.exe")
	cmd := exec.Command(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + path + `" ` + args, HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// serviceUninstall stops the service and removes it and its firewall
// rule; %ProgramData%\Sliqtly (settings, decks, backups, log) stays
func serviceUninstall(out io.Writer) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		fmt.Fprintf(out, "service %s is not installed\n", serviceName)
		removeFirewallRule()
		return nil
	}
	defer s.Close()
	if err := stopService(s); err != nil {
		return err
	}
	if err := s.Delete(); err != nil {
		return err
	}
	removeFirewallRule()
	fmt.Fprintf(out, "service %s removed; settings and decks stay in %s\n", serviceName, serviceHome())
	return nil
}

func withService(do func(*mgr.Service) error) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("%s: %w (sliqtly-server service install)", serviceName, err)
	}
	defer s.Close()
	return do(s)
}

func startService(s *mgr.Service) error {
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return err
	}
	return waitState(s, svc.Running, 30*time.Second)
}

// stopService stops it and waits until it has; a stopped one is left so
func stopService(s *mgr.Service) error {
	st, err := s.Query()
	if err != nil {
		return err
	}
	if st.State == svc.Stopped {
		return nil
	}
	if st.State != svc.StopPending {
		if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return err
		}
	}
	return waitState(s, svc.Stopped, serviceStopWait+10*time.Second)
}

func waitState(s *mgr.Service, want svc.State, limit time.Duration) error {
	until := time.Now().Add(limit)
	for {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == want {
			return nil
		}
		if want == svc.Running && st.State == svc.Stopped {
			return fmt.Errorf("the service stopped; see %s", filepath.Join(serviceHome(), "log", "sliqtly.log"))
		}
		if time.Now().After(until) {
			return fmt.Errorf("still %s after %s", stateName(st.State), limit)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func stateName(s svc.State) string {
	switch s {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "starting"
	case svc.StopPending:
		return "stopping"
	case svc.Running:
		return "running"
	case svc.Paused, svc.PausePending, svc.ContinuePending:
		return "paused"
	}
	return fmt.Sprintf("state %d", s)
}

// commandLine is exe and args quoted as Windows reads a command line
func commandLine(exe string, args []string) string {
	parts := []string{syscall.EscapeArg(exe)}
	for _, a := range args {
		parts = append(parts, syscall.EscapeArg(a))
	}
	return strings.Join(parts, " ")
}

// serviceRun is the service itself: the settings file read into the
// environment, the log in log\sliqtly.log, and the server run until the
// service manager says stop
func serviceRun(home, config string, errOut io.Writer) int {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		fmt.Fprintln(errOut, "service run: only the service manager starts this (sliqtly-server service start); to run the server here, sliqtly-server -data <folder>")
		return 2
	}
	logFile, err := openServiceLog(filepath.Join(home, "log"))
	if err == nil {
		log.SetOutput(logFile)
		// what the runtime writes on a crash goes there too
		windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(logFile.Fd()))
		os.Stderr = logFile
	}
	if err := loadEnvFile(config); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("settings: %v", err)
		return 1
	}
	for k, v := range serviceDefaults(home, os.LookupEnv) {
		os.Setenv(k, v)
	}
	// the flags are the settings' now
	os.Args = os.Args[:1]
	start := time.Now()
	if err := svc.Run(serviceName, &winService{start: start}); err != nil {
		log.Printf("service: %v", err)
		return 1
	}
	return 0
}

// openServiceLog appends to sliqtly.log; one over 10 MB is kept as
// sliqtly.log.1 and a new one begun
func openServiceLog(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "sliqtly.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 10<<20 {
		os.Rename(path, path+".1")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
}

type winService struct{ start time.Time }

func (w *winService) Execute(_ []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	stop, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		runServer(w.start, stop)
	}()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case <-done:
			// the server ended without being told to: a failure, so the
			// service manager starts it again
			return true, 1
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: uint32(serviceStopWait / time.Millisecond)}
				cancel()
				select {
				case <-done:
				case <-time.After(serviceStopWait):
					log.Printf("stop: the server did not end in %s", serviceStopWait)
				}
				return false, 0
			}
		}
	}
}
