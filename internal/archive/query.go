package archive

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/k8s"
)

// QueryParams selects archived logs for a time window (within retention).
type QueryParams struct {
	Namespace string
	Kind      string
	Workload  string
	Pods      []string
	Container string
	From      time.Time
	To        time.Time
}

// Status describes whether history archival is available.
type Status struct {
	Enabled       bool   `json:"enabled"`
	Bucket        string `json:"bucket,omitempty"`
	Prefix        string `json:"prefix,omitempty"`
	Region        string `json:"region,omitempty"`
	RetentionDays int    `json:"retentionDays"`
	MaxLookback   string `json:"maxLookback"` // RFC3339 earliest queryable
}

func (s *Store) Status() Status {
	cfg := s.cfg
	st := Status{
		Enabled:       cfg.Valid(),
		RetentionDays: cfg.RetentionDays,
		MaxLookback:   time.Now().UTC().AddDate(0, 0, -cfg.RetentionDays).Format(time.RFC3339),
	}
	if st.Enabled {
		st.Bucket = cfg.Bucket
		st.Prefix = cfg.Prefix
		st.Region = cfg.Region
	}
	return st
}

// StreamHistory writes formatted log lines for [From, To] to w (same shape as live stream).
// Pods may be empty or omitted — all archived pods for the workload are used (deleted pods still readable).
func (s *Store) StreamHistory(ctx context.Context, q QueryParams, w http.ResponseWriter) error {
	if !s.cfg.Valid() {
		return fmt.Errorf("s3 archive disabled")
	}
	if q.From.IsZero() || q.To.IsZero() || !q.To.After(q.From) {
		return fmt.Errorf("invalid from/to range")
	}
	retentionStart := time.Now().UTC().AddDate(0, 0, -s.cfg.RetentionDays)
	if q.From.Before(retentionStart) {
		q.From = retentionStart
	}
	if q.To.After(time.Now().UTC().Add(time.Minute)) {
		q.To = time.Now().UTC().Add(time.Minute)
	}

	flusher, _ := w.(http.Flusher)
	pods := q.Pods
	if len(pods) == 0 {
		discovered, err := s.ListArchivedPods(ctx, q.Namespace, q.Kind, q.Workload)
		if err != nil {
			return err
		}
		pods = discovered
	}
	if len(pods) == 0 {
		// Fall back to workload-level listing (any pod segment under this workload).
		prefix := strings.Join([]string{
			s.cfg.Prefix, "v1",
			safePath(q.Namespace), safePath(q.Kind), safePath(q.Workload),
		}, "/")
		objs, err := s.ListPrefix(ctx, prefix)
		if err != nil {
			return err
		}
		return s.streamObjects(ctx, q, objs, "", w, flusher)
	}

	var hits []histHit

	for _, pod := range pods {
		prefix := strings.Join([]string{
			s.cfg.Prefix, "v1",
			safePath(q.Namespace), safePath(q.Kind), safePath(q.Workload), safePath(pod),
		}, "/")
		objs, err := s.ListPrefix(ctx, prefix)
		if err != nil {
			return err
		}
		part, err := s.collectHits(ctx, q, objs, pod)
		if err != nil {
			return err
		}
		hits = append(hits, part...)
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].t.Equal(hits[j].t) {
			return hits[i].pod < hits[j].pod
		}
		return hits[i].t.Before(hits[j].t)
	})

	const maxLines = 20000
	if len(hits) > maxLines {
		hits = hits[len(hits)-maxLines:]
	}

	for _, h := range hits {
		formatted := k8s.FormatLogLine(h.raw, h.pod, true)
		if _, err := w.Write(formatted); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	return nil
}

type histHit struct {
	t   time.Time
	raw []byte
	pod string
}

func (s *Store) collectHits(ctx context.Context, q QueryParams, objs []ObjectInfo, fallbackPod string) ([]histHit, error) {
	var hits []histHit
	for _, obj := range objs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		day, ok := dayFromKey(obj.Key)
		if ok {
			dayEnd := day.Add(24 * time.Hour)
			if dayEnd.Before(q.From) || day.After(q.To) {
				continue
			}
		} else if !obj.LastModified.IsZero() {
			if obj.LastModified.Before(q.From.Add(-time.Hour)) || obj.LastModified.After(q.To.Add(time.Hour)) {
				continue
			}
		}
		body, err := s.GetGunzip(ctx, obj.Key)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(body))
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			var rec Line
			if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
				continue
			}
			if q.Container != "" && rec.C != "" && rec.C != q.Container {
				continue
			}
			ts, err := time.Parse(time.RFC3339Nano, rec.T)
			if err != nil {
				ts, err = time.Parse(time.RFC3339, rec.T)
			}
			if err != nil {
				continue
			}
			if ts.Before(q.From) || ts.After(q.To) {
				continue
			}
			podName := rec.Pod
			if podName == "" {
				podName = fallbackPod
			}
			if podName == "" {
				_, _, _, p, _, ok := parseArchiveKey(obj.Key, s.cfg.Prefix)
				if ok {
					podName = p
				}
			}
			raw := []byte(ts.UTC().Format(time.RFC3339Nano) + " " + rec.M + "\n")
			hits = append(hits, histHit{t: ts, raw: raw, pod: podName})
		}
	}
	return hits, nil
}

func (s *Store) streamObjects(ctx context.Context, q QueryParams, objs []ObjectInfo, fallbackPod string, w http.ResponseWriter, flusher http.Flusher) error {
	hits, err := s.collectHits(ctx, q, objs, fallbackPod)
	if err != nil {
		return err
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].t.Equal(hits[j].t) {
			return hits[i].pod < hits[j].pod
		}
		return hits[i].t.Before(hits[j].t)
	})
	const maxLines = 20000
	if len(hits) > maxLines {
		hits = hits[len(hits)-maxLines:]
	}
	for _, h := range hits {
		formatted := k8s.FormatLogLine(h.raw, h.pod, true)
		if _, err := w.Write(formatted); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	return nil
}
