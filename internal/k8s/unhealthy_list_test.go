package k8s_test

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/config"
	"github.com/Abdul-Rehman-DevOps/log-viewer/internal/k8s"
)

// newTestClient uses an unexported-compatible approach via ListUnhealthyPods
// through a thin wrapper constructed like production Client.
func clientWithObjects(t *testing.T, objs ...runtime.Object) *k8s.Client {
	t.Helper()
	fc := fake.NewSimpleClientset(objs...)
	return k8s.NewWithClientset(fc)
}

func TestListUnhealthyPodsRespectsAllowlist(t *testing.T) {
	objs := []runtime.Object{
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "allowed-app", Namespace: "dev", UID: "deploy-uid-1"},
			Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(1)},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "secret-app", Namespace: "dev", UID: "deploy-uid-2"},
			Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(1)},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "allowed-app-rs", Namespace: "dev", UID: "rs-uid-1",
				OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", UID: "deploy-uid-1", Name: "allowed-app"}},
			},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "secret-app-rs", Namespace: "dev", UID: "rs-uid-2",
				OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", UID: "deploy-uid-2", Name: "secret-app"}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "allowed-app-pod", Namespace: "dev",
				OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", UID: "rs-uid-1", Name: "allowed-app-rs"}},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name: "app", RestartCount: 3, Ready: false,
					State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
				}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "secret-app-pod", Namespace: "dev",
				OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", UID: "rs-uid-2", Name: "secret-app-rs"}},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name: "app", RestartCount: 9, Ready: false,
					State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
				}},
			},
		},
	}

	c := clientWithObjects(t, objs...)
	cfg := config.Default()
	cfg.Mode = "include"
	cfg.Include = []string{"dev"}
	cfg.AllowedWorkloads = []string{"dev/Deployment/allowed-app"}

	got, err := c.ListUnhealthyPods(context.Background(), cfg, []string{"dev"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 unhealthy allowlisted pod, got %d %#v", len(got), got)
	}
	if got[0].Pod != "allowed-app-pod" || got[0].Workload != "allowed-app" {
		t.Fatalf("wrong pod returned: %#v", got[0])
	}
}

func int32Ptr(v int32) *int32 { return &v }
