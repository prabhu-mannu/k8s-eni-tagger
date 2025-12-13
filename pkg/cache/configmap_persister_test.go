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
			expectedItems: map[string]*aws.ENIInfo{}, // Should skip invalid
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

		// Verify deletion
		cm := &corev1.ConfigMap{}
		err = k8sClient.Get(context.TODO(), client.ObjectKey{Name: configMapName, Namespace: "default"}, cm)
		assert.NoError(t, err)
		assert.NotContains(t, cm.Data, "10.0.0.1")
		assert.Contains(t, cm.Data, "10.0.0.2")
	})
}
