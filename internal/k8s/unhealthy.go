package k8s

import (
	"context"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/config"
)

// UnhealthyPod is a pod in CrashLoopBackOff, Failed, or other error state
// that belongs to an Admin-allowlisted workload.
type UnhealthyPod struct {
	Kind         string `json:"kind"`
	Namespace    string `json:"namespace"`
	Workload     string `json:"workload"`
	Pod          string `json:"pod"`
	Container    string `json:"container,omitempty"`
	Phase        string `json:"phase"`
	Reason       string `json:"reason"`
	Message      string `json:"message,omitempty"`
	RestartCount int32  `json:"restartCount"`
	Severity     string `json:"severity"` // crashloop | failed | error | waiting
	PreviousLogs bool   `json:"previousLogs"`
}

// ListUnhealthyPods returns CrashLoop / Failed / error pods for Admin-scoped workloads only.
func (c *Client) ListUnhealthyPods(ctx context.Context, cfg config.Settings, namespaces []string) ([]UnhealthyPod, error) {
	namespaces = config.FilterNamespacesForScan(cfg, namespaces)
	var out []UnhealthyPod

	for _, ns := range namespaces {
		if cfg.Workloads.Deployments {
			list, err := c.cs.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, d := range list.Items {
					if !config.WorkloadAllowed(cfg, ns, "Deployment", d.Name) {
						continue
					}
					pods := podsForDeploymentObjs(ctx, c.cs, ns, d.UID)
					out = append(out, classifyOwnedPods(ns, "Deployment", d.Name, pods)...)
				}
			}
		}
		if cfg.Workloads.StatefulSets {
			list, err := c.cs.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, st := range list.Items {
					if !config.WorkloadAllowed(cfg, ns, "StatefulSet", st.Name) {
						continue
					}
					pods := podsOwnedByObjs(ctx, c.cs, ns, "StatefulSet", st.UID)
					out = append(out, classifyOwnedPods(ns, "StatefulSet", st.Name, pods)...)
				}
			}
		}
		if cfg.Workloads.DaemonSets {
			list, err := c.cs.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, d := range list.Items {
					if !config.WorkloadAllowed(cfg, ns, "DaemonSet", d.Name) {
						continue
					}
					pods := podsOwnedByObjs(ctx, c.cs, ns, "DaemonSet", d.UID)
					out = append(out, classifyOwnedPods(ns, "DaemonSet", d.Name, pods)...)
				}
			}
		}
		if cfg.Workloads.Jobs {
			list, err := c.cs.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, j := range list.Items {
					if !config.WorkloadAllowed(cfg, ns, "Job", j.Name) {
						continue
					}
					pods := podsOwnedByObjs(ctx, c.cs, ns, "Job", j.UID)
					out = append(out, classifyOwnedPods(ns, "Job", j.Name, pods)...)
				}
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return severityRank(out[i].Severity) < severityRank(out[j].Severity)
		}
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		if out[i].Workload != out[j].Workload {
			return out[i].Workload < out[j].Workload
		}
		return out[i].Pod < out[j].Pod
	})
	return out, nil
}

func severityRank(s string) int {
	switch s {
	case "crashloop":
		return 0
	case "failed":
		return 1
	case "error":
		return 2
	default:
		return 3
	}
}

func podsForDeploymentObjs(ctx context.Context, cs kubernetes.Interface, ns string, deployUID types.UID) []corev1.Pod {
	rsList, err := cs.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
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
		return nil
	}
	list, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	var out []corev1.Pod
	for _, p := range list.Items {
		for _, own := range p.OwnerReferences {
			if own.Kind == "ReplicaSet" {
				if _, ok := rsUIDs[own.UID]; ok {
					out = append(out, p)
					break
				}
			}
		}
	}
	return out
}

func podsOwnedByObjs(ctx context.Context, cs kubernetes.Interface, ns, kind string, uid types.UID) []corev1.Pod {
	list, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	var out []corev1.Pod
	for _, p := range list.Items {
		for _, own := range p.OwnerReferences {
			if own.Kind == kind && own.UID == uid {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

func classifyOwnedPods(ns, kind, workload string, pods []corev1.Pod) []UnhealthyPod {
	var out []UnhealthyPod
	for _, p := range pods {
		if u, ok := ClassifyPodHealth(ns, kind, workload, p); ok {
			out = append(out, u)
		}
	}
	return out
}

// ClassifyPodHealth returns unhealthy details when the pod is failing / crash-looping.
// Exported for unit tests.
func ClassifyPodHealth(ns, kind, workload string, p corev1.Pod) (UnhealthyPod, bool) {
	base := UnhealthyPod{
		Kind:      kind,
		Namespace: ns,
		Workload:  workload,
		Pod:       p.Name,
		Phase:     string(p.Status.Phase),
	}

	if p.Status.Phase == corev1.PodFailed {
		base.Reason = firstNonEmpty(p.Status.Reason, "Failed")
		base.Message = p.Status.Message
		base.Severity = "failed"
		base.PreviousLogs = true
		if c, rc, msg := worstContainer(p); c != "" {
			base.Container = c
			base.RestartCount = rc
			if msg != "" && base.Message == "" {
				base.Message = msg
			}
		}
		return base, true
	}

	if p.Status.Phase == corev1.PodSucceeded {
		return UnhealthyPod{}, false
	}

	statuses := append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...)
	statuses = append(statuses, p.Status.ContainerStatuses...)

	for _, st := range statuses {
		if hit, reason, msg, sev, prev := containerUnhealthy(st); hit {
			base.Container = st.Name
			base.Reason = reason
			base.Message = msg
			base.RestartCount = st.RestartCount
			base.Severity = sev
			base.PreviousLogs = prev || st.RestartCount > 0
			return base, true
		}
	}

	if p.Status.Phase == corev1.PodPending {
		reason := p.Status.Reason
		msg := p.Status.Message
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
				reason = firstNonEmpty(c.Reason, reason, "Unschedulable")
				msg = firstNonEmpty(c.Message, msg)
				base.Reason = reason
				base.Message = msg
				base.Severity = "waiting"
				return base, true
			}
		}
	}

	return UnhealthyPod{}, false
}

func containerUnhealthy(st corev1.ContainerStatus) (hit bool, reason, msg, severity string, previous bool) {
	if st.State.Waiting != nil {
		w := st.State.Waiting
		r := w.Reason
		switch r {
		case "CrashLoopBackOff":
			return true, r, w.Message, "crashloop", true
		case "ImagePullBackOff", "ErrImagePull", "InvalidImageName",
			"CreateContainerConfigError", "CreateContainerError",
			"RunContainerError", "Error":
			return true, r, w.Message, "error", st.RestartCount > 0
		}
		if st.RestartCount >= 3 && (r == "ContainerCreating" || r == "" || strings.Contains(strings.ToLower(r), "back")) {
			return true, firstNonEmpty(r, "Restarting"), w.Message, "crashloop", true
		}
	}
	if st.State.Terminated != nil {
		t := st.State.Terminated
		if t.ExitCode != 0 {
			r := firstNonEmpty(t.Reason, "Error")
			return true, r, firstNonEmpty(t.Message, msgForExit(t.ExitCode)), "failed", true
		}
	}
	if st.LastTerminationState.Terminated != nil && st.RestartCount > 0 && !st.Ready {
		t := st.LastTerminationState.Terminated
		if t.ExitCode != 0 {
			return true, firstNonEmpty(t.Reason, "CrashLoopBackOff"), t.Message, "crashloop", true
		}
	}
	return false, "", "", "", false
}

func worstContainer(p corev1.Pod) (name string, restarts int32, msg string) {
	statuses := append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...)
	statuses = append(statuses, p.Status.ContainerStatuses...)
	for _, st := range statuses {
		if st.RestartCount >= restarts {
			restarts = st.RestartCount
			name = st.Name
			if st.State.Terminated != nil {
				msg = st.State.Terminated.Message
			} else if st.State.Waiting != nil {
				msg = st.State.Waiting.Message
			}
		}
	}
	return name, restarts, msg
}

func msgForExit(code int32) string {
	return "exit code " + strconv.FormatInt(int64(code), 10)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ResolvePodWorkload finds the Admin-facing owner (Deployment / StatefulSet / …) for a pod.
func (c *Client) ResolvePodWorkload(ctx context.Context, namespace, podName string) (kind, name string, err error) {
	p, err := c.cs.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	for _, own := range p.OwnerReferences {
		switch own.Kind {
		case "ReplicaSet":
			rs, err := c.cs.AppsV1().ReplicaSets(namespace).Get(ctx, own.Name, metav1.GetOptions{})
			if err != nil {
				return "ReplicaSet", own.Name, nil
			}
			for _, ro := range rs.OwnerReferences {
				if ro.Kind == "Deployment" {
					return "Deployment", ro.Name, nil
				}
			}
			return "ReplicaSet", own.Name, nil
		case "StatefulSet", "DaemonSet", "Job":
			return own.Kind, own.Name, nil
		}
	}
	return "", "", nil
}
