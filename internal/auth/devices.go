package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/inluxc/harbor/internal/store"
)

const (
	// PairingTTL is how long a pairing code can be approved.
	PairingTTL = 10 * time.Minute
	// PairingPollInterval is how often a device should ask for its token.
	PairingPollInterval = 3 * time.Second
	// MaxPendingPairings caps pairings waiting at once (the endpoint is public).
	MaxPendingPairings = 20
	// StreamLinkTTL is how long a signed stream link works: a long film,
	// paused for a while, still plays to the end.
	StreamLinkTTL = 8 * time.Hour

	deviceTokenPrefix = "hbd_"
	pollSecretPrefix  = "hbp_"
	// Pairing codes avoid letters and digits people mix up (0/O, 1/I/L).
	codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	codeLen      = 6
)

var (
	ErrTooManyPairings = errors.New("too many devices are waiting to pair; approve or wait for them to expire")
	ErrBadDeviceName   = errors.New("device name must be 1–64 characters")
	ErrUnknownCode     = errors.New("that code isn't waiting to pair; check it on the device, or start pairing again")
)

// Pairing is what a device needs to show its code and collect its token.
type Pairing struct {
	Code       string        // shown on the device, e.g. "K7Q-4MD"
	PollSecret string        // kept by the device; sent to collect the token
	ExpiresIn  time.Duration // until the code stops working
	Interval   time.Duration // between polls
}

// NormaliseCode turns what a person typed ("k7q 4md") into the stored form.
func NormaliseCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if strings.ContainsRune(codeAlphabet, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func randomCode() string {
	buf := make([]byte, codeLen)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	out := make([]byte, codeLen)
	for i, v := range buf {
		// 256 % 31 leaves a tiny bias; codes live ten minutes and need approval.
		out[i] = codeAlphabet[int(v)%len(codeAlphabet)]
	}
	return string(out)
}

// StartPairing registers a device that wants to sign in.
func (a *Service) StartPairing(ctx context.Context, name, platform, ip string) (*Pairing, error) {
	name, platform = strings.TrimSpace(name), strings.TrimSpace(platform)
	if name == "" || len([]rune(name)) > 64 {
		return nil, ErrBadDeviceName
	}
	if len(platform) > 32 {
		platform = platform[:32]
	}
	n, err := a.Store.CountPendingDevices(ctx)
	if err != nil {
		return nil, err
	}
	if n >= MaxPendingPairings {
		return nil, ErrTooManyPairings
	}
	poll := pollSecretPrefix + randomToken(32)
	for range 5 { // a code collision is unlikely; try again if it happens
		code := randomCode()
		_, err = a.Store.CreatePairing(ctx, name, platform, HashToken(code), HashToken(poll), a.now().Add(PairingTTL), ip)
		if err == nil {
			return &Pairing{Code: code[:3] + "-" + code[3:], PollSecret: poll, ExpiresIn: PairingTTL, Interval: PairingPollInterval}, nil
		}
		if !strings.Contains(err.Error(), "UNIQUE") {
			return nil, err
		}
	}
	return nil, err
}

// ApprovePairing gives the device showing code to user.
func (a *Service) ApprovePairing(ctx context.Context, code string, userID int64) (*store.Device, error) {
	c := NormaliseCode(code)
	if len(c) != codeLen {
		return nil, ErrUnknownCode
	}
	d, err := a.Store.ApprovePairing(ctx, HashToken(c), userID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnknownCode
	}
	return d, err
}

// CollectToken returns a new device token once the pairing is approved.
// It returns store.ErrPending while waiting and store.ErrNotFound when the
// secret is unknown, expired, or already used.
func (a *Service) CollectToken(ctx context.Context, pollSecret string) (string, *store.Device, error) {
	if !strings.HasPrefix(pollSecret, pollSecretPrefix) {
		return "", nil, store.ErrNotFound
	}
	tok := deviceTokenPrefix + randomToken(32)
	d, err := a.Store.CollectToken(ctx, HashToken(pollSecret), HashToken(tok))
	if err != nil {
		return "", nil, err
	}
	return tok, d, nil
}

// remoteHost is the client address without its port.
func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- signed stream links ----

const streamSecretKey = "stream_link_secret"

// StreamSigner signs and checks links to library files, for players that
// can't send headers (HTML video, AVPlayer, TV players).
type StreamSigner struct {
	key []byte
	now func() time.Time
}

// NewStreamSigner loads the signing key from the store, creating it on first use.
func NewStreamSigner(ctx context.Context, s *store.Store) (*StreamSigner, error) {
	var hexKey string
	err := s.GetSetting(ctx, streamSecretKey, &hexKey)
	if errors.Is(err, store.ErrNotFound) {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		hexKey = hex.EncodeToString(b)
		if err := s.SetSetting(ctx, streamSecretKey, hexKey); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) < 32 {
		return nil, fmt.Errorf("stream link key is damaged")
	}
	return &StreamSigner{key: key, now: time.Now}, nil
}

func (s *StreamSigner) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

func (s *StreamSigner) mac(fileID int64, exp int64) string {
	m := hmac.New(sha256.New, s.key)
	fmt.Fprintf(m, "stream:%d:%d", fileID, exp)
	return hex.EncodeToString(m.Sum(nil))
}

// Sign returns the query string ("exp=…&sig=…") for a file and when it expires.
func (s *StreamSigner) Sign(fileID int64) (string, time.Time) {
	exp := s.clock().Add(StreamLinkTTL).Truncate(time.Second)
	return "exp=" + strconv.FormatInt(exp.Unix(), 10) + "&sig=" + s.mac(fileID, exp.Unix()), exp
}

// Valid reports whether exp and sig are a live signature for fileID.
func (s *StreamSigner) Valid(fileID int64, exp, sig string) bool {
	e, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || sig == "" || len(s.key) == 0 || s.clock().Unix() > e {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.mac(fileID, e)))
}
