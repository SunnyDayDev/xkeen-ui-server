package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/linuxlistener"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/server"
)

const (
	serverBase        = "/opt/libexec/xkeen-ui/server"
	serverCurrent     = serverBase + "/current"
	serverConfig      = "/opt/etc/xkeen-ui/server"
	serverProfilePath = serverConfig + "/profile.json"
	serverCertPath    = serverConfig + "/tls-cert.pem"
	serverKeyPath     = serverConfig + "/tls-key.pem"
	serverState       = stateBase + "/server"
	serverDB          = serverState + "/server.sqlite"
	serverReceipt     = control + "/server.process.json"
	serverInit        = "/opt/etc/init.d/S91xkeen-ui-server"
	serverAsset       = "xkeen-ui-server-linux-arm64"
)

var (
	errServerTLS       = errors.New("invalid server TLS pair")
	errServerReady     = errors.New("installed server did not confirm HTTPS readiness")
	errServerInstalled = errors.New("a central server installation already exists")
)

type serverProfile struct {
	Release    release
	UID        uint32
	GID        uint32
	Listen     string
	Origin     string
	CertDigest string
	KeyDigest  string
}

func installServer(args []string) error {
	stage, version, manifest := args[0], args[1], args[2]
	selected := release{version, manifest}
	uid, gid, ok := parsePrincipal(args[3])
	if !ok || !trustedStage(stage) || !validRelease(selected) {
		return errUsage
	}
	listen, err := netip.ParseAddrPort(args[4])
	if err != nil || listen.Port() == 0 || listen.Addr().Zone() != "" {
		return errors.New("invalid server listen address")
	}
	origin, err := server.ParseWebOrigin(args[5])
	if err != nil {
		return errors.New("invalid server browser origin")
	}
	cert, err := readProtected(args[6], 0, 1<<20)
	if err != nil {
		return errServerTLS
	}
	key, err := readProtected(args[7], 0600, 1<<20)
	if err != nil {
		return errServerTLS
	}
	defer clear(key)
	if _, err := validateServerCert(cert, key, string(origin)); err != nil {
		return err
	}
	p := serverProfile{selected, uid, gid, listen.String(), string(origin), digest(cert), digest(key)}
	if err := probePIDFD(); err != nil {
		return errProcess
	}
	admission, err := lockAdmission("server", p.UID, p.GID)
	if err != nil {
		return err
	}
	defer admission.Close()
	lock, err := lockRole("server")
	if err != nil {
		return err
	}
	defer lock.Close()
	if _, err := os.Lstat(serverInit); !errors.Is(err, os.ErrNotExist) {
		return errServerInstalled
	}
	if err := rootDirs(true, serverConfig, serverBase, serverBase+"/generations", filepath.Dir(serverInit)); err != nil {
		return err
	}

	existing, err := loadServerProfile()
	hasExisting := err == nil
	if hasExisting && existing != p {
		return errors.New("retained server installation requires the same TLS and service profile")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errSafety
	}
	if !hasExisting {
		if _, err := os.Lstat(serverState); !errors.Is(err, os.ErrNotExist) {
			return errSafety
		}
		if err := publishServerProfile(p, cert, key); err != nil {
			return err
		}
	}
	if err := prepareServerState(p); err != nil {
		return err
	}
	target := "generations/" + p.Release.Version
	got, err := os.Readlink(serverCurrent)
	if errors.Is(err, os.ErrNotExist) {
		if err := prepareServerGeneration(stage, target); err != nil {
			return err
		}
		if os.Symlink(target, serverCurrent) != nil || syncDir(serverBase) != nil {
			return errSafety
		}
	} else if err != nil || !hasExisting || got != target {
		return errServerInstalled
	}
	if err := syncDir(serverBase); err != nil {
		return errSafety
	}
	if err := p.control("start"); err != nil {
		if p.control("stop") != nil || removePublished(serverCurrent) != nil {
			return errors.New("server start failed and its installation needs local recovery")
		}
		return err
	}
	if err := writeFile(serverInit, []byte(serverInitScript), 0755, 0, 0); err != nil {
		if p.control("stop") == nil {
			_ = errors.Join(removePublished(serverInit), removePublished(serverCurrent))
		}
		return errors.New("cannot publish server init registration")
	}
	fmt.Println("Entware server installation ready")
	return nil
}

func prepareServerState(p serverProfile) error {
	info, err := os.Lstat(serverState)
	if errors.Is(err, os.ErrNotExist) {
		stage, stageErr := os.MkdirTemp(stateBase, ".server-state-")
		if stageErr != nil {
			return errSafety
		}
		defer os.RemoveAll(stage)
		if os.Chown(stage, int(p.UID), int(p.GID)) != nil || syncDir(stage) != nil || os.Rename(stage, serverState) != nil {
			return errSafety
		}
		info, err = os.Lstat(serverState)
	}
	if err != nil || !owned(info, p.UID, os.ModeDir, 0700) || info.Sys().(*syscall.Stat_t).Gid != p.GID || syncDir(stateBase) != nil {
		return errSafety
	}
	return nil
}

func prepareServerGeneration(stage, target string) error {
	path := filepath.Join(serverBase, target)
	if err := rootDirs(true, path); err != nil {
		return err
	}
	for _, asset := range []string{installerAsset, serverAsset} {
		raw, err := readProtected(filepath.Join(stage, asset), 0, 64<<20)
		if err != nil || len(raw) == 0 {
			return errSafety
		}
		if err := writeFile(filepath.Join(path, asset), raw, 0755, 0, 0); err != nil {
			return err
		}
	}
	return nil
}

func publishServerProfile(p serverProfile, cert, key []byte) error {
	if err := writeFile(serverCertPath, cert, 0644, 0, 0); err != nil {
		return err
	}
	if err := writeFile(serverKeyPath, key, 0640, 0, int(p.GID)); err != nil {
		return err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return errSafety
	}
	return writeFile(serverProfilePath, raw, 0600, 0, 0)
}

func loadServerProfile() (serverProfile, error) {
	raw, err := readProtected(serverProfilePath, 0600, 8192)
	if err != nil {
		return serverProfile{}, err
	}
	var p serverProfile
	if jsonv2.Unmarshal(raw, &p, jsonv2.RejectUnknownMembers(true)) != nil || !validRelease(p.Release) ||
		p.UID == 0 || p.GID == 0 || !shaPattern.MatchString(p.CertDigest) || !shaPattern.MatchString(p.KeyDigest) {
		return serverProfile{}, errSafety
	}
	listen, err := netip.ParseAddrPort(p.Listen)
	origin, originErr := server.ParseWebOrigin(p.Origin)
	if err != nil || listen.Port() == 0 || listen.Addr().Zone() != "" || originErr != nil || string(origin) != p.Origin {
		return serverProfile{}, errSafety
	}
	return p, nil
}

func serverService(args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	lock, err := lockRole("server")
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := rootDirs(false, serverConfig, serverBase, serverBase+"/generations"); err != nil {
		return err
	}
	p, err := loadServerProfile()
	if err != nil {
		return err
	}
	switch args[0] {
	case "start", "stop", "status":
		if len(args) != 1 {
			return errUsage
		}
		return p.control(args[0])
	case "admin":
		return p.admin(args[1:])
	default:
		return errUsage
	}
}

func (p serverProfile) binary() (string, error) {
	want := "generations/" + p.Release.Version
	got, err := os.Readlink(serverCurrent)
	if err != nil || got != want {
		return "", errSafety
	}
	dir := filepath.Join(serverBase, want)
	if err := rootDirs(false, dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, serverAsset), nil
}

func (p serverProfile) control(action string) error {
	binary, err := p.binary()
	if err != nil {
		return err
	}
	process := managedProcess{binary: binary, receipt: serverReceipt, uid: p.UID, gid: p.GID, notReady: errServerReady,
		arguments: []string{"--listen", p.Listen, "--db-path", serverDB, "--tls-cert", serverCertPath, "--tls-key", serverKeyPath, "--web-origin", p.Origin}}
	if action != "stop" {
		cert, err := p.installedCertificate()
		if err != nil {
			return err
		}
		probe, stop := serverReadyCheck(p, cert)
		defer stop()
		process.probe = probe
	}
	return process.control(action)
}

func (p serverProfile) installedCertificate() (*x509.Certificate, error) {
	cert, err := readProtected(serverCertPath, 0644, 1<<20)
	if err != nil || digest(cert) != p.CertDigest {
		return nil, errServerTLS
	}
	keyFile, err := checkedFile(serverKeyPath, 0, 0640)
	if err != nil {
		return nil, errServerTLS
	}
	defer keyFile.Close()
	info, err := keyFile.Stat()
	if err != nil || info.Sys().(*syscall.Stat_t).Gid != p.GID {
		return nil, errServerTLS
	}
	key, err := readBounded(keyFile, 1<<20)
	defer clear(key)
	if err != nil || digest(key) != p.KeyDigest {
		return nil, errServerTLS
	}
	return validateServerCert(cert, key, p.Origin)
}

func validateServerCert(cert, key []byte, origin string) (*x509.Certificate, error) {
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil || pair.Leaf == nil {
		return nil, errServerTLS
	}
	target, err := url.Parse(origin)
	now := time.Now()
	if err != nil || pair.Leaf.VerifyHostname(target.Hostname()) != nil || now.Before(pair.Leaf.NotBefore) || now.After(pair.Leaf.NotAfter) {
		return nil, errServerTLS
	}
	return pair.Leaf, nil
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func serverReadyCheck(p serverProfile, expected *x509.Certificate) (func(int) bool, func()) {
	listen := netip.MustParseAddrPort(p.Listen)
	dial := listen
	if listen.Addr().IsUnspecified() {
		host := netip.IPv6Loopback()
		if listen.Addr().Is4() {
			host = netip.MustParseAddr("127.0.0.1")
		}
		dial = netip.AddrPortFrom(host, listen.Port())
	}
	roots := x509.NewCertPool()
	roots.AddCert(expected)
	dialer := net.Dialer{Timeout: 700 * time.Millisecond}
	transport := &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12, RootCAs: roots,
		VerifyConnection: func(connection tls.ConnectionState) error {
			if len(connection.PeerCertificates) == 0 || !bytes.Equal(connection.PeerCertificates[0].Raw, expected.Raw) {
				return errServerTLS
			}
			return nil
		},
	}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, dial.String())
	}}
	client := &http.Client{Transport: transport, Timeout: 700 * time.Millisecond,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(pid int) bool {
		if !linuxlistener.Owns(pid, listen) {
			return false
		}
		reply, err := client.Get(p.Origin + "/healthz")
		if err != nil {
			return false
		}
		defer reply.Body.Close()
		return reply.StatusCode == http.StatusOK && linuxlistener.Owns(pid, listen)
	}, client.CloseIdleConnections
}

func (p serverProfile) admin(args []string) error {
	if len(args) != 1 && len(args) != 3 {
		return errUsage
	}
	if args[0] != "setup" && args[0] != "reset" {
		return errUsage
	}
	suffix := []string{"admin", args[0], "--db-path", serverDB}
	if len(args) == 3 {
		if args[1] != "--password-file" || !filepath.IsAbs(args[2]) {
			return errUsage
		}
		suffix = append(suffix, args[1:]...)
	}
	binary, err := p.binary()
	if err != nil {
		return err
	}
	file, err := checkedFile(binary, 0, 0755)
	if err != nil {
		return errSafety
	}
	file.Close()
	cmd := exec.Command(binary, suffix...)
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/opt/bin:/opt/sbin:/usr/sbin:/usr/bin:/sbin:/bin", "TERM=" + os.Getenv("TERM")}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: p.UID, Gid: p.GID, Groups: []uint32{}}}
	if err := cmd.Run(); err != nil {
		return errors.New("local administrator command failed")
	}
	return nil
}

const serverInitScript = `#!/opt/bin/sh
set -eu
[ "$#" -ge 1 ] || exit 2
case "$1" in
  start|stop|status|check)
    [ "$#" -le 2 ] || exit 2
    action=$1
    [ "$action" != check ] || action=status
    set -- "$action"
    ;;
  admin) ;;
  *) exit 2 ;;
esac
exec /opt/libexec/xkeen-ui/server/current/xkeen-entware-installer-linux-arm64 server-service "$@"
`
