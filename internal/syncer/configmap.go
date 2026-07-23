package syncer

import (
	"context"
	"log"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/AIVMNetwork/log-viewer/internal/config"
)

// ConfigMapSync keeps settings shared across replicas.
type ConfigMapSync struct {
	cs        kubernetes.Interface
	namespace string
	name      string
	store     *config.Store
}

func NewConfigMapSync(store *config.Store) (*ConfigMapSync, error) {
	name := os.Getenv("LOG_VIEWER_CONFIGMAP")
	ns := os.Getenv("POD_NAMESPACE")
	if name == "" || ns == "" {
		return nil, nil // disabled
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	s := &ConfigMapSync{cs: cs, namespace: ns, name: name, store: store}
	store.SetOnSave(func(settings config.Settings) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := s.Push(ctx); err != nil {
			log.Printf("configmap sync push: %v", err)
		}
	})
	return s, nil
}

func (s *ConfigMapSync) Bootstrap(ctx context.Context) error {
	if s == nil {
		return nil
	}
	cm, err := s.cs.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return s.Push(ctx)
	}
	if err != nil {
		return err
	}
	if raw, ok := cm.Data["config.json"]; ok && raw != "" {
		if err := s.store.ReplaceFromBytes([]byte(raw)); err != nil {
			log.Printf("configmap load: %v", err)
		} else {
			log.Printf("loaded shared config from ConfigMap %s/%s", s.namespace, s.name)
		}
	}
	return nil
}

func (s *ConfigMapSync) Push(ctx context.Context) error {
	if s == nil {
		return nil
	}
	b, err := s.store.Marshal()
	if err != nil {
		return err
	}
	cm, err := s.cs.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = s.cs.CoreV1().ConfigMaps(s.namespace).Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: s.name, Namespace: s.namespace, Labels: map[string]string{
				"app.kubernetes.io/name": "log-viewer",
			}},
			Data: map[string]string{"config.json": string(b)},
		}, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data["config.json"] = string(b)
	_, err = s.cs.CoreV1().ConfigMaps(s.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

func (s *ConfigMapSync) StartWatch(ctx context.Context) {
	if s == nil {
		return
	}
	go func() {
		t := time.NewTicker(4 * time.Second)
		defer t.Stop()
		var resourceVersion string
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				cm, err := s.cs.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
				if err != nil {
					continue
				}
				if cm.ResourceVersion == resourceVersion {
					continue
				}
				resourceVersion = cm.ResourceVersion
				if raw, ok := cm.Data["config.json"]; ok && raw != "" {
					_ = s.store.ReplaceFromBytes([]byte(raw))
				}
			}
		}
	}()
}
