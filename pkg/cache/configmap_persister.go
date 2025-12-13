package cache

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s-eni-tagger/pkg/aws"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	configMapName = "eni-tagger-cache"
)

// configMapPersister implements ConfigMapPersister interface
type configMapPersister struct {
	client    client.Client
	namespace string
}

// NewConfigMapPersister creates a new ConfigMap-based persister
func NewConfigMapPersister(client client.Client, namespace string) ConfigMapPersister {
	return &configMapPersister{
		client:    client,
		namespace: namespace,
	}
}

// Load loads all cached ENI entries from the ConfigMap
func (p *configMapPersister) Load(ctx context.Context) (map[string]*aws.ENIInfo, error) {
	logger := log.FromContext(ctx)

	cm := &corev1.ConfigMap{}
	err := p.client.Get(ctx, client.ObjectKey{
		Namespace: p.namespace,
		Name:      configMapName,
	}, cm)

	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("ENI cache ConfigMap not found, starting fresh")
			return make(map[string]*aws.ENIInfo), nil
		}
		return nil, fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	result := make(map[string]*aws.ENIInfo)
	for ip, data := range cm.Data {
		var info aws.ENIInfo
		if err := json.Unmarshal([]byte(data), &info); err != nil {
			logger.Info("Failed to unmarshal ENI info, skipping entry", "ip", ip, "error", err.Error())
			continue
		}
		result[ip] = &info
	}

	return result, nil
}

// Save persists a single ENI entry to the ConfigMap
func (p *configMapPersister) Save(ctx context.Context, ip string, info *aws.ENIInfo) error {
	logger := log.FromContext(ctx)

	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal ENI info: %w", err)
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm := &corev1.ConfigMap{}
		err := p.client.Get(ctx, client.ObjectKey{
			Namespace: p.namespace,
			Name:      configMapName,
		}, cm)

		if err != nil {
			if apierrors.IsNotFound(err) {
				// Create new ConfigMap
				cm = &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      configMapName,
						Namespace: p.namespace,
					},
					Data: map[string]string{
						ip: string(data),
					},
				}
				if err := p.client.Create(ctx, cm); err != nil {
					return fmt.Errorf("failed to create ConfigMap: %w", err)
				}
				logger.Info("Created ENI cache ConfigMap", "ip", ip)
				return nil
			}
			return err
		}

		// Update with resource version check
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data[ip] = string(data)

		return p.client.Update(ctx, cm)
	})
}

// Delete removes a single ENI entry from the ConfigMap
func (p *configMapPersister) Delete(ctx context.Context, ip string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm := &corev1.ConfigMap{}
		err := p.client.Get(ctx, client.ObjectKey{
			Namespace: p.namespace,
			Name:      configMapName,
		}, cm)

		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil // Already gone
			}
			return err
		}

		if cm.Data == nil {
			return nil
		}

		delete(cm.Data, ip)

		return p.client.Update(ctx, cm)
	})
}
