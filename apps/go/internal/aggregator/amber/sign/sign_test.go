package sign

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

var (
	fixedTime  = time.Date(2026, 2, 27, 12, 0, 0, 0, time.UTC)
	fixedNonce = "11111111-2222-4333-8444-555555555555"
	creds      = Credentials{AccessID: "test-id", Secret: "test-secret"}
)

func fixedSigner() *Signer {
	return New(creds, WithClock(func() time.Time { return fixedTime }), WithNonce(func() string { return fixedNonce }))
}

// Expected signatures were computed outside Go with:
//
//	printf '%s' "$PLAINTEXT" | openssl dgst -sha256 -hmac 'test-secret' -binary | base64
func TestHeadersMatchIndependentVector(t *testing.T) {
	tests := []struct {
		name, method, path string
		query              url.Values
		wantPlain, wantSig string
	}{
		{
			name: "GET with unsorted query", method: "get", path: "/v1/assets",
			query:     url.Values{"coin": {"60"}, "assetId": {"c60"}},
			wantPlain: "GET;/v1/assets;assetId=c60&coin=60;test-id;" + fixedNonce + ";Fri, 27 Feb 2026 12:00:00 GMT",
			wantSig:   "YVQz5DSlQQLYxjFgaJaVt3nNFulHBuCe9RN2AFB83hY=",
		},
		{
			name: "POST without query", method: "POST", path: "/amber-api/v1/route",
			wantPlain: "POST;/amber-api/v1/route;;test-id;" + fixedNonce + ";Fri, 27 Feb 2026 12:00:00 GMT",
			wantSig:   "p9dHXl+SkWS3WWgBkS2VLE4u0ZhvAraMPYNwvtc40T0=",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := fixedSigner().Headers(tc.method, tc.path, tc.query)
			if got := plaintext(tc.method, tc.path, tc.query, creds.AccessID, fixedNonce, h[HeaderDate]); got != tc.wantPlain {
				t.Fatalf("plaintext\n got %q\nwant %q", got, tc.wantPlain)
			}
			if got := h[HeaderAuthorization]; got != authPrefix+tc.wantSig {
				t.Fatalf("authorization = %q, want %q", got, authPrefix+tc.wantSig)
			}
			if h[HeaderCredential] != creds.AccessID || h[HeaderNonce] != fixedNonce || h[HeaderDate] != "Fri, 27 Feb 2026 12:00:00 GMT" {
				t.Fatalf("unexpected headers: %v", h)
			}
		})
	}
}

func TestVerify(t *testing.T) {
	q := url.Values{"b": {"2"}, "a": {"1"}}
	toHeader := func(m map[string]string) http.Header {
		h := http.Header{}
		for k, v := range m {
			h.Set(k, v)
		}
		return h
	}
	good := toHeader(fixedSigner().Headers("GET", "/x", q))

	if err := Verify(good, "GET", "/x", q, creds, fixedTime.Add(time.Minute), 5*time.Minute); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	cases := map[string]func() error{
		"wrong secret": func() error {
			return Verify(good, "GET", "/x", q, Credentials{AccessID: "test-id", Secret: "other"}, fixedTime, time.Minute)
		},
		"wrong path":  func() error { return Verify(good, "GET", "/y", q, creds, fixedTime, time.Minute) },
		"stale date":  func() error { return Verify(good, "GET", "/x", q, creds, fixedTime.Add(time.Hour), time.Minute) },
		"wrong query": func() error { return Verify(good, "GET", "/x", url.Values{"a": {"9"}}, creds, fixedTime, time.Minute) },
	}
	for name, f := range cases {
		if f() == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestNonceIsUUIDv4AndUnique(t *testing.T) {
	a, b := newUUID(), newUUID()
	if a == b || len(a) != 36 || a[14] != '4' {
		t.Fatalf("bad uuids %q %q", a, b)
	}
}

func TestSecretNeverPrinted(t *testing.T) {
	var buf strings.Builder
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "creds", creds)
	for _, s := range []string{fmt.Sprint(creds), fmt.Sprintf("%v %+v %#v", creds, creds, creds), buf.String()} {
		if strings.Contains(s, creds.Secret) {
			t.Fatalf("secret leaked in %q", s)
		}
	}
}
