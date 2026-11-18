package botguard

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rushteam/beauty/pkg/store/kvstore"
)

const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

var masterKey = bytes.Repeat([]byte{7}, 32)

func newGuard(t *testing.T, opts ...Option) *Guard {
	t.Helper()
	store := kvstore.NewMemory()
	t.Cleanup(store.Stop)
	g, err := New(store, masterKey, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func ok200(w http.ResponseWriter, r *http.Request) {
	if v, ok := FromContext(r.Context()); ok {
		w.Header().Set("X-Score", strings.Join(v.Reasons, ","))
	}
	w.WriteHeader(http.StatusOK)
}

func issue(t *testing.T, g *Guard, fp string) Grant {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/_botguard/token", nil)
	grant, err := g.IssueToken(req, fp)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func humanPayload(fp string) Payload {
	return Payload{
		TS:          time.Now().UnixMilli(),
		Fingerprint: fp,
		Behavior:    Behavior{Moves: 80, Clicks: 2, DwellMs: 5000, Straightness: 620, SpeedCV: 700},
	}
}

func signed(t *testing.T, method, target, body string, grant Grant, p Payload) *http.Request {
	t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	var req *http.Request
	if rd != nil {
		req = httptest.NewRequest(method, target, rd)
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	req.Header.Set("User-Agent", browserUA)
	if err := SignRequest(req, grant, p); err != nil {
		t.Fatal(err)
	}
	return req
}

func serve(g *Guard, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	g.Middleware()(http.HandlerFunc(ok200)).ServeHTTP(rec, req)
	return rec
}

func TestHumanRequestAllowedAndBodyPreserved(t *testing.T) {
	g := newGuard(t)
	grant := issue(t, g, "fp1")
	req := signed(t, http.MethodPost, "/api/items?b=2&a=1&a=0", `{"x":1}`, grant, humanPayload("fp1"))

	var got []byte
	h := g.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		got = buf.Bytes()
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	if string(got) != `{"x":1}` {
		t.Fatalf("body not restored: %q", got)
	}
}

func TestTamperingRejected(t *testing.T) {
	g := newGuard(t)
	grant := issue(t, g, "fp1")
	cases := map[string]func(r *http.Request){
		"query": func(r *http.Request) { r.URL.RawQuery = "a=999" },
		"path":  func(r *http.Request) { r.URL.Path = "/api/admin" },
		"body":  func(r *http.Request) { r.Body = http.NoBody },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			req := signed(t, http.MethodPost, "/api/items?a=1", `{"x":1}`, grant, humanPayload("fp1"))
			mutate(req)
			if rec := serve(g, req); rec.Code != http.StatusForbidden {
				t.Fatalf("got %d, want 403", rec.Code)
			}
		})
	}
}

func TestReplayRejected(t *testing.T) {
	g := newGuard(t)
	grant := issue(t, g, "fp1")
	req := signed(t, http.MethodGet, "/api/items", "", grant, humanPayload("fp1"))
	sig := req.Header.Get("X-Guard-Sign")

	if rec := serve(g, req); rec.Code != http.StatusOK {
		t.Fatalf("first: got %d", rec.Code)
	}
	again := httptest.NewRequest(http.MethodGet, "/api/items", nil)
	again.Header.Set("User-Agent", browserUA)
	again.Header.Set("X-Guard-Token", grant.Token)
	again.Header.Set("X-Guard-Sign", sig)
	if rec := serve(g, again); rec.Code != http.StatusForbidden {
		t.Fatalf("replay: got %d, want 403", rec.Code)
	}
}

func TestTokenAndTimestampFailuresAskForRefresh(t *testing.T) {
	g := newGuard(t)
	grant := issue(t, g, "fp1")

	stale := humanPayload("fp1")
	stale.TS -= (2 * time.Minute).Milliseconds()
	reqs := map[string]*http.Request{
		"stale_ts": signed(t, http.MethodGet, "/x", "", grant, stale),
		"no_token": httptest.NewRequest(http.MethodGet, "/x", nil),
		"bad_token": func() *http.Request {
			r := signed(t, http.MethodGet, "/x", "", grant, humanPayload("fp1"))
			r.Header.Set("X-Guard-Token", "AAAA"+grant.Token[4:])
			return r
		}(),
	}
	for name, req := range reqs {
		t.Run(name, func(t *testing.T) {
			rec := serve(g, req)
			if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "token_required") {
				t.Fatalf("got %d %s", rec.Code, rec.Body)
			}
		})
	}

	short := newGuard(t, WithTokenTTL(-time.Second))
	expired := issue(t, short, "fp1")
	v := short.evaluate(signed(t, http.MethodGet, "/x", "", expired, humanPayload("fp1")), time.Now())
	if v.Failure != FailTokenExpired {
		t.Fatalf("got %q", v.Failure)
	}
}

func TestSessionKeyBoundToToken(t *testing.T) {
	g := newGuard(t)
	a, b := issue(t, g, "fp1"), issue(t, g, "fp1")
	req := signed(t, http.MethodGet, "/x", "", Grant{Token: a.Token, Key: b.Key}, humanPayload("fp1"))
	if rec := serve(g, req); rec.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", rec.Code)
	}
}

func TestHighRiskBlocked(t *testing.T) {
	g := newGuard(t)
	grant := issue(t, g, "fp1")
	p := humanPayload("fp1")
	p.Flags = FlagWebdriver | FlagHeadlessUA
	rec := serve(g, signed(t, http.MethodGet, "/x", "", grant, p))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "denied") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestBotUserAgentDetectedServerSide(t *testing.T) {
	g := newGuard(t)
	grant := issue(t, g, "fp1")
	req := signed(t, http.MethodGet, "/x", "", grant, humanPayload("fp1"))
	req.Header.Set("User-Agent", "python-requests/2.31")
	v := g.evaluate(req, time.Now())
	if v.Failure != "" || v.Score < 60 {
		t.Fatalf("verdict %+v", v)
	}
}

func TestMediumRiskChallengeThenPoW(t *testing.T) {
	g := newGuard(t, WithPoW(8, time.Minute))
	grant := issue(t, g, "fp1")
	// 指纹与令牌不一致 → 40 分，触发挑战
	rec := serve(g, signed(t, http.MethodGet, "/x", "", grant, humanPayload("fp-other")))
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	var resp struct {
		Pow Challenge `json:"pow"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	solution := SolveChallenge(resp.Pow)

	req := signed(t, http.MethodGet, "/x", "", grant, humanPayload("fp-other"))
	req.Header.Set("X-Guard-PoW", solution)
	if rec := serve(g, req); rec.Code != http.StatusOK {
		t.Fatalf("with pow: got %d %s", rec.Code, rec.Body)
	}
	// 通过后令牌在 passTTL 内免挑战
	if rec := serve(g, signed(t, http.MethodGet, "/x", "", grant, humanPayload("fp-other"))); rec.Code != http.StatusOK {
		t.Fatalf("after pass: got %d", rec.Code)
	}
}

func TestPoWRejectsForgedAndReused(t *testing.T) {
	g := newGuard(t, WithPoW(8, time.Minute))
	ctx := t.Context()
	now := time.Now()
	c := g.NewChallenge("tok-a")
	sol := SolveChallenge(c)

	if g.verifyPoW(ctx, sol, "tok-b", now) {
		t.Fatal("challenge accepted for another token")
	}
	if !g.verifyPoW(ctx, sol, "tok-a", now) {
		t.Fatal("valid solution rejected")
	}
	if g.verifyPoW(ctx, sol, "tok-a", now) {
		t.Fatal("solution reused")
	}
	forged := c
	forged.Bits = 0
	enc, _, _ := strings.Cut(c.Challenge, ".")
	forged.Challenge = enc + ".AAAAAAAAAAAAAAAAAAAAAA"
	if g.verifyPoW(ctx, SolveChallenge(forged), "tok-a", now) {
		t.Fatal("forged challenge accepted")
	}
}

func TestDeceiveHandler(t *testing.T) {
	honeypot := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	g := newGuard(t, WithDeceive(honeypot))
	grant := issue(t, g, "fp1")
	p := humanPayload("fp1")
	p.Flags = FlagWebdriver | FlagNativeTampered
	rec := serve(g, signed(t, http.MethodGet, "/x", "", grant, p))
	if rec.Code != http.StatusOK || rec.Body.String() != `{"items":[]}` {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestMonitorAndOffModes(t *testing.T) {
	var seen *Verdict
	g := newGuard(t, WithOnVerdict(func(_ *http.Request, v *Verdict) { seen = v }))
	g.SetMode(ModeMonitor)
	if rec := serve(g, httptest.NewRequest(http.MethodGet, "/x", nil)); rec.Code != http.StatusOK {
		t.Fatalf("monitor: got %d", rec.Code)
	}
	if seen == nil || seen.Failure != FailTokenMissing {
		t.Fatalf("monitor verdict %+v", seen)
	}
	seen = nil
	g.SetMode(ModeOff)
	if rec := serve(g, httptest.NewRequest(http.MethodGet, "/x", nil)); rec.Code != http.StatusOK || seen != nil {
		t.Fatalf("off: got %d, verdict %+v", rec.Code, seen)
	}
}

func TestFingerprintRateLimit(t *testing.T) {
	g := newGuard(t, WithFingerprintRateLimit(3), WithThresholds(30, 70))
	grant := issue(t, g, "fp1")
	var last *httptest.ResponseRecorder
	for range 4 {
		last = serve(g, signed(t, http.MethodGet, "/x", "", grant, humanPayload("fp1")))
	}
	if last.Code != http.StatusPreconditionRequired {
		t.Fatalf("got %d", last.Code)
	}
}

func TestKeyRotation(t *testing.T) {
	old := newGuard(t)
	grant := issue(t, old, "fp1")

	newKey := bytes.Repeat([]byte{9}, 32)
	store := kvstore.NewMemory()
	t.Cleanup(store.Stop)
	g, err := New(store, newKey, WithPreviousKeys(masterKey))
	if err != nil {
		t.Fatal(err)
	}
	if rec := serve(g, signed(t, http.MethodGet, "/x", "", grant, humanPayload("fp1"))); rec.Code != http.StatusOK {
		t.Fatalf("old token after rotation: got %d %s", rec.Code, rec.Body)
	}
}

func TestTokenHandler(t *testing.T) {
	g := newGuard(t)
	rec := httptest.NewRecorder()
	g.TokenHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/t", strings.NewReader(`{"fp":"abc"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	var resp struct {
		Token string `json:"token"`
		Key   string `json:"key"`
		Exp   int64  `json:"exp"`
		Now   int64  `json:"now"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	key, err := base64.RawURLEncoding.DecodeString(resp.Key)
	if err != nil || len(key) != 32 || resp.Token == "" || resp.Exp <= time.Now().Unix() {
		t.Fatalf("bad grant %+v", resp)
	}
	claims, _, err := g.openToken(resp.Token)
	if err != nil || claims.Fingerprint != "abc" || claims.IPNet != "192.0.2.0/24" {
		t.Fatalf("claims %+v err %v", claims, err)
	}
}

func TestCanonicalDigestMatchesJSRules(t *testing.T) {
	q, _ := url.ParseQuery("z=1&a=hello+world&a=%E4%B8%AD&k=*~'()!")
	got := CanonicalDigest("post", "/a b", q, nil)
	want := CanonicalDigest("POST", "/a b", url.Values{"k": {"*~'()!"}, "a": {"中", "hello world"}, "z": {"1"}}, []byte{})
	if !bytes.Equal(got, want) {
		t.Fatal("digest should be order-insensitive and method case-insensitive")
	}
	if s := uriComponent("a b/中*~"); s != "a%20b%2F%E4%B8%AD*~" {
		t.Fatalf("uriComponent = %s", s)
	}
}

// xorMACCipher 是测试用魔改算法：明文按密钥异或，尾部 32 字节是 HMAC-SHA256(aad||明文)。
type xorMACCipher struct{}

func (xorMACCipher) ID() string { return "x1" }

func (xorMACCipher) Seal(key, plaintext, aad []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, errors.New("empty key")
	}
	out := make([]byte, len(plaintext), len(plaintext)+sha256.Size)
	for i := range plaintext {
		out[i] = plaintext[i] ^ key[i%len(key)]
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(aad)
	mac.Write(plaintext)
	return mac.Sum(out), nil
}

func (xorMACCipher) Open(key, sealed, aad []byte) ([]byte, error) {
	if len(key) == 0 || len(sealed) < sha256.Size {
		return nil, errors.New("bad seal")
	}
	body, tag := sealed[:len(sealed)-sha256.Size], sealed[len(sealed)-sha256.Size:]
	plain := make([]byte, len(body))
	for i := range body {
		plain[i] = body[i] ^ key[i%len(key)]
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(aad)
	mac.Write(plain)
	if !hmac.Equal(mac.Sum(nil), tag) {
		return nil, errors.New("bad seal")
	}
	return plain, nil
}

type badIDCipher struct{}

func (badIDCipher) ID() string                          { return "has.dot" }
func (badIDCipher) Seal(_, _, _ []byte) ([]byte, error) { return nil, nil }
func (badIDCipher) Open(_, _, _ []byte) ([]byte, error) { return nil, nil }

func TestCustomCipherAcceptedAlongsideV1(t *testing.T) {
	g := newGuard(t, WithCipher(xorMACCipher{}), WithAcceptCiphers(AESGCM))
	grant := issue(t, g, "fp1")

	req := httptest.NewRequest(http.MethodPost, "/api/items?a=1", strings.NewReader(`{"x":1}`))
	req.Header.Set("User-Agent", browserUA)
	if err := g.SignRequest(req, grant, humanPayload("fp1")); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(req.Header.Get("X-Guard-Sign"), "x1.") {
		t.Fatalf("sign = %s", req.Header.Get("X-Guard-Sign"))
	}
	if rec := serve(g, req); rec.Code != http.StatusOK {
		t.Fatalf("custom cipher: got %d %s", rec.Code, rec.Body)
	}

	legacy := signed(t, http.MethodGet, "/api/items", "", grant, humanPayload("fp1"))
	if !strings.HasPrefix(legacy.Header.Get("X-Guard-Sign"), "v1.") {
		t.Fatal("package SignRequest should stay on v1")
	}
	if rec := serve(g, legacy); rec.Code != http.StatusOK {
		t.Fatalf("accepted v1: got %d %s", rec.Code, rec.Body)
	}

	unknown := signed(t, http.MethodGet, "/api/items", "", grant, humanPayload("fp1"))
	unknown.Header.Set("X-Guard-Sign", "zz."+unknown.Header.Get("X-Guard-Sign")[3:])
	if rec := serve(g, unknown); rec.Code != http.StatusForbidden {
		t.Fatalf("unknown cipher: got %d", rec.Code)
	}
}

func TestCipherRegistration(t *testing.T) {
	store := kvstore.NewMemory()
	defer store.Stop()
	if _, err := New(store, masterKey, WithCipher(badIDCipher{})); err == nil {
		t.Fatal("invalid id accepted")
	}
	if _, err := New(store, masterKey, WithCipher(xorMACCipher{}), WithAcceptCiphers(xorMACCipher{})); err == nil {
		t.Fatal("duplicate id accepted")
	}
	if _, err := New(store, masterKey, WithAcceptCiphers(nil)); err == nil {
		t.Fatal("nil cipher accepted")
	}
}

func TestSkipPrefixes(t *testing.T) {
	g := newGuard(t, WithSkipPrefixes("/healthz"))
	if rec := serve(g, httptest.NewRequest(http.MethodGet, "/healthz", nil)); rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
}
