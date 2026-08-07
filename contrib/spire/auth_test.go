package spire_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/rushteam/beauty/contrib/spire"
	"github.com/rushteam/beauty/pkg/authz"
	"github.com/rushteam/beauty/pkg/middleware/auth"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestAuthorizeID(t *testing.T) {
	a, err := spire.AuthorizeID("spiffe://example.org/client")
	if err != nil {
		t.Fatal(err)
	}
	id := spiffeid.RequireFromString("spiffe://example.org/client")
	if err := a(id, nil); err != nil {
		t.Fatalf("expected allow: %v", err)
	}
	other := spiffeid.RequireFromString("spiffe://example.org/other")
	if err := a(other, nil); err == nil {
		t.Fatal("expected deny")
	}
}

func TestAuthorizeIDInvalid(t *testing.T) {
	if _, err := spire.AuthorizeID("not-a-spiffe-id"); err == nil {
		t.Fatal("expected error")
	}
}

func TestUserAndSubjectFromID(t *testing.T) {
	id := spiffeid.RequireFromString("spiffe://example.org/ns/foo/sa/bar")
	u := spire.UserFromID(id, "workload")
	if u.ID() != id.String() || !u.HasRole("workload") {
		t.Fatalf("user=%+v", u)
	}
	sub := spire.SubjectFromID(id, "workload")
	if sub.ID != id.String() || sub.Attrs["path"] != "/ns/foo/sa/bar" || sub.Attrs["trust_domain"] != "example.org" {
		t.Fatalf("subject=%+v", sub)
	}
}

func TestPeerIDFromContextTLSInfo(t *testing.T) {
	want := "spiffe://example.org/client"
	u, err := url.Parse(want)
	if err != nil {
		t.Fatal(err)
	}
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{SPIFFEID: u},
	})
	id, err := spire.PeerIDFromContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if id.String() != want {
		t.Fatalf("got %s", id)
	}
}

func TestPeerIDFromContextMissing(t *testing.T) {
	_, err := spire.PeerIDFromContext(context.Background())
	if !errors.Is(err, spire.ErrNoPeerID) {
		t.Fatalf("got %v", err)
	}
}

func TestPeerIDFromRequest(t *testing.T) {
	cert := mustSPIFFECert(t, "spiffe://example.org/web-client")
	req := httptest.NewRequest(http.MethodGet, "https://svc/api", nil)
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	id, err := spire.PeerIDFromRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if id.String() != "spiffe://example.org/web-client" {
		t.Fatalf("got %s", id)
	}
}

func TestUnaryServerInterceptor(t *testing.T) {
	want := "spiffe://example.org/client"
	u, _ := url.Parse(want)
	ctx := peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{SPIFFEID: u},
	})

	interceptor := spire.UnaryServerInterceptor(spire.WithRoles("svc"), spire.WithAuthzSubject())
	var gotUser auth.User
	var gotSub authz.Subject
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/pkg.Svc/Ping"},
		func(ctx context.Context, _ any) (any, error) {
			var ok bool
			gotUser, ok = auth.GetUserFromContext(ctx)
			if !ok {
				t.Fatal("missing user")
			}
			gotSub, ok = authz.SubjectFromContext(ctx)
			if !ok {
				t.Fatal("missing subject")
			}
			return "ok", nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if gotUser.ID() != want || !gotUser.HasRole("svc") {
		t.Fatalf("user=%v", gotUser)
	}
	if gotSub.ID != want {
		t.Fatalf("subject=%v", gotSub)
	}
}

func TestUnaryServerInterceptorUnauthenticated(t *testing.T) {
	interceptor := spire.UnaryServerInterceptor()
	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/pkg.Svc/Ping"},
		func(context.Context, any) (any, error) { return nil, nil })
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("got %v", err)
	}
}

func TestUnaryServerInterceptorSkip(t *testing.T) {
	interceptor := spire.UnaryServerInterceptor(spire.WithSkipPaths("/pkg.Svc/Health"))
	called := false
	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/pkg.Svc/Health"},
		func(context.Context, any) (any, error) {
			called = true
			return nil, nil
		})
	if err != nil || !called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestHTTPMiddleware(t *testing.T) {
	cert := mustSPIFFECert(t, "spiffe://example.org/http-client")
	h := spire.HTTPMiddleware(spire.WithRoles("http"), spire.WithAuthzSubject())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth.GetUserFromContext(r.Context())
		if !ok || u.ID() != "spiffe://example.org/http-client" {
			t.Fatalf("user=%v ok=%v", u, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "https://svc/api", nil)
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestHTTPMiddlewareUnauthorized(t *testing.T) {
	h := spire.HTTPMiddleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("should not reach handler")
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "https://svc/api", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rr.Code)
	}
}

func mustSPIFFECert(t *testing.T, id string) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse(id)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		URIs:         []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
