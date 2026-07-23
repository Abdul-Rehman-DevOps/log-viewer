package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	AdminCookie = "lv_admin"
	UserCookie  = "lv_session"
	// SessionTTL is sliding: refreshed on activity; idle beyond this logs the user out.
	SessionTTL = 10 * time.Minute
)

type Session struct {
	Username string `json:"u"`
	Role     string `json:"r"` // admin | user
	Exp      int64  `json:"e"`
}

type Status int

const (
	StatusMissing Status = iota
	StatusOK
	StatusExpired
	StatusInvalid
)

type Sessions struct {
	secret []byte
}

func NewSessions() *Sessions {
	sec := os.Getenv("LOG_VIEWER_SESSION_SECRET")
	if sec == "" {
		sec = os.Getenv("LOG_VIEWER_ADMIN_PASSWORD")
	}
	if sec == "" {
		sec = "log-viewer-dev-session-secret"
	}
	return &Sessions{secret: []byte(sec)}
}

func (s *Sessions) Create(username, role string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = SessionTTL
	}
	sess := Session{
		Username: username,
		Role:     role,
		Exp:      time.Now().Add(ttl).Unix(),
	}
	return s.sign(sess)
}

// Touch issues a new token with a fresh sliding expiry.
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
	if status == StatusExpired {
		msg = `{"error":"session_timeout","message":"Session timed out due to inactivity. Please sign in again."}`
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
