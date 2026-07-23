package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash,omitempty"`
	Password     string `json:"password,omitempty"`
	Enabled      bool   `json:"enabled"`
}

type Workloads struct {
	Deployments  bool `json:"deployments"`
	StatefulSets bool `json:"statefulSets"`
	DaemonSets   bool `json:"daemonSets"`
	Jobs         bool `json:"jobs"`
}

// Settings is editable from the admin panel.
type Settings struct {
	Mode    string   `json:"mode"`
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
	Filter  string   `json:"filter"`
	Title   string   `json:"title"`

	AuthEnabled bool   `json:"authEnabled"`
	Users       []User `json:"users"`

	Workloads Workloads `json:"workloads"`

	// AllowedWorkloads keys: "namespace/Kind/name" e.g. "dev/Deployment/api".
	// Empty = show all workloads of enabled kinds in allowed namespaces.
	AllowedWorkloads []string `json:"allowedWorkloads"`

	// About / branding links (shown in viewer About panel).
	AuthorName   string `json:"authorName"`
	GitHubURL    string `json:"githubUrl"`
	PortfolioURL string `json:"portfolioUrl"`
	RepoURL      string `json:"repoUrl"`
}

func Default() Settings {
	return Settings{
		Mode:        "exclude",
		Include:     []string{},
		Exclude:     []string{"kube-system", "kube-public", "kube-node-lease"},
		Filter:      "",
		Title:       "Log Viewer",
		AuthEnabled: true, // force setup: create users before viewing logs
		Users:       []User{},
		Workloads: Workloads{
			Deployments:  true,
			StatefulSets: true,
			DaemonSets:   false,
			Jobs:         false,
		},
		AllowedWorkloads: []string{},
		AuthorName:       "Abdul Rehman",
		GitHubURL:        "https://github.com/Abdul-Rehman-DevOps",
		PortfolioURL:     "https://abdulrehman.cz/",
		RepoURL:          "https://github.com/Abdul-Rehman-DevOps/log-viewer",
	}
}

func WorkloadKey(namespace, kind, name string) string {
	return namespace + "/" + kind + "/" + name
}

type Store struct {
	path     string
	mu       sync.RWMutex
	cur      Settings
	onSave   func(Settings) // optional ConfigMap sync
	reloadCh chan struct{}
}

func NewStore(path string) (*Store, error) {
	s := &Store{path: path, cur: Default(), reloadCh: make(chan struct{}, 1)}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	s.mu.Lock()
	normalize(&s.cur)
	s.mu.Unlock()
	return s, nil
}

func (s *Store) SetOnSave(fn func(Settings)) { s.onSave = fn }

func normalize(cur *Settings) {
	if !cur.Workloads.Deployments && !cur.Workloads.StatefulSets &&
		!cur.Workloads.DaemonSets && !cur.Workloads.Jobs {
		cur.Workloads = Default().Workloads
	}
	if cur.Title == "" {
		cur.Title = "Log Viewer"
	}
	if cur.AuthorName == "" {
		cur.AuthorName = "Abdul Rehman"
	}
	if cur.GitHubURL == "" {
		cur.GitHubURL = "https://github.com/Abdul-Rehman-DevOps"
	}
	if cur.PortfolioURL == "" {
		cur.PortfolioURL = "https://abdulrehman.cz/"
	}
	if cur.RepoURL == "" {
		cur.RepoURL = "https://github.com/Abdul-Rehman-DevOps/log-viewer"
	}
	if cur.Mode == "" {
		cur.Mode = "exclude"
	}
	// If users exist, auth must be on
	if HasEnabledUsers(*cur) {
		cur.AuthEnabled = true
	}
}

func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.cur)
}

func (s *Store) GetForAdmin() Settings {
	c := s.Get()
	out := c
	out.Users = make([]User, len(c.Users))
	for i, u := range c.Users {
		out.Users[i] = User{Username: u.Username, Enabled: u.Enabled}
	}
	return out
}

func (s *Store) Update(next Settings) error {
	next.Mode = strings.ToLower(strings.TrimSpace(next.Mode))
	if next.Mode != "include" && next.Mode != "exclude" {
		return fmt.Errorf("mode must be include or exclude")
	}
	if next.Title == "" {
		next.Title = "Log Viewer"
	}
	next.Include = cleanList(next.Include)
	next.Exclude = cleanList(next.Exclude)
	next.AllowedWorkloads = cleanList(next.AllowedWorkloads)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Author branding is locked — Admin UI cannot change these fields.
	next.AuthorName = s.cur.AuthorName
	next.GitHubURL = s.cur.GitHubURL
	next.PortfolioURL = s.cur.PortfolioURL
	next.RepoURL = s.cur.RepoURL

	mergedUsers, err := mergeUsers(s.cur.Users, next.Users)
	if err != nil {
		return err
	}
	next.Users = mergedUsers
	if HasEnabledUsers(next) {
		next.AuthEnabled = true
	}
	normalize(&next)

	if err := s.saveLocked(next); err != nil {
		return err
	}
	s.cur = next
	if s.onSave != nil {
		go s.onSave(clone(next))
	}
	return nil
}

// ReplaceFromBytes used by ConfigMap reload (other replicas).
func (s *Store) ReplaceFromBytes(b []byte) error {
	var cur Settings
	if err := json.Unmarshal(b, &cur); err != nil {
		return err
	}
	normalize(&cur)
	s.mu.Lock()
	s.cur = cur
	_ = s.saveLocked(cur) // keep local file in sync
	s.mu.Unlock()
	return nil
}

func mergeUsers(prev, incoming []User) ([]User, error) {
	prevByName := map[string]User{}
	for _, u := range prev {
		prevByName[u.Username] = u
	}
	out := make([]User, 0, len(incoming))
	seen := map[string]struct{}{}
	for _, u := range incoming {
		u.Username = strings.TrimSpace(u.Username)
		if u.Username == "" {
			continue
		}
		if _, dup := seen[u.Username]; dup {
			return nil, fmt.Errorf("duplicate user %q", u.Username)
		}
		seen[u.Username] = struct{}{}
		old, had := prevByName[u.Username]
		switch {
		case strings.TrimSpace(u.Password) != "":
			hash, err := bcrypt.GenerateFromPassword([]byte(u.Password), bcrypt.DefaultCost)
			if err != nil {
				return nil, err
			}
			u.PasswordHash = string(hash)
			u.Password = ""
		case had:
			u.PasswordHash = old.PasswordHash
			u.Password = ""
		default:
			return nil, fmt.Errorf("user %q needs a password", u.Username)
		}
		out = append(out, u)
	}
	return out, nil
}

func HasEnabledUsers(cfg Settings) bool {
	for _, u := range cfg.Users {
		if u.Enabled && u.Username != "" {
			return true
		}
	}
	return false
}

// RequireLogin is true when at least one enabled user exists.
func (s *Store) RequireLogin() bool {
	return HasEnabledUsers(s.Get())
}

// NeedsSetup is true when no enabled users exist — viewer blocked until admin creates one.
func (s *Store) NeedsSetup() bool {
	return !HasEnabledUsers(s.Get())
}

func (s *Store) AuthEnabled() bool {
	return s.RequireLogin()
}

func (s *Store) VerifyUser(username, password string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.cur.Users {
		if u.Username != username || !u.Enabled {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil {
			return true
		}
	}
	return false
}

func WorkloadAllowed(cfg Settings, namespace, kind, name string) bool {
	if len(cfg.AllowedWorkloads) == 0 {
		return true
	}
	key := WorkloadKey(namespace, kind, name)
	for _, k := range cfg.AllowedWorkloads {
		if k == key {
			return true
		}
	}
	return false
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var cur Settings
	if err := json.Unmarshal(b, &cur); err != nil {
		return err
	}
	normalize(&cur)
	s.mu.Lock()
	s.cur = cur
	s.mu.Unlock()
	return nil
}

func (s *Store) saveLocked(cur Settings) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	clean := cur
	clean.Users = make([]User, len(cur.Users))
	for i, u := range cur.Users {
		clean.Users[i] = User{Username: u.Username, PasswordHash: u.PasswordHash, Enabled: u.Enabled}
	}
	b, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Marshal() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	clean := s.cur
	clean.Users = make([]User, len(s.cur.Users))
	for i, u := range s.cur.Users {
		clean.Users[i] = User{Username: u.Username, PasswordHash: u.PasswordHash, Enabled: u.Enabled}
	}
	return json.MarshalIndent(clean, "", "  ")
}

func cleanList(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func clone(s Settings) Settings {
	c := s
	c.Include = append([]string{}, s.Include...)
	c.Exclude = append([]string{}, s.Exclude...)
	c.Users = append([]User{}, s.Users...)
	c.AllowedWorkloads = append([]string{}, s.AllowedWorkloads...)
	return c
}

func ResolveNamespaces(cfg Settings, all []string) []string {
	excl := toSet(cfg.Exclude)
	incl := toSet(cfg.Include)
	switch cfg.Mode {
	case "include":
		out := make([]string, 0, len(cfg.Include))
		for _, ns := range cfg.Include {
			if _, ok := incl[ns]; ok {
				out = append(out, ns)
			}
		}
		return out
	default:
		out := make([]string, 0, len(all))
		for _, ns := range all {
			if _, skip := excl[ns]; skip {
				continue
			}
			out = append(out, ns)
		}
		return out
	}
}

func NamespaceAllowed(cfg Settings, ns string) bool {
	if cfg.Mode == "include" {
		for _, n := range cfg.Include {
			if n == ns {
				return true
			}
		}
		return false
	}
	for _, n := range cfg.Exclude {
		if n == ns {
			return false
		}
	}
	return true
}

func toSet(list []string) map[string]struct{} {
	m := make(map[string]struct{}, len(list))
	for _, v := range list {
		m[v] = struct{}{}
	}
	return m
}

// StartFileWatch reloads local file periodically (backup when ConfigMap not used).
func (s *Store) StartFileWatch(interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		var lastMod time.Time
		for range t.C {
			st, err := os.Stat(s.path)
			if err != nil {
				continue
			}
			if st.ModTime().After(lastMod) {
				lastMod = st.ModTime()
				_ = s.load()
			}
		}
	}()
}
