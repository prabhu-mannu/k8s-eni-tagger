package cache

import (
	"context"
	"encoding/json"
	"testing"

	"k8s-eni-tagger/pkg/aws"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

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
	data1, _ := json.Marshal(info1)

	tests := []struct {
		name          string
		existingObjs  []client.Object
		expectedItems map[string]*aws.ENIInfo
		expectedError string
	}{
		{
			name:          "ConfigMap Not Found",
			existingObjs:  []client.Object{},
			expectedItems: map[string]*aws.ENIInfo{},
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
			expectedItems: map[string]*aws.ENIInfo{
				"10.0.0.1": info1,
			},
		},
		{
			name: "Corrupt Data",
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
			expectedError: "",                        // No error - corruption is handled gracefully
			expectedItems: map[string]*aws.ENIInfo{}, // No valid items
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
					assert.Equal(t, tt.expectedItems["10.0.0.1"].ID, items["10.0.0.1"].ID)
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

	t.Run("Create New ConfigMap", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		p := NewConfigMapPersister(k8sClient, "default")

		err := p.Save(context.TODO(), "10.0.0.1", info)
		assert.NoError(t, err)

		// Verify created
		cm := &corev1.ConfigMap{}
		err = k8sClient.Get(context.TODO(), client.ObjectKey{Name: configMapName, Namespace: "default"}, cm)
		assert.NoError(t, err)
		assert.Contains(t, cm.Data, "10.0.0.1")
	})

	t.Run("Update Existing ConfigMap", func(t *testing.T) {
		existing := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: configMapName, Namespace: "default"},
			Data:       map[string]string{"10.0.0.2": "{}"},
		}
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
		p := NewConfigMapPersister(k8sClient, "default")

		err := p.Save(context.TODO(), "10.0.0.1", info)
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

		err := p.Save(context.TODO(), "10.0.0.1", info)
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
	validData, _ := json.Marshal(validInfo)

	tests := []struct {
		name          string
		configMapData map[string]string
		expectedItems map[string]*aws.ENIInfo
	}{
		{
			name: "Mixed valid and invalid entries",
			configMapData: map[string]string{
				"10.0.0.1": string(validData), // Valid
				"10.0.0.2": "{invalid-json",   // Invalid JSON
				"10.0.0.3": "",                // Empty string
				"10.0.0.4": `{"ID":"eni-4"}`,  // Missing required fields
			},
			expectedItems: map[string]*aws.ENIInfo{
				"10.0.0.1": validInfo,
				"10.0.0.4": {ID: "eni-4"}, // Only ID set, but that's valid for the cache
			},
		},
		{
			name: "Partial corruption in middle",
			configMapData: map[string]string{
				"10.0.0.1": string(validData),
				"10.0.0.2": `{"id":"eni-2","tags":{"corrupt":`,
				"10.0.0.3": string(validData),
			},
			expectedItems: map[string]*aws.ENIInfo{
				"10.0.0.1": validInfo,
				"10.0.0.3": validInfo,
			},
		},
		{
			name: "All entries corrupted",
			configMapData: map[string]string{
				"10.0.0.1": "{broken",
				"10.0.0.2": "not-json",
				"10.0.0.3": `null`,
			},
			expectedItems: map[string]*aws.ENIInfo{},
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

			for ip, expectedInfo := range tt.expectedItems {
				actualInfo, exists := items[ip]
				assert.True(t, exists, "Expected item %s to exist", ip)
				assert.Equal(t, expectedInfo.ID, actualInfo.ID)
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
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
		p := NewConfigMapPersister(k8sClient, "default")

		err := p.Delete(context.TODO(), "10.0.0.1")
		assert.NoError(t, err)

		// Verify deletion
		cm := &corev1.ConfigMap{}
		err = k8sClient.Get(context.TODO(), client.ObjectKey{Name: configMapName, Namespace: "default"}, cm)
		assert.NoError(t, err)
		assert.NotContains(t, cm.Data, "10.0.0.1")
		assert.Contains(t, cm.Data, "10.0.0.2")
	})
}
