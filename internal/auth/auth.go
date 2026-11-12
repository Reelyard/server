// Package auth handles passwords, browser sessions and API keys.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/reelyard/server/internal/store"
)

const (
	// CookieName is the session cookie.
	CookieName = "reelyard_session"
	// APIKeyHeader carries an API key for scripts.
	APIKeyHeader = "X-Api-Key"
	// SessionTTL is how long a login lasts.
	SessionTTL = 30 * 24 * time.Hour
	// MinPasswordLen is the shortest accepted password.
	MinPasswordLen = 8
	apiKeyPrefix   = "hbr_"
	bcryptCost     = 12
)

var (
	ErrBadCredentials = errors.New("wrong username or password")
	ErrWeakPassword   = errors.New("password must be at least 8 characters")
	ErrBadUsername    = errors.New("username must be 1–64 characters with no spaces")
	ErrTooManyTries   = errors.New("too many failed logins; wait a minute and try again")
)

// HashPassword returns a bcrypt hash.
func HashPassword(pw string) (string, error) {
	if err := ValidatePassword(pw); err != nil {
		return "", err
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(b), err
}

// ValidatePassword checks password rules.
func ValidatePassword(pw string) error {
	if utf8.RuneCountInString(pw) < MinPasswordLen {
		return ErrWeakPassword
	}
	if len(pw) > 72 { // bcrypt limit
		return errors.New("password must be at most 72 bytes")
	}
	return nil
}

// ValidateUsername checks username rules.
func ValidateUsername(u string) error {
	if u == "" || len(u) > 64 || strings.ContainsAny(u, " \t\r\n") {
		return ErrBadUsername
	}
	return nil
}

// HashToken returns the stored form of a session token or API key.
func HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// dummyHash makes failed lookups take as long as real password checks.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("reelyard-timing-equaliser"), bcryptCost)

// Service ties auth to the store.
type Service struct {
	Store   *store.Store
	limiter *limiter
	now     func() time.Time
}

// New returns an auth service.
func New(s *store.Store) *Service {
	return &Service{Store: s, limiter: newLimiter(5, time.Minute), now: time.Now}
}

// Login checks credentials and creates a session, returning the raw token.
func (a *Service) Login(ctx context.Context, username, password, userAgent, remote string) (string, *store.User, error) {
	key := remote + "|" + strings.ToLower(username)
	if !a.limiter.allow(key, a.now()) {
		return "", nil, ErrTooManyTries
	}
	u, err := a.Store.UserByName(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		a.limiter.fail(key, a.now())
		return "", nil, ErrBadCredentials
	}
	if err != nil {
		return "", nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		a.limiter.fail(key, a.now())
		return "", nil, ErrBadCredentials
	}
	a.limiter.reset(key)
	tok, err := a.NewSession(ctx, u.ID, userAgent, remote)
	return tok, u, err
}

// NewSession creates a session for a user and returns the raw token.
func (a *Service) NewSession(ctx context.Context, userID int64, userAgent, remote string) (string, error) {
	tok := randomToken(32)
	if len(userAgent) > 256 {
		userAgent = userAgent[:256]
	}
	err := a.Store.CreateSession(ctx, HashToken(tok), userID, a.now().Add(SessionTTL), userAgent, remote)
	return tok, err
}

// Logout ends the session with this raw token.
func (a *Service) Logout(ctx context.Context, token string) error {
	return a.Store.DeleteSession(ctx, HashToken(token))
}

// NewAPIKey creates a key and returns the raw key (shown once).
func (a *Service) NewAPIKey(ctx context.Context, userID int64, name string) (string, *store.APIKey, error) {
	raw := apiKeyPrefix + randomToken(24)
	k, err := a.Store.CreateAPIKey(ctx, userID, name, raw[:len(apiKeyPrefix)+4], HashToken(raw))
	return raw, k, err
}

// HeaderCredentials reports whether a request carries a credential in a
// header (an API key or a Bearer token). Browsers never add these by
// themselves, so such requests can't be cross-site request forgeries; and
// Authenticate never falls back to the cookie when one is present.
func HeaderCredentials(r *http.Request) bool {
	return r.Header.Get(APIKeyHeader) != "" || r.Header.Get("Authorization") != ""
}

// Authenticate finds the user for a request from its API key header,
// Bearer token (a device token or an API key), or session cookie. A request
// with a header credential is judged on that alone. It returns
// store.ErrNotFound when no credential is valid.
func (a *Service) Authenticate(r *http.Request) (*store.User, error) {
	if k := r.Header.Get(APIKeyHeader); k != "" {
		return a.Store.APIKeyUser(r.Context(), HashToken(k))
	}
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, tok, ok := strings.Cut(h, " ")
		tok = strings.TrimSpace(tok)
		if !ok || !strings.EqualFold(scheme, "Bearer") || tok == "" {
			return nil, store.ErrNotFound
		}
		switch {
		case strings.HasPrefix(tok, deviceTokenPrefix):
			return a.Store.DeviceUser(r.Context(), HashToken(tok), remoteHost(r))
		case strings.HasPrefix(tok, apiKeyPrefix):
			return a.Store.APIKeyUser(r.Context(), HashToken(tok))
		}
		return nil, store.ErrNotFound
	}
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil, store.ErrNotFound
	}
	return a.Store.SessionUser(r.Context(), HashToken(c.Value))
}

// SessionCookie builds the cookie for a new session.
// secure should be true when the request arrived over HTTPS.
func SessionCookie(token string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// ClearCookie removes the session cookie.
func ClearCookie(secure bool) *http.Cookie {
	return &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode}
}

type ctxKey struct{}

// WithUser stores the authenticated user in a context.
func WithUser(ctx context.Context, u *store.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// UserFrom returns the authenticated user, or nil.
func UserFrom(ctx context.Context) *store.User {
	u, _ := ctx.Value(ctxKey{}).(*store.User)
	return u
}

// limiter allows max failures per key within window.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, fails: map[string][]time.Time{}}
}

func (l *limiter) prune(key string, now time.Time) []time.Time {
	ts := l.fails[key]
	keep := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < l.window {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.fails, key)
		return nil
	}
	l.fails[key] = keep
	return keep
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key, now)) < l.max
}

func (l *limiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[key] = append(l.prune(key, now), now)
	// Keep memory bounded if someone sprays usernames.
	if len(l.fails) > 10000 {
		for k := range l.fails {
			l.prune(k, now)
		}
	}
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}
