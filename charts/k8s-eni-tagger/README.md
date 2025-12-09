# k8s-eni-tagger Helm Chart

A Helm chart for deploying the k8s-eni-tagger controller, which automatically tags AWS Elastic Network Interfaces (ENIs) associated with Kubernetes Pods based on Pod annotations.

## Features

✅ **IRSA Support** - Full AWS IAM Roles for Service Accounts integration  
✅ **Custom Service Account** - Use existing SA or create new with custom annotations  
✅ **Security Group Binding** - Pod-level security group assignment via annotations  
✅ **High Availability** - Automatic leader election when running multiple replicas  
✅ **Metrics & Monitoring** - Built-in Prometheus metrics endpoint with Service  
✅ **Flexible Configuration** - All controller flags exposed and configurable  
✅ **Cache Persistence** - Optional ConfigMap-backed cache with automatic RBAC  
✅ **Security Best Practices** - Control `automountServiceAccountToken`, run as non-root  
✅ **Production Ready** - Resource limits, health checks, anti-affinity support  

## Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- AWS EKS cluster (or self-managed Kubernetes on AWS EC2)
- IAM permissions for EC2 ENI operations

## Installation

### Add the Helm repository

```bash
helm repo add k8s-eni-tagger https://prabhu-mannu.github.io/k8s-eni-tagger
helm repo update
```

### Install the chart

```bash
helm install k8s-eni-tagger k8s-eni-tagger/k8s-eni-tagger \
  --namespace kube-system \
  --create-namespace
```

### Install with custom values

```bash
helm install k8s-eni-tagger k8s-eni-tagger/k8s-eni-tagger \
  --namespace kube-system \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"="arn:aws:iam::123456789012:role/k8s-eni-tagger" \
  --set config.enableLeaderElection=true \
  --set replicaCount=2
```

## Configuration

### Service Account & IRSA

The chart supports AWS IAM Roles for Service Accounts (IRSA) for secure AWS API access:

```yaml
serviceAccount:
  create: true
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/k8s-eni-tagger
  automountServiceAccountToken: true
```

**Using an existing service account:**

```yaml
serviceAccount:
  create: false
  name: my-existing-service-account
```

### EKS Security Group Binding

To bind specific security groups to the controller pod:

```yaml
podAnnotations:
  vpc.amazonaws.com/pod-eni: '{"securityGroups": ["sg-xxxxxxxxx", "sg-yyyyyyyyy"]}'
```

### High Availability Setup

For high availability, simply set `replicaCount` > 1. Leader election is automatically enabled:

```yaml
replicaCount: 2  # Leader election automatically enabled
```

You can also explicitly control leader election:

```yaml
replicaCount: 1

config:
  enableLeaderElection: true  # Force enable even with single replica
```

**Note:** When `replicaCount > 1`, leader election is automatically enabled regardless of the `enableLeaderElection` setting to prevent split-brain scenarios.

### Controller Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `config.annotationKey` | Annotation key to watch for tags | `eni-tagger.io/tags` |
| `config.watchNamespace` | Namespace to watch (empty = all) | `""` |
| `config.maxConcurrentReconciles` | Concurrent reconciliation workers | `1` |
| `config.dryRun` | Enable dry-run mode (no AWS changes) | `false` |
| `config.enableLeaderElection` | Enable leader election for HA (auto-enabled when replicaCount > 1) | `false` |
| `config.metricsBindAddress` | Metrics endpoint bind address | `:8090` |
| `config.healthProbeBindAddress` | Health probe bind address | `:8081` |
| `config.subnetIDs` | Comma-separated allowed subnet IDs | `""` |
| `config.allowSharedENITagging` | Allow tagging shared ENIs (WARNING) | `false` |
| `config.enableENICache` | Enable in-memory ENI cache | `true` |
| `config.enableCacheConfigMap` | Enable ConfigMap cache persistence | `false` |
| `config.awsRateLimitQPS` | AWS API rate limit (QPS) | `10` |
| `config.awsRateLimitBurst` | AWS API burst limit | `20` |
| `config.pprofBindAddress` | Pprof profiling endpoint (0=disabled) | `"0"` |

### Metrics

The chart creates a Service for Prometheus metrics scraping:

```yaml
metrics:
  enabled: true
  type: ClusterIP
  port: 8090
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "8090"
    prometheus.io/path: "/metrics"
```

### Resources

```yaml
resources:
  limits:
    cpu: 500m
    memory: 128Mi
  requests:
    cpu: 10m
    memory: 64Mi
```

### Environment Variables

```yaml
env:
  AWS_REGION: us-east-1
  AWS_DEFAULT_REGION: us-east-1

envFrom:
  - configMapRef:
      name: my-config
  - secretRef:
      name: my-secret
```

### Volume Mounts

```yaml
extraVolumes:
  - name: cache-volume
    emptyDir: {}

extraVolumeMounts:
  - name: cache-volume
    mountPath: /cache
```

## IAM Permissions

The controller requires the following IAM permissions:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeNetworkInterfaces",
        "ec2:CreateTags",
        "ec2:DeleteTags"
      ],
      "Resource": "*"
    }
  ]
}
```

### Creating an IAM Role for IRSA

```bash
# Create the IAM policy
aws iam create-policy \
  --policy-name k8s-eni-tagger-policy \
  --policy-document file://iam-policy.json

# Create the IAM role and associate it with the service account
eksctl create iamserviceaccount \
  --name k8s-eni-tagger \
  --namespace kube-system \
  --cluster my-cluster \
  --attach-policy-arn arn:aws:iam::123456789012:policy/k8s-eni-tagger-policy \
  --approve
```

## Examples

### Basic Installation with IRSA

```yaml
# values.yaml
serviceAccount:
  create: true
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/k8s-eni-tagger

config:
  watchNamespace: default
  maxConcurrentReconciles: 5
```

### High Availability Setup

```yaml
# values.yaml
replicaCount: 3

serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/k8s-eni-tagger

config:
  enableLeaderElection: true
  maxConcurrentReconciles: 10
  awsRateLimitQPS: 20
  awsRateLimitBurst: 50

resources:
  limits:
    cpu: 1000m
    memory: 256Mi
  requests:
    cpu: 100m
    memory: 128Mi

affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          labelSelector:
            matchLabels:
              app.kubernetes.io/name: k8s-eni-tagger
          topologyKey: kubernetes.io/hostname
```

### Dry-Run Mode for Testing

```yaml
# values.yaml
config:
  dryRun: true
  watchNamespace: test
```

## Upgrading

```bash
helm upgrade k8s-eni-tagger k8s-eni-tagger/k8s-eni-tagger \
  --namespace kube-system \
  --values values.yaml
```

## Uninstalling

```bash
helm uninstall k8s-eni-tagger --namespace kube-system
```

## Values

See [values.yaml](values.yaml) for the full list of configurable parameters.

## Support

- GitHub Issues: https://github.com/prabhu-mannu/k8s-eni-tagger/issues
- Documentation: https://github.com/prabhu-mannu/k8s-eni-tagger
