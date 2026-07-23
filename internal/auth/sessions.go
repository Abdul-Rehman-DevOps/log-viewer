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
	SessionTTL  = time.Hour
)

type Session struct {
	Username string `json:"u"`
	Role     string `json:"r"` // admin | user
	Exp      int64  `json:"e"`
}

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

func (s *Sessions) Get(id string) (Session, bool) {
	sess, err := s.verify(id)
	if err != nil {
		return Session{}, false
	}
	if time.Now().Unix() > sess.Exp {
		return Session{}, false
	}
	return sess, true
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
