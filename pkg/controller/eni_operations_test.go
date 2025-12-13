package controller

import (
	"context"
	"sync"
	"testing"

	"k8s-eni-tagger/pkg/aws"

	"github.com/stretchr/testify/assert"
)

func TestValidateENI(t *testing.T) {
	tests := []struct {
		name        string
		reconciler  *PodReconciler
		eniInfo     *aws.ENIInfo
		expectError bool
		errorMsg    string
	}{
		{
			name:       "Basic Success",
			reconciler: &PodReconciler{PodRateLimiters: &sync.Map{}, PodRateLimitQPS: 0.1, PodRateLimitBurst: 1},
			eniInfo:    &aws.ENIInfo{ID: "eni-1", SubnetID: "subnet-1"},
		},
		{
			name: "Subnet Filter Success",
			reconciler: &PodReconciler{
				SubnetIDs:         []string{"subnet-1", "subnet-2"},
				PodRateLimiters:   &sync.Map{},
				PodRateLimitQPS:   0.1,
				PodRateLimitBurst: 1,
			},
			eniInfo: &aws.ENIInfo{ID: "eni-1", SubnetID: "subnet-1"},
		},
		{
			name: "Subnet Filter Failure",
			reconciler: &PodReconciler{
				SubnetIDs:         []string{"subnet-1"},
				PodRateLimiters:   &sync.Map{},
				PodRateLimitQPS:   0.1,
				PodRateLimitBurst: 1,
			},
			eniInfo:     &aws.ENIInfo{ID: "eni-1", SubnetID: "subnet-2"},
			expectError: true,
			errorMsg:    "ENI eni-1 subnet subnet-2 is not in allowed subnet list",
		},
		{
			name:        "Shared ENI Blocked",
			reconciler:  &PodReconciler{AllowSharedENITagging: false, PodRateLimiters: &sync.Map{}, PodRateLimitQPS: 0.1, PodRateLimitBurst: 1},
			eniInfo:     &aws.ENIInfo{ID: "eni-1", IsShared: true},
			expectError: true,
			errorMsg:    "ENI eni-1 is shared",
		},
		{
			name:       "Shared ENI Allowed",
			reconciler: &PodReconciler{AllowSharedENITagging: true, PodRateLimiters: &sync.Map{}, PodRateLimitQPS: 0.1, PodRateLimitBurst: 1},
			eniInfo:    &aws.ENIInfo{ID: "eni-1", IsShared: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.reconciler.validateENI(context.TODO(), tt.eniInfo)
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
