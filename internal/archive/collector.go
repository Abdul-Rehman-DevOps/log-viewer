package archive

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/config"
	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/k8s"
)

// Line is one archived log record (JSONL).
type Line struct {
	T   string `json:"t"` // RFC3339Nano UTC from kubelet
	Pod string `json:"p"`
	C   string `json:"c,omitempty"`
	M   string `json:"m"`
}

// Collector periodically snapshots pod logs into S3.
type Collector struct {
	store  *Store
	k8s    *k8s.Client
	cfgStore *config.Store

	mu          sync.Mutex
	lastSince   map[string]time.Time // key: ns/pod[/container]
	stopCh      chan struct{}
	stopped     chan struct{}
}

func NewCollector(store *Store, kclient *k8s.Client, cfgStore *config.Store) *Collector {
	return &Collector{
		store:    store,
		k8s:      kclient,
		cfgStore: cfgStore,
		lastSince: make(map[string]time.Time),
		stopCh:   make(chan struct{}),
		stopped:  make(chan struct{}),
	}
}

func (c *Collector) Start(ctx context.Context) {
	go c.loop(ctx)
}

func (c *Collector) Stop() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
	<-c.stopped
}

func (c *Collector) loop(parent context.Context) {
	defer close(c.stopped)
	cfg := c.store.Config()
	log.Printf("s3 archive collector start interval=%s retention=%dd bucket=%s prefix=%s",
		cfg.Interval, cfg.RetentionDays, cfg.Bucket, cfg.Prefix)

	// First pass soon after boot, then on interval.
	t := time.NewTimer(5 * time.Second)
	defer t.Stop()
	cleanupEvery := 0

	for {
		select {
		case <-parent.Done():
			return
		case <-c.stopCh:
			return
		case <-t.C:
			runCtx, cancel := context.WithTimeout(parent, cfg.Interval)
			if err := c.collectOnce(runCtx); err != nil {
				log.Printf("s3 archive collect: %v", err)
			}
			cleanupEvery++
			if cleanupEvery >= 10 {
				cleanupEvery = 0
				cutoff := time.Now().UTC().AddDate(0, 0, -cfg.RetentionDays)
				if _, err := c.store.CleanupOlderThan(runCtx, cutoff); err != nil {
					log.Printf("s3 archive cleanup: %v", err)
				}
			}
			cancel()
			t.Reset(cfg.Interval)
		}
	}
}

func (c *Collector) collectOnce(ctx context.Context) error {
	settings := c.cfgStore.Get()
	allNS, err := c.k8s.ListNamespaces(ctx)
	if err != nil {
		return err
	}
	nsList := config.ResolveNamespaces(settings, allNS)
	workloads, err := c.k8s.ListWorkloads(ctx, settings, nsList)
	if err != nil {
		return err
	}
	var uploaded, skipped int
	now := time.Now().UTC()
	for _, wl := range workloads {
		for _, pod := range wl.Pods {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			containers, err := c.k8s.PodContainers(ctx, wl.Namespace, pod)
			if err != nil || len(containers) == 0 {
				containers = []string{""}
			}
			for _, ctr := range containers {
				n, err := c.archivePod(ctx, wl, pod, ctr, now)
				if err != nil {
					log.Printf("s3 archive ns=%s pod=%s ctr=%q: %v", wl.Namespace, pod, ctr, err)
					continue
				}
				if n > 0 {
					uploaded++
				} else {
					skipped++
				}
			}
		}
	}
	log.Printf("s3 archive tick uploaded=%d empty=%d workloads=%d", uploaded, skipped, len(workloads))
	return nil
}

func (c *Collector) archivePod(ctx context.Context, wl k8s.Workload, pod, container string, now time.Time) (int, error) {
	ck := checkpointKey(wl.Namespace, pod, container)
	c.mu.Lock()
	since, ok := c.lastSince[ck]
	c.mu.Unlock()

	cfg := c.store.Config()
	var sincePtr *time.Time
	var sinceSec *int64
	if ok {
		s := since.Add(-2 * time.Second) // small overlap to avoid gaps
		sincePtr = &s
	} else if cfg.BackfillSeconds > 0 {
		sec := cfg.BackfillSeconds
		sinceSec = &sec
	} else {
		s := now.Add(-cfg.Interval)
		sincePtr = &s
	}

	raw, err := c.k8s.FetchPodLogs(ctx, wl.Namespace, pod, container, sincePtr, sinceSec, 5000)
	if err != nil {
		// Pod may be terminating / no logs yet.
		return 0, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		c.mu.Lock()
		c.lastSince[ck] = now
		c.mu.Unlock()
		return 0, nil
	}

	body, lastTS, n := encodeChunk(raw, pod, container)
	if n == 0 {
		c.mu.Lock()
		c.lastSince[ck] = now
		c.mu.Unlock()
		return 0, nil
	}

	key := c.store.ObjectKey(wl.Namespace, wl.Kind, wl.Name, pod, now)
	if container != "" {
		// Include container in filename uniqueness when multi-container.
		key = strings.TrimSuffix(key, ".jsonl.gz") + "-" + safePath(container) + ".jsonl.gz"
	}
	if err := c.store.PutGzip(ctx, key, body); err != nil {
		return 0, err
	}

	next := now
	if !lastTS.IsZero() {
		next = lastTS
	}
	c.mu.Lock()
	c.lastSince[ck] = next
	c.mu.Unlock()
	return n, nil
}

func checkpointKey(ns, pod, container string) string {
	return ns + "/" + pod + "/" + container
}

func encodeChunk(raw []byte, pod, container string) (body []byte, lastTS time.Time, count int) {
	var buf bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		ts, msg := splitKubeTS(line)
		rec := Line{T: ts, Pod: pod, C: container, M: msg}
		if ts == "" {
			rec.T = time.Now().UTC().Format(time.RFC3339Nano)
			rec.M = line
		} else if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			lastTS = t
		} else if t, err := time.Parse(time.RFC3339, ts); err == nil {
			lastTS = t
		}
		b, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		buf.Write(b)
		buf.WriteByte('\n')
		count++
	}
	return buf.Bytes(), lastTS, count
}

func splitKubeTS(line string) (ts, msg string) {
	sp := strings.IndexByte(line, ' ')
	if sp <= 0 {
		return "", line
	}
	cand := line[:sp]
	if !strings.Contains(cand, "T") {
		return "", line
	}
	if _, err := time.Parse(time.RFC3339Nano, cand); err != nil {
		if _, err2 := time.Parse(time.RFC3339, cand); err2 != nil {
			return "", line
		}
	}
	return cand, strings.TrimLeft(line[sp+1:], " ")
}
