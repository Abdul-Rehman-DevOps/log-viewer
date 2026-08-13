package k8s_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/k8s"
)

func TestClassifyCrashLoopBackOff(t *testing.T) {
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-abc"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "api",
				Ready:        false,
				RestartCount: 7,
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{
						Reason:  "CrashLoopBackOff",
						Message: "back-off 5m0s",
					},
				},
			}},
		},
	}
	u, ok := k8s.ClassifyPodHealth("prod", "Deployment", "api", p)
	if !ok {
		t.Fatal("expected unhealthy")
	}
	if u.Severity != "crashloop" || u.Reason != "CrashLoopBackOff" {
		t.Fatalf("got %#v", u)
	}
	if !u.PreviousLogs || u.Container != "api" || u.RestartCount != 7 {
		t.Fatalf("previous/container/restarts: %#v", u)
	}
}

func TestClassifyPodFailed(t *testing.T) {
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "job-x"},
		Status: corev1.PodStatus{
			Phase:   corev1.PodFailed,
			Reason:  "Error",
			Message: "container exit",
		},
	}
	u, ok := k8s.ClassifyPodHealth("dev", "Job", "migrate", p)
	if !ok || u.Severity != "failed" {
		t.Fatalf("expected failed, got ok=%v %#v", ok, u)
	}
}

func TestClassifyImagePullBackOff(t *testing.T) {
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-1"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "web",
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "not found"},
				},
			}},
		},
	}
	u, ok := k8s.ClassifyPodHealth("dev", "Deployment", "web", p)
	if !ok || u.Severity != "error" || u.Reason != "ImagePullBackOff" {
		t.Fatalf("got ok=%v %#v", ok, u)
	}
}

func TestClassifyHealthyRunning(t *testing.T) {
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "ok-1"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "app",
				Ready: true,
				State: corev1.ContainerState{
					Running: &corev1.ContainerStateRunning{},
				},
			}},
		},
	}
	if _, ok := k8s.ClassifyPodHealth("dev", "Deployment", "ok", p); ok {
		t.Fatal("healthy pod must not be classified unhealthy")
	}
}

func TestClassifySucceededJobSkipped(t *testing.T) {
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "job-ok"},
		Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
	}
	if _, ok := k8s.ClassifyPodHealth("dev", "Job", "once", p); ok {
		t.Fatal("succeeded job pods are not unhealthy")
	}
}
