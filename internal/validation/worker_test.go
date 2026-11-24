package validation

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SunnyDayDev/xkeen-ui-server/internal/application/publication"
	"github.com/SunnyDayDev/xkeen-ui-server/internal/configuration"
)

type execFunc func(context.Context, request) error

func (f execFunc) check(c context.Context, r request) error { return f(c, r) }

type pki struct {
	root *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func testPKI(t *testing.T) pki {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour)}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	ca, e = x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return pki{ca, key, pool}
}
func (p pki) issue(t *testing.T, usage x509.ExtKeyUsage) (tls.Certificate, string) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	if e != nil {
		t.Fatal(e)
	}
	leaf := &x509.Certificate{SerialNumber: serial, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"host.docker.internal"}, NotBefore: p.root.NotBefore, NotAfter: p.root.NotAfter, ExtKeyUsage: []x509.ExtKeyUsage{usage}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, leaf, p.root, &key.PublicKey, p.key)
	if e != nil {
		t.Fatal(e)
	}
	digest := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, hex.EncodeToString(digest[:])
}
func tlsService(t *testing.T, e executor) (string, pki, tls.Certificate) {
	t.Helper()
	p := testPKI(t)
	server, _ := p.issue(t, x509.ExtKeyUsageServerAuth)
	client, pin := p.issue(t, x509.ExtKeyUsageClientAuth)
	cfg, err := WorkerTLS(server, p.pool, pin)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(newHandler(e))
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.TLS = cfg
	s.StartTLS()
	t.Cleanup(s.Close)
	return s.URL, p, client
}
func TestRemoteTrustAndPrivacy(t *testing.T) {
	const marker = "synthetic-test-only-secret"
	var calls atomic.Int32
	addr, p, allowed := tlsService(t, execFunc(func(_ context.Context, r request) error {
		calls.Add(1)
		if !bytes.Contains(r.Files[0].Bytes, []byte(marker)) {
			t.Error("missing exact test marker")
		}
		return fmt.Errorf("private runner detail %s", marker)
	}))
	files := sample()
	files[0].Content = []byte(fmt.Sprintf("{\"log\":{\"marker\":%q}}", marker))
	wrong, _ := p.issue(t, x509.ExtKeyUsageClientAuth)
	for _, test := range []struct {
		pair tls.Certificate
		pool *x509.CertPool
	}{{allowed, p.pool}, {wrong, p.pool}, {allowed, testPKI(t).pool}} {
		c, err := Remote(addr, &tls.Config{Certificates: []tls.Certificate{test.pair}, RootCAs: test.pool})
		if err != nil {
			t.Fatal(err)
		}
		got := c.Validate(context.Background(), configuration.XrayIdentityVersion, files)
		if got != publication.ErrValidatorUnavailable || strings.Contains(got.Error(), marker) {
			t.Fatal("unsafe or incorrect error")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("unexpected deliveries", calls.Load())
	}
}
func TestStrictMessageAndBounds(t *testing.T) {
	for _, raw := range []string{`{"Attempt":"a","Attempt":"b"}`, `{"ATTEMPT":"a","Attempt":"b"}`, `{"Unexpected":"x"}`, `{} {}`, strings.Repeat(" ", 129)} {
		var v response
		if readMessage(strings.NewReader(raw), &v, 128) {
			t.Fatal("accepted malformed response")
		}
	}
}
func TestRejectsCorruptManifestBeforeExecution(t *testing.T) {
	req, _ := newRequest(configuration.XrayIdentityVersion, sample())
	req.Files[0].Bytes[0] = ' '
	data, _ := json.Marshal(req)
	w := httptest.NewRecorder()
	newHandler(execFunc(func(context.Context, request) error { t.Fatal("ran invalid job"); return nil })).ServeHTTP(w, httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(data)))
	if w.Code != http.StatusBadRequest {
		t.Fatal(w.Code)
	}
}
func TestDockerRealCatalog(t *testing.T) {
	image := os.Getenv("XKEEN_TEST_VALIDATOR_IMAGE")
	if image == "" {
		t.Skip("run lab/check-validator.sh")
	}
	binary := os.Getenv("XKEEN_TEST_DOCKER")
	socket := os.Getenv("XKEEN_TEST_ENGINE")
	base := os.Getenv("XKEEN_TEST_FIXTURES")
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	w, err := NewDockerWorker(binary, socket, image, dir)
	if err != nil {
		t.Fatal("worker setup", err)
	}
	defer w.Close()
	addr, p, cert := tlsService(t, w)
	v, err := Remote(addr, &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: p.pool})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"positive", "negative"} {
		entries, err := os.ReadDir(filepath.Join(base, name))
		if err != nil {
			t.Fatal(err)
		}
		var files []configuration.CatalogFile
		for _, entry := range entries {
			b, err := os.ReadFile(filepath.Join(base, name, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, configuration.CatalogFile{Name: entry.Name(), Content: b})
		}
		err = v.Validate(context.Background(), configuration.XrayIdentityVersion, files)
		if (name == "positive" && err != nil) || (name == "negative" && !errors.Is(err, publication.ErrCatalogRejected)) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Reap a private scratch directory left by an interrupted worker before
	// reporting a fresh result. No secret marker survives completion.
	stale := filepath.Join(dir, "job-00000000000000000000000000000000")
	if err := os.Mkdir(stale, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "synthetic-canary"), []byte("synthetic-private-marker"), 0600); err != nil {
		t.Fatal(err)
	}
	files := sample()
	files[0].Content = []byte(`{"log":{"access":"/opt/var/log/xray/access.log","error":"/opt/var/log/xray/error.log","loglevel":"warning","fixture":"synthetic-private-marker"}}`)
	if err := v.Validate(context.Background(), configuration.XrayIdentityVersion, files); err != nil {
		t.Fatal("ephemeral logger", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "worker.lock" {
		t.Fatal("stale private files")
	}
	if os.Getenv("XKEEN_TEST_ENTWARE_BINARY") != "" {
		checkEntwareClient(t, binary, socket, addr, p, cert, base)
	}
}
func checkEntwareClient(t *testing.T, binary, socket, addr string, p pki, cert tls.Certificate, base string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "identity")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string]*pem.Block{"ca.pem": {Type: "CERTIFICATE", Bytes: p.root.Raw}, "client.pem": {Type: "CERTIFICATE", Bytes: cert.Certificate[0]}, "key.pem": {Type: "PRIVATE KEY", Bytes: key}} {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(b), 0444); err != nil {
			t.Fatal(err)
		}
	}
	// Only the deliberately generated credentials and synthetic catalog are mounted.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	network := "bridge"
	remote := strings.Replace(addr, "127.0.0.1", "host.docker.internal", 1)
	if os.Getenv("XKEEN_TEST_CLIENT_HOST_NET") == "1" {
		network = "host"
		remote = addr
	}
	args := []string{"--host", "unix://" + socket, "run", "--rm", "--pull=never", "--read-only", "--network", network, "--user", "65534:65534", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--log-driver", "none", "--mount", "type=bind,src=" + dir + ",dst=/identity,readonly", "--mount", "type=bind,src=" + filepath.Join(base, "positive") + ",dst=/catalog,readonly", "--mount", "type=bind,src=" + os.Getenv("XKEEN_TEST_ENTWARE_BINARY") + ",dst=/test,readonly", "-e", "XKEEN_TEST_REMOTE=" + remote, "--entrypoint", "/test", os.Getenv("XKEEN_TEST_ENTWARE_IMAGE"), "-test.run", "^TestEntwareClientProcess$", "-test.v"}
	output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("Entware client: %v %s", err, output)
	}
}
func TestEntwareClientProcess(t *testing.T) {
	addr := os.Getenv("XKEEN_TEST_REMOTE")
	if addr == "" {
		t.Skip("synthetic lab client only")
	}
	pair, err := tls.LoadX509KeyPair("/identity/client.pem", "/identity/key.pem")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("/identity/ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		t.Fatal("CA")
	}
	v, err := Remote(addr, &tls.Config{Certificates: []tls.Certificate{pair}, RootCAs: pool})
	if err != nil {
		t.Fatal(err)
	}
	var files []configuration.CatalogFile
	for _, name := range names {
		b, e := os.ReadFile("/catalog/" + name)
		if e != nil {
			t.Fatal(e)
		}
		files = append(files, configuration.CatalogFile{Name: name, Content: b})
	}
	if err = v.Validate(context.Background(), configuration.XrayIdentityVersion, files); err != nil {
		t.Fatal(err)
	}
}

func TestResourceLimitIsUnavailable(t *testing.T) {
	files := sample()
	files[0].Content = make([]byte, maxCatalog+1)
	if _, err := newRequest(configuration.XrayIdentityVersion, files); !errors.Is(err, publication.ErrValidatorUnavailable) {
		t.Fatal("resource limit misclassified", err)
	}
}
func TestLocalRequiresPrivateDirectory(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "validation-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "test.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := Local(socket); err == nil {
		t.Fatal("untrusted local listener accepted")
	}
}

func TestAvailabilityOnDiagnosticAmbiguity(t *testing.T) {
	for _, test := range []struct {
		text      string
		ambiguous bool
	}{
		{"Failed to start: main: failed to load config files: [/candidate/05_routing.json] > infra/conf: unknown config id: invalid-fixture", false},
		{"Failed to start: main: failed to create server > infra/conf: invalid IP address: synthetic", false},
		{"Failed to start: main: failed to load config files > open /candidate/05_routing.json: permission denied", true},
		{"Failed to start: main: failed to load config files > open /asset: no such file or directory", true},
		{"unrecognized validator problem", true},
	} {
		if got := ambiguousExit([]byte(test.text)); got != test.ambiguous {
			t.Errorf("classification=%v want=%v", got, test.ambiguous)
		}
	}
}
func TestLocalValidationAndRevocation(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "validator-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "validation.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	h := newHandler(execFunc(func(context.Context, request) error { return nil }))
	s := &http.Server{Handler: h}
	go s.Serve(ln)
	defer s.Close()
	v, err := Local(socket)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.Validate(context.Background(), configuration.XrayIdentityVersion, sample()); err != nil {
		t.Fatal(err)
	}
	// Reuse of pooled connections must not bypass a changed trust boundary on the next check.
	if err = os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if e := v.Validate(context.Background(), configuration.XrayIdentityVersion, sample()); !errors.Is(e, publication.ErrValidatorUnavailable) {
		t.Fatal("untrusted local directory used")
	}
}
func TestWorkerLimitsAndCancellation(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	addr, p, pair := tlsService(t, execFunc(func(ctx context.Context, r request) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}))
	v, err := Remote(addr, &tls.Config{Certificates: []tls.Certificate{pair}, RootCAs: p.pool})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- v.Validate(ctx, configuration.XrayIdentityVersion, sample()) }()
	select {
	case <-started:
	case <-time.After(time.Second * 3):
		t.Fatal("worker never started")
	}
	if e := v.Validate(context.Background(), configuration.XrayIdentityVersion, sample()); !errors.Is(e, publication.ErrValidatorUnavailable) {
		t.Fatal("worker accepted concurrent job")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not reach executor")
	}
	if e := <-done; !errors.Is(e, publication.ErrValidatorUnavailable) {
		t.Fatal("cancelled job returned success")
	}
}

func TestRuntimeSeesExactlyStagedBytes(t *testing.T) {
	dir := t.TempDir()
	req, err := newRequest(configuration.XrayIdentityVersion, sample())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range req.Files {
		if err := os.WriteFile(filepath.Join(dir, f.Name), f.Bytes, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if !VerifyStagedCatalog(dir, req.Digest) {
		t.Fatal("complete exact mount was rejected")
	}
	first := filepath.Join(dir, req.Files[0].Name)
	if err := os.WriteFile(first, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if VerifyStagedCatalog(dir, req.Digest) {
		t.Fatal("changed bytes in runtime mount were accepted")
	}
	if err := os.WriteFile(first, req.Files[0].Bytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if VerifyStagedCatalog(dir, req.Digest) {
		t.Fatal("additional runtime file accepted")
	}
}

func TestOutputQuotaCancelsAndDiscards(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &cappedOutput{cancel: cancel}
	if _, err := b.Write([]byte("safe prefix")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write(bytes.Repeat([]byte("synthetic-test-marker"), 8192)); err != nil {
		t.Fatal(err)
	}
	if !b.over || ctx.Err() == nil || b.data.String() != "safe prefix" {
		t.Fatal("unbounded process output")
	}
}
