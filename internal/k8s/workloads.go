package k8s

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/config"
)

var pktZone = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Karachi")
	if err != nil {
		return time.FixedZone("PKT", 5*3600)
	}
	return loc
}()

type Workload struct {
	Kind      string   `json:"kind"`
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Replicas  int32    `json:"replicas"`
	Ready     int32    `json:"ready"`
	Pods      []string `json:"pods"`
}

func (c *Client) ListWorkloads(ctx context.Context, cfg config.Settings, namespaces []string) ([]Workload, error) {
	return c.listWorkloads(ctx, cfg, namespaces, true)
}

// ListWorkloadsRaw ignores AllowedWorkloads (for admin picker).
func (c *Client) ListWorkloadsRaw(ctx context.Context, cfg config.Settings, namespaces []string) ([]Workload, error) {
	return c.listWorkloads(ctx, cfg, namespaces, false)
}

func (c *Client) listWorkloads(ctx context.Context, cfg config.Settings, namespaces []string, applyAllowlist bool) ([]Workload, error) {
	if applyAllowlist {
		namespaces = config.FilterNamespacesForScan(cfg, namespaces)
	}
	var out []Workload
	for _, ns := range namespaces {
		if cfg.Workloads.Deployments {
			list, err := c.cs.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, d := range list.Items {
					if applyAllowlist && !config.WorkloadAllowed(cfg, ns, "Deployment", d.Name) {
						continue
					}
					pods, ready := podsForDeployment(ctx, c.cs, ns, d.UID)
					out = append(out, Workload{
						Kind: "Deployment", Namespace: ns, Name: d.Name,
						Replicas: deref(d.Spec.Replicas), Ready: ready, Pods: pods,
					})
				}
			}
		}
		if cfg.Workloads.StatefulSets {
			list, err := c.cs.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, st := range list.Items {
					if applyAllowlist && !config.WorkloadAllowed(cfg, ns, "StatefulSet", st.Name) {
						continue
					}
					pods, ready := podsOwnedBy(ctx, c.cs, ns, "StatefulSet", st.UID)
					out = append(out, Workload{
						Kind: "StatefulSet", Namespace: ns, Name: st.Name,
						Replicas: deref(st.Spec.Replicas), Ready: ready, Pods: pods,
					})
				}
			}
		}
		if cfg.Workloads.DaemonSets {
			list, err := c.cs.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, d := range list.Items {
					if applyAllowlist && !config.WorkloadAllowed(cfg, ns, "DaemonSet", d.Name) {
						continue
					}
					pods, ready := podsOwnedBy(ctx, c.cs, ns, "DaemonSet", d.UID)
					out = append(out, Workload{
						Kind: "DaemonSet", Namespace: ns, Name: d.Name,
						Replicas: d.Status.DesiredNumberScheduled, Ready: ready, Pods: pods,
					})
				}
			}
		}
		if cfg.Workloads.Jobs {
			list, err := c.cs.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, j := range list.Items {
					if applyAllowlist && !config.WorkloadAllowed(cfg, ns, "Job", j.Name) {
						continue
					}
					pods, ready := podsOwnedBy(ctx, c.cs, ns, "Job", j.UID)
					out = append(out, Workload{
						Kind: "Job", Namespace: ns, Name: j.Name,
						Replicas: 1, Ready: ready, Pods: pods,
					})
				}
			}
		}
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

// podsForDeployment resolves Pods via ReplicaSets owned by this Deployment only.
// Label selectors alone can wrongly include CronJob/worker pods that share labels.
func podsForDeployment(ctx context.Context, cs kubernetes.Interface, ns string, deployUID types.UID) ([]string, int32) {
	rsList, err := cs.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, 0
	}
	rsUIDs := map[types.UID]struct{}{}
	for _, rs := range rsList.Items {
		for _, own := range rs.OwnerReferences {
			if own.Kind == "Deployment" && own.UID == deployUID {
				rsUIDs[rs.UID] = struct{}{}
				break
			}
		}
	}
	if len(rsUIDs) == 0 {
		return nil, 0
	}
	list, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, 0
	}
	names := make([]string, 0)
	var ready int32
	for _, p := range list.Items {
		owned := false
		for _, own := range p.OwnerReferences {
			if own.Kind == "ReplicaSet" {
				if _, ok := rsUIDs[own.UID]; ok {
					owned = true
					break
				}
			}
		}
		if !owned {
			continue
		}
		names = append(names, p.Name)
		if podReady(p) {
			ready++
		}
	}
	sort.Strings(names)
	return names, ready
}

func podsOwnedBy(ctx context.Context, cs kubernetes.Interface, ns, kind string, uid types.UID) ([]string, int32) {
	list, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, 0
	}
	names := make([]string, 0)
	var ready int32
	for _, p := range list.Items {
		owned := false
		for _, own := range p.OwnerReferences {
			if own.Kind == kind && own.UID == uid {
				owned = true
				break
			}
		}
		if !owned {
			continue
		}
		names = append(names, p.Name)
		if podReady(p) {
			ready++
		}
	}
	sort.Strings(names)
	return names, ready
}

func podReady(p corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func deref(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// StreamPodsLogs follows one or more pods, prefixes each line with pod name when
// multiple pods are streamed, and rewrites timestamps to Asia/Karachi (PKT).
// When previous is true, streams the prior terminated container instance (useful for CrashLoopBackOff).
func (c *Client) StreamPodsLogs(ctx context.Context, namespace string, pods []string, container string, tail int64, previous bool, w http.ResponseWriter) error {
	if len(pods) == 0 {
		return fmt.Errorf("no pods")
	}
	flusher, _ := w.(http.Flusher)
	var writeMu sync.Mutex
	writeLine := func(pod string, raw []byte) error {
		// Always prefix pod name so colored source badges work for single- and multi-pod streams.
		formatted := formatLogLine(raw, pod, true)
		writeMu.Lock()
		defer writeMu.Unlock()
		if _, err := w.Write(formatted); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}

	if len(pods) == 1 {
		return c.streamOne(ctx, namespace, pods[0], container, tail, previous, func(line []byte) error {
			return writeLine(pods[0], line)
		})
	}

	errCh := make(chan error, len(pods))
	var wg sync.WaitGroup
	for _, pod := range pods {
		pod := pod
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c.streamOne(ctx, namespace, pod, container, tail, previous, func(line []byte) error {
				return writeLine(pod, line)
			})
			if err != nil && ctx.Err() == nil {
				errCh <- fmt.Errorf("%s: %w", pod, err)
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return nil
	case <-done:
		select {
		case err := <-errCh:
			return err
		default:
			return nil
		}
	}
}

func (c *Client) streamOne(ctx context.Context, namespace, pod, container string, tail int64, previous bool, onLine func([]byte) error) error {
	opts := &corev1.PodLogOptions{
		Follow:     !previous, // previous instance is finite — do not follow
		Timestamps: true,
		TailLines:  &tail,
		Previous:   previous,
	}
	if container != "" {
		opts.Container = container
	}
	req := c.cs.CoreV1().Pods(namespace).GetLogs(pod, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("log stream: %w", err)
	}
	defer stream.Close()

	reader := bufio.NewReader(stream)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if werr := onLine(line); werr != nil {
				return werr
			}
		}
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

var (
	isoRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)
)

func toPKTString(s string) string {
	return isoRe.ReplaceAllStringFunc(s, func(m string) string {
		t, err := time.Parse(time.RFC3339Nano, m)
		if err != nil {
			t, err = time.Parse(time.RFC3339, m)
		}
		if err != nil {
			return m
		}
		return t.In(pktZone).Format("2006-01-02 15:04:05.000 PKT")
	})
}

// formatLogLine converts leading RFC3339 timestamp to PKT and optionally prefixes pod.
// ANSI color codes in the message body are preserved for the viewer to render.
func formatLogLine(raw []byte, pod string, withPod bool) []byte {
	line := bytes.TrimRight(raw, "\r\n")
	msg := string(line)
	var out strings.Builder
	if withPod {
		out.WriteString("[")
		out.WriteString(shortPod(pod))
		out.WriteString("] ")
	}
	tsStr, rest, ok := splitTimestamp(msg)
	if ok && strings.Contains(tsStr, "T") {
		if t, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
			out.WriteString(t.In(pktZone).Format("2006-01-02 15:04:05.000 PKT"))
			out.WriteString(" ")
			out.WriteString(strings.TrimLeft(rest, " "))
		} else if t, err := time.Parse(time.RFC3339, tsStr); err == nil {
			out.WriteString(t.In(pktZone).Format("2006-01-02 15:04:05.000 PKT"))
			out.WriteString(" ")
			out.WriteString(strings.TrimLeft(rest, " "))
		} else {
			out.WriteString(toPKTString(msg))
		}
	} else {
		out.WriteString(toPKTString(msg))
	}
	out.WriteByte('\n')
	return []byte(out.String())
}

func splitTimestamp(msg string) (ts, rest string, ok bool) {
	sp := strings.IndexByte(msg, ' ')
	if sp <= 0 {
		return "", msg, false
	}
	ts = msg[:sp]
	if !strings.Contains(ts, "T") {
		return "", msg, false
	}
	return ts, msg[sp+1:], true
}

func shortPod(pod string) string {
	// keep readable; full name is fine for clarity when multiplexing
	return pod
}

func (c *Client) PodContainers(ctx context.Context, namespace, pod string) ([]string, error) {
	p, err := c.cs.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(p.Spec.Containers))
	for _, ctn := range p.Spec.Containers {
		out = append(out, ctn.Name)
	}
	return out, nil
}
