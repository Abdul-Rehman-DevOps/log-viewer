package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	AdminCookie = "lv_admin"
	UserCookie  = "lv_session"
	// SessionTTL is sliding: refreshed on activity; idle beyond this logs the user out.
	SessionTTL = 8 * time.Hour
)

type Session struct {
	Username string `json:"u"`
	Role     string `json:"r"` // admin | user
	Exp      int64  `json:"e"`
	// Gen binds the cookie to the current process/restart epoch.
	Gen string `json:"g,omitempty"`
}

type Status int

const (
	StatusMissing Status = iota
	StatusOK
	StatusExpired
	StatusInvalid
	StatusRestarted // server restarted — cookie gen no longer matches
)

type Sessions struct {
	secret  []byte
	dataDir string

	mu   sync.RWMutex
	gen  string
}

// NewSessions creates a session manager with a shared restart epoch.
//
// Multi-replica note: never mint a random epoch per process — with emptyDir each
// pod would get a different gen and load-balancing looks like "session restarted".
// Order: LOG_VIEWER_SESSION_EPOCH env → existing session.epoch file → stable hash
// of the session secret (all replicas agree). Bump the env or rotate the secret
// to invalidate all cookies intentionally.
func NewSessions(dataDir string) *Sessions {
	sec := os.Getenv("LOG_VIEWER_SESSION_SECRET")
	if sec == "" {
		sec = os.Getenv("LOG_VIEWER_ADMIN_PASSWORD")
	}
	if sec == "" {
		sec = "log-viewer-dev-session-secret"
	}
	s := &Sessions{secret: []byte(sec), dataDir: dataDir}
	s.loadOrInitEpoch()
	return s
}

func (s *Sessions) setGen(gen string) {
	s.mu.Lock()
	s.gen = gen
	s.mu.Unlock()
}

func (s *Sessions) loadOrInitEpoch() {
	if e := strings.TrimSpace(os.Getenv("LOG_VIEWER_SESSION_EPOCH")); e != "" {
		s.setGen(e)
		s.persistEpoch(e)
		return
	}
	if s.dataDir != "" {
		if raw, err := os.ReadFile(s.epochPath()); err == nil {
			if g := strings.TrimSpace(string(raw)); g != "" {
				s.setGen(g)
				return
			}
		}
	}
	// Stable across replicas without a shared volume.
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte("log-viewer-session-epoch-v1"))
	gen := hex.EncodeToString(mac.Sum(nil))
	s.setGen(gen)
	s.persistEpoch(gen)
}

func (s *Sessions) persistEpoch(gen string) {
	if s.dataDir == "" || gen == "" {
		return
	}
	_ = os.MkdirAll(s.dataDir, 0o755)
	_ = os.WriteFile(s.epochPath(), []byte(gen+"\n"), 0o600)
}

func (s *Sessions) epochPath() string {
	return filepath.Join(s.dataDir, "session.epoch")
}

func (s *Sessions) currentGen() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gen
}

func (s *Sessions) Create(username, role string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = SessionTTL
	}
	sess := Session{
		Username: username,
		Role:     role,
		Exp:      time.Now().Add(ttl).Unix(),
		Gen:      s.currentGen(),
	}
	return s.sign(sess)
}

// Touch issues a new token with a fresh sliding expiry (same restart gen).
func (s *Sessions) Touch(sess Session) (string, error) {
	return s.Create(sess.Username, sess.Role, SessionTTL)
}

func (s *Sessions) Get(id string) (Session, bool) {
	sess, st := s.Lookup(id)
	return sess, st == StatusOK
}

func (s *Sessions) Lookup(id string) (Session, Status) {
	if id == "" {
		return Session{}, StatusMissing
	}
	sess, err := s.verify(id)
	if err != nil {
		return Session{}, StatusInvalid
	}
	if sess.Gen == "" || sess.Gen != s.currentGen() {
		return sess, StatusRestarted
	}
	if time.Now().Unix() > sess.Exp {
		return sess, StatusExpired
	}
	return sess, StatusOK
}

func (s *Sessions) Delete(id string) {}

func (s *Sessions) sign(sess Session) (string, error) {
	b, err := json.Marshal(sess)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig, nil
}

func (s *Sessions) verify(token string) (Session, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Session{}, errBadToken
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return Session{}, errBadToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Session{}, err
	}
	var sess Session
	if err := json.Unmarshal(raw, &sess); err != nil {
		return Session{}, err
	}
	return sess, nil
}

var errBadToken = &tokenError{"invalid session"}

type tokenError struct{ s string }

func (e *tokenError) Error() string { return e.s }

func SetCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	})
}

func ClearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
}

func CookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

func CookieMaxAge() int {
	return int(SessionTTL.Seconds())
}

func SessionTTLSeconds() string {
	return strconv.Itoa(int(SessionTTL.Seconds()))
}

// WriteUnauthorized returns 401 with a clear JSON reason for the UI.
func WriteUnauthorized(w http.ResponseWriter, status Status) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	msg := `{"error":"unauthorized"}`
	switch status {
	case StatusExpired:
		msg = `{"error":"session_timeout","message":"Session timed out due to inactivity. Please sign in again."}`
	case StatusRestarted:
		msg = `{"error":"session_restarted","message":"Session ended because Log Viewer restarted. Please sign in again."}`
	}
	_, _ = w.Write([]byte(msg))
}

// SlideCookie refreshes the sliding session cookie after a successful auth check.
func SlideCookie(w http.ResponseWriter, sessions *Sessions, cookieName string, sess Session) {
	id, err := sessions.Touch(sess)
	if err != nil {
		return
	}
	SetCookie(w, cookieName, id, CookieMaxAge())
}
