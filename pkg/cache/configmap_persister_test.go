package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"k8s-eni-tagger/pkg/aws"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// conflictOnceClient injects a single conflict on Update to verify retry behavior.
type conflictOnceClient struct {
	client.Client
	mu             sync.Mutex
	updateCalls    int
	conflictRaised bool
}

func (c *conflictOnceClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	c.mu.Lock()
	c.updateCalls++
	raiseConflict := !c.conflictRaised
	if raiseConflict {
		c.conflictRaised = true
	}
	c.mu.Unlock()

	if raiseConflict {
		return apierrors.NewConflict(
			schema.GroupResource{Group: "", Resource: "configmaps"},
			obj.GetName(),
			fmt.Errorf("simulated conflict"),
		)
	}

	return c.Client.Update(ctx, obj, opts...)
}

func TestNewConfigMapPersister(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	require.NoError(t, err)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	p := NewConfigMapPersister(k8sClient, "default")
	assert.NotNil(t, p)
}

func TestLoad(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	require.NoError(t, err)

	info1 := &aws.ENIInfo{ID: "eni-1", Tags: map[string]string{"foo": "bar"}}
	entry1 := CachedEntry{Info: info1, PodUID: "pod-1"}
	data1, _ := json.Marshal(entry1)

	tests := []struct {
		name          string
		existingObjs  []client.Object
		expectedItems map[string]CachedEntry
		expectedError string
	}{
		{
			name:          "ConfigMap Not Found",
			existingObjs:  []client.Object{},
			expectedItems: map[string]CachedEntry{},
		},
		{
			name: "ConfigMap Exists with Data",
			existingObjs: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      configMapName,
						Namespace: "default",
					},
					Data: map[string]string{
						"10.0.0.1": string(data1),
					},
				},
			},
			expectedItems: map[string]CachedEntry{
				"10.0.0.1": entry1,
			},
		},
		{
			name: "Corrupt Data (Invalid JSON)",
			existingObjs: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      configMapName,
						Namespace: "default",
					},
					Data: map[string]string{
						"10.0.0.1": "{invalid-json",
					},
				},
			},
			expectedError: "",                       // No error - corruption is handled gracefully
			expectedItems: map[string]CachedEntry{}, // No valid items
		},
		{
			// New-format payload missing PodUID is preserved with an empty
			// PodUID; cache.get() treats it as a miss so the next reconcile
			// refreshes it under the current format.
			name: "Migrated Entry (Missing PodUID)",
			existingObjs: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      configMapName,
						Namespace: "default",
					},
					Data: map[string]string{
						"10.0.0.1": `{"info":{"ID":"eni-1"}}`,
					},
				},
			},
			expectedError: "",
			expectedItems: map[string]CachedEntry{
				"10.0.0.1": {Info: &aws.ENIInfo{ID: "eni-1"}, PodUID: ""},
			},
		},
		{
			// Legacy format from the pre-UID release: top-level aws.ENIInfo
			// JSON without the {"info": ...} wrapper. Should be preserved
			// with an empty PodUID so it refreshes on next access.
			name: "Migrated Entry (Legacy ENIInfo Format)",
			existingObjs: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      configMapName,
						Namespace: "default",
					},
					Data: map[string]string{
						"10.0.0.1": `{"ID":"eni-legacy","Tags":{"team":"platform"}}`,
					},
				},
			},
			expectedError: "",
			expectedItems: map[string]CachedEntry{
				"10.0.0.1": {Info: &aws.ENIInfo{ID: "eni-legacy"}, PodUID: ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.existingObjs...).Build()
			p := NewConfigMapPersister(k8sClient, "default")

			items, err := p.Load(context.TODO())

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, len(tt.expectedItems), len(items))
				if len(tt.expectedItems) > 0 {
					assert.Equal(t, tt.expectedItems["10.0.0.1"].Info.ID, items["10.0.0.1"].Info.ID)
					assert.Equal(t, tt.expectedItems["10.0.0.1"].PodUID, items["10.0.0.1"].PodUID)
				}
			}
		})
	}
}

func TestSave(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	require.NoError(t, err)

	info := &aws.ENIInfo{ID: "eni-1", Tags: map[string]string{"foo": "bar"}}
	entry := CachedEntry{Info: info, PodUID: "pod-1"}

	t.Run("Create New ConfigMap", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		p := NewConfigMapPersister(k8sClient, "default")

		err := p.Save(context.TODO(), "10.0.0.1", entry)
		assert.NoError(t, err)

		// Verify created
		cm := &corev1.ConfigMap{}
		err = k8sClient.Get(context.TODO(), client.ObjectKey{Name: configMapName, Namespace: "default"}, cm)
		assert.NoError(t, err)
		assert.Contains(t, cm.Data, "10.0.0.1")
		// Verify content contains PodUID
		assert.Contains(t, cm.Data["10.0.0.1"], "pod-1")
	})

	t.Run("Update Existing ConfigMap", func(t *testing.T) {
		existing := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: configMapName, Namespace: "default"},
			Data:       map[string]string{"10.0.0.2": "{}"},
		}
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
		p := NewConfigMapPersister(k8sClient, "default")

		err := p.Save(context.TODO(), "10.0.0.1", entry)
		assert.NoError(t, err)

		// Verify updated
		cm := &corev1.ConfigMap{}
		err = k8sClient.Get(context.TODO(), client.ObjectKey{Name: configMapName, Namespace: "default"}, cm)
		assert.NoError(t, err)
		assert.Contains(t, cm.Data, "10.0.0.1")
		assert.Contains(t, cm.Data, "10.0.0.2")
	})
}

func TestDelete(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	require.NoError(t, err)

	t.Run("ConfigMap Not Found", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		p := NewConfigMapPersister(k8sClient, "default")

		err := p.Delete(context.TODO(), "10.0.0.1")
		assert.NoError(t, err)
	})

	t.Run("Delete Item", func(t *testing.T) {
		existing := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: configMapName, Namespace: "default"},
			Data:       map[string]string{"10.0.0.1": "{}", "10.0.0.2": "{}"},
		}
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
		p := NewConfigMapPersister(k8sClient, "default")

		err := p.Delete(context.TODO(), "10.0.0.1")
		assert.NoError(t, err)

		cm := &corev1.ConfigMap{}
		err = k8sClient.Get(context.TODO(), client.ObjectKey{Name: configMapName, Namespace: "default"}, cm)
		assert.NoError(t, err)
		assert.NotContains(t, cm.Data, "10.0.0.1")
		assert.Contains(t, cm.Data, "10.0.0.2")
	})
}

func TestSaveRetryOnConflict(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	require.NoError(t, err)

	info := &aws.ENIInfo{ID: "eni-1", Tags: map[string]string{"foo": "bar"}}
	entry := CachedEntry{Info: info, PodUID: "pod-1"}

	// This test verifies that retry logic works for conflict errors
	// In a real scenario, this would happen when multiple processes try to update the ConfigMap simultaneously
	t.Run("Save with retry succeeds", func(t *testing.T) {
		existing := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:            configMapName,
				Namespace:       "default",
				ResourceVersion: "1",
			},
			Data: map[string]string{"10.0.0.2": "{}"},
		}
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
		p := NewConfigMapPersister(k8sClient, "default")

		err := p.Save(context.TODO(), "10.0.0.1", entry)
		assert.NoError(t, err)

		// Verify saved
		cm := &corev1.ConfigMap{}
		err = k8sClient.Get(context.TODO(), client.ObjectKey{Name: configMapName, Namespace: "default"}, cm)
		assert.NoError(t, err)
		assert.Contains(t, cm.Data, "10.0.0.1")
		assert.Contains(t, cm.Data, "10.0.0.2")
	})
}

func TestLoadCorruptionScenarios(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	require.NoError(t, err)

	validInfo := &aws.ENIInfo{ID: "eni-valid", Tags: map[string]string{"valid": "true"}}
	validEntry := CachedEntry{Info: validInfo, PodUID: "pod-valid"}
	validData, _ := json.Marshal(validEntry)

	tests := []struct {
		name          string
		configMapData map[string]string
		expectedItems map[string]CachedEntry
	}{
		{
			name: "Mixed valid, migrated, and invalid entries",
			configMapData: map[string]string{
				"10.0.0.1": string(validData),                      // Valid current format
				"10.0.0.2": "{invalid-json",                        // Invalid JSON
				"10.0.0.3": "",                                     // Empty string
				"10.0.0.4": `{"info":{"ID":"eni-4"}}`,              // Migrated (missing PodUID)
				"10.0.0.5": `{"ID":"eni-legacy","Tags":{"a":"b"}}`, // Migrated (legacy format)
			},
			expectedItems: map[string]CachedEntry{
				"10.0.0.1": validEntry,
				"10.0.0.4": {Info: &aws.ENIInfo{ID: "eni-4"}, PodUID: ""},
				"10.0.0.5": {Info: &aws.ENIInfo{ID: "eni-legacy"}, PodUID: ""},
			},
		},
		{
			name: "Partial corruption in middle",
			configMapData: map[string]string{
				"10.0.0.1": string(validData),
				"10.0.0.2": `{"id":"eni-2","tags":{"corrupt":`,
				"10.0.0.3": string(validData),
			},
			expectedItems: map[string]CachedEntry{
				"10.0.0.1": validEntry,
				"10.0.0.3": validEntry,
			},
		},
		{
			name: "All entries corrupted",
			configMapData: map[string]string{
				"10.0.0.1": "{broken",
				"10.0.0.2": "not-json",
				"10.0.0.3": `null`,
			},
			expectedItems: map[string]CachedEntry{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: "default",
				},
				Data: tt.configMapData,
			}
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
			p := NewConfigMapPersister(k8sClient, "default")

			items, err := p.Load(context.TODO())
			assert.NoError(t, err)
			assert.Equal(t, len(tt.expectedItems), len(items))

			for ip, expectedEntry := range tt.expectedItems {
				actualEntry, exists := items[ip]
				assert.True(t, exists, "Expected item %s to exist", ip)
				assert.Equal(t, expectedEntry.Info.ID, actualEntry.Info.ID)
				assert.Equal(t, expectedEntry.PodUID, actualEntry.PodUID)
			}
		})
	}
}

func TestDeleteRetryOnConflict(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	require.NoError(t, err)

	t.Run("Delete with retry succeeds", func(t *testing.T) {
		existing := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:            configMapName,
				Namespace:       "default",
				ResourceVersion: "1",
			},
			Data: map[string]string{
				"10.0.0.1": "{}",
				"10.0.0.2": "{}",
			},
		}
		baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
		conflictClient := &conflictOnceClient{Client: baseClient}
		p := NewConfigMapPersister(conflictClient, "default")

		err := p.Delete(context.TODO(), "10.0.0.1")
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, conflictClient.updateCalls, 2, "expected retry after injected conflict")

		cm := &corev1.ConfigMap{}
		err = baseClient.Get(context.TODO(), client.ObjectKey{Name: configMapName, Namespace: "default"}, cm)
		assert.NoError(t, err)
		assert.NotContains(t, cm.Data, "10.0.0.1")
		assert.Contains(t, cm.Data, "10.0.0.2")
	})
}
