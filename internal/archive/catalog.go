package archive

import (
	"context"
	"path"
	"sort"
	"strings"
	"time"
)

// ArchivedWorkload is a workload that still has objects in S3 (may no longer exist in the cluster).
type ArchivedWorkload struct {
	Kind      string   `json:"kind"`
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Pods      []string `json:"pods"`
	Days      []string `json:"days,omitempty"` // YYYY-MM-DD with data
	LastSeen  string   `json:"lastSeen,omitempty"`
	Archived  bool     `json:"archived"` // always true for S3-sourced entries
}

// ListArchivedWorkloads discovers ns/kind/name/pods from S3 keys within retention.
// Survives deleted Deployments and pods — objects stay until retention cleanup.
func (s *Store) ListArchivedWorkloads(ctx context.Context) ([]ArchivedWorkload, error) {
	if !s.cfg.Valid() {
		return nil, nil
	}
	prefix := s.cfg.Prefix + "/v1/"
	objs, err := s.ListPrefix(ctx, prefix)
	if err != nil {
		return nil, err
	}
	retentionStart := time.Now().UTC().AddDate(0, 0, -s.cfg.RetentionDays).Truncate(24 * time.Hour)

	type agg struct {
		pods map[string]struct{}
		days map[string]struct{}
		last time.Time
	}
	byKey := map[string]*agg{}

	for _, o := range objs {
		ns, kind, name, pod, day, ok := parseArchiveKey(o.Key, s.cfg.Prefix)
		if !ok {
			continue
		}
		if day.Before(retentionStart) {
			continue
		}
		wk := ns + "/" + kind + "/" + name
		a := byKey[wk]
		if a == nil {
			a = &agg{pods: map[string]struct{}{}, days: map[string]struct{}{}}
			byKey[wk] = a
		}
		if pod != "" {
			a.pods[pod] = struct{}{}
		}
		dayStr := day.Format("2006-01-02")
		a.days[dayStr] = struct{}{}
		mod := o.LastModified
		if mod.IsZero() {
			mod = day
		}
		if mod.After(a.last) {
			a.last = mod
		}
	}

	out := make([]ArchivedWorkload, 0, len(byKey))
	for wk, a := range byKey {
		parts := strings.SplitN(wk, "/", 3)
		if len(parts) != 3 {
			continue
		}
		pods := make([]string, 0, len(a.pods))
		for p := range a.pods {
			pods = append(pods, p)
		}
		sort.Strings(pods)
		days := make([]string, 0, len(a.days))
		for d := range a.days {
			days = append(days, d)
		}
		sort.Strings(days)
		item := ArchivedWorkload{
			Namespace: parts[0],
			Kind:      parts[1],
			Name:      parts[2],
			Pods:      pods,
			Days:      days,
			Archived:  true,
		}
		if !a.last.IsZero() {
			item.LastSeen = a.last.UTC().Format(time.RFC3339)
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ListArchivedPods returns pod names under a workload prefix in S3.
func (s *Store) ListArchivedPods(ctx context.Context, ns, kind, workload string) ([]string, error) {
	prefix := strings.Join([]string{
		s.cfg.Prefix, "v1",
		safePath(ns), safePath(kind), safePath(workload),
	}, "/")
	objs, err := s.ListPrefix(ctx, prefix)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	for _, o := range objs {
		_, _, _, pod, _, ok := parseArchiveKey(o.Key, s.cfg.Prefix)
		if ok && pod != "" {
			seen[pod] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// parseArchiveKey expects: {prefix}/v1/{ns}/{kind}/{workload}/{pod}/{YYYY-MM-DD}/{file}
func parseArchiveKey(key, prefix string) (ns, kind, name, pod string, day time.Time, ok bool) {
	key = strings.TrimPrefix(key, "/")
	base := strings.Trim(prefix, "/") + "/v1/"
	if !strings.HasPrefix(key, base) {
		return "", "", "", "", time.Time{}, false
	}
	rest := strings.TrimPrefix(key, base)
	parts := strings.Split(rest, "/")
	// ns, kind, workload, pod, day, file
	if len(parts) < 6 {
		return "", "", "", "", time.Time{}, false
	}
	ns, kind, name, pod = parts[0], parts[1], parts[2], parts[3]
	dayStr := parts[4]
	t, err := time.ParseInLocation("2006-01-02", dayStr, time.UTC)
	if err != nil {
		return "", "", "", "", time.Time{}, false
	}
	_ = path.Base(key)
	return ns, kind, name, pod, t, true
}
