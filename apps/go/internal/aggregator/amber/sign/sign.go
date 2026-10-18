// Package sign implements the HMAC-SHA256 request signing used by the Amber API.
//
// plaintext = METHOD;PATH;SORTED_QUERY;ACCESS_ID;NONCE;DATE
//
// SORTED_QUERY joins raw (not URL-encoded) query values sorted by key with "&". The request body is
// not part of the signature. The signature is base64(HMAC-SHA256(secret, plaintext)).
package sign

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Header names sent with every signed request.
const (
	HeaderCredential    = "X-TW-CREDENTIAL" //nolint:gosec // G101: a header name, not a credential
	HeaderNonce         = "X-TW-NONCE"
	HeaderDate          = "X-TW-DATE"
	HeaderAuthorization = "Authorization"
	authPrefix          = "HMAC-SHA256 Signature="
)

// Credentials are the API access ID and HMAC secret. Every string form redacts the secret.
type Credentials struct {
	AccessID string
	Secret   string
}

func (c Credentials) String() string {
	return fmt.Sprintf("Credentials{AccessID: %s, Secret: REDACTED}", c.AccessID)
}

// GoString keeps the secret out of %#v.
func (c Credentials) GoString() string { return c.String() }

// LogValue keeps the secret out of slog output.
func (c Credentials) LogValue() slog.Value {
	return slog.GroupValue(slog.String("access_id", c.AccessID), slog.String("secret", "REDACTED"))
}

// Signer produces the auth headers for one request at a time.
type Signer struct {
	creds Credentials
	now   func() time.Time
	nonce func() string
}

// Option customises a Signer; used by tests to fix time and nonce.
type Option func(*Signer)

func WithClock(now func() time.Time) Option { return func(s *Signer) { s.now = now } }
func WithNonce(nonce func() string) Option  { return func(s *Signer) { s.nonce = nonce } }

func New(c Credentials, opts ...Option) *Signer {
	s := &Signer{creds: c, now: time.Now, nonce: newUUID}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Headers returns the auth and content headers for a request.
func (s *Signer) Headers(method, path string, q url.Values) map[string]string {
	nonce := s.nonce()
	date := s.now().UTC().Format(http.TimeFormat)
	p := plaintext(method, path, q, s.creds.AccessID, nonce, date)
	return map[string]string{
		HeaderCredential:    s.creds.AccessID,
		HeaderNonce:         nonce,
		HeaderDate:          date,
		HeaderAuthorization: authPrefix + signature(s.creds.Secret, p),
		"Content-Type":      "application/json",
	}
}

// Verify checks a signed request. Used by the mock server; replay detection on the nonce is the
// caller's job because it needs state.
func Verify(h http.Header, method, path string, q url.Values, c Credentials, now time.Time, maxSkew time.Duration) error {
	if h.Get(HeaderCredential) != c.AccessID {
		return errors.New("unknown credential")
	}
	date := h.Get(HeaderDate)
	t, err := time.Parse(http.TimeFormat, date)
	if err != nil {
		return fmt.Errorf("bad date: %w", err)
	}
	if d := now.Sub(t); d > maxSkew || d < -maxSkew {
		return errors.New("date outside allowed skew")
	}
	nonce := h.Get(HeaderNonce)
	if nonce == "" {
		return errors.New("missing nonce")
	}
	got, ok := strings.CutPrefix(h.Get(HeaderAuthorization), authPrefix)
	if !ok {
		return errors.New("missing signature")
	}
	want := signature(c.Secret, plaintext(method, path, q, c.AccessID, nonce, date))
	if !hmac.Equal([]byte(got), []byte(want)) {
		return errors.New("signature mismatch")
	}
	return nil
}

func plaintext(method, path string, q url.Values, accessID, nonce, date string) string {
	return strings.Join([]string{strings.ToUpper(method), path, sortedQuery(q), accessID, nonce, date}, ";")
}

func sortedQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, "&")
}

func signature(secret, plaintext string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(plaintext))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// newUUID returns a random version-4 UUID.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
