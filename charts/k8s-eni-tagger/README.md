# k8s-eni-tagger Helm Chart

A Helm chart for deploying the k8s-eni-tagger controller, which automatically tags AWS Elastic Network Interfaces (ENIs) associated with Kubernetes Pods based on Pod annotations.

## Features

✅ **IRSA Support** - Full AWS IAM Roles for Service Accounts integration  
✅ **Custom Service Account** - Use existing SA or create new with custom annotations  
✅ **Security Groups for Pods** - Pod-level security groups via `SecurityGroupPolicy`  
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

Multiple installation methods are available:

### Method 1: OCI Registry (Recommended)

Install directly from GHCR OCI registry:

```bash
helm install k8s-eni-tagger oci://ghcr.io/prabhu-mannu/charts/k8s-eni-tagger \
  --version 0.1.1 \
  --namespace kube-system \
  --create-namespace
```

### Method 2: Helm Repository (GitHub Pages)

Add the Helm repository:

```bash
helm repo add k8s-eni-tagger https://prabhu-mannu.github.io/k8s-eni-tagger
helm repo update
helm install k8s-eni-tagger k8s-eni-tagger/k8s-eni-tagger \
  --namespace kube-system \
  --create-namespace
```

### Method 3: From GitHub Release (Tarball)

Download from the GitHub release:

```bash
helm install k8s-eni-tagger \
  https://github.com/prabhu-mannu/k8s-eni-tagger/releases/download/v0.1.1/k8s-eni-tagger-0.1.1.tgz \
  --namespace kube-system \
  --create-namespace
```

### Install with Custom Values

```bash
helm install k8s-eni-tagger oci://ghcr.io/prabhu-mannu/charts/k8s-eni-tagger \
  --version 0.1.1 \
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

### EKS Security Group Binding (Security Groups for Pods)

The chart supports EKS security groups for Pods via the `SecurityGroupPolicy` CRD. **Important:** The annotation `vpc.amazonaws.com/pod-eni` is NOT used for security group binding.

#### Enable via Helm values:

```yaml
securityGroupPolicy:
  enabled: true
  groupIds:
    - sg-0123456789abcdef0  # Controller pod security group
```

This creates a `SecurityGroupPolicy` CRD that attaches the specified security groups to controller pods.

**Security Group Requirements:**
- Must allow **inbound** from node security group for kubelet probes (port 8081)
- Must allow **outbound TCP/UDP port 53** to CoreDNS security group
- Must allow **outbound HTTPS (443)** to AWS APIs and Kubernetes API server
- Must allow **inbound Prometheus scraping** if metrics are collected (port 8090)

**Prerequisites:**
1. EKS cluster VPC CNI configured for security groups for Pods:
   ```bash
   kubectl set env daemonset aws-node -n kube-system ENABLE_POD_ENI=true
   ```
2. Cluster IAM role has `AmazonEKSVPCResourceController` policy attached
3. Nitro-based instance types (see [AWS limits.go](https://github.com/aws/amazon-vpc-resource-controller-k8s/blob/master/pkg/aws/vpc/limits.go))

**Manual SecurityGroupPolicy example:**

```yaml
apiVersion: vpcresources.k8s.aws/v1beta1
kind: SecurityGroupPolicy
metadata:
  name: k8s-eni-tagger-sg
  namespace: kube-system
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: k8s-eni-tagger
  securityGroups:
    groupIds:
      - sg-0123456789abcdef0
```

**References:**
- [AWS Security Groups for Pods Documentation](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html)
- [SecurityGroupPolicy CRD Examples](https://docs.aws.amazon.com/eks/latest/userguide/sg-pods-example-deployment.html)

### Network Policy

The chart includes an optional `NetworkPolicy` for controller pod network isolation:

```yaml
networkPolicy:
  enabled: true
```

The NetworkPolicy allows:
- **Ingress:** Prometheus metrics (8090), health probes (8081)
- **Egress:** DNS (53), Kubernetes API (443/6443), AWS APIs (443)

**Note:** Requires a network policy provider (Calico, Cilium, etc.)

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
| `config.metricsBindAddress` | Metrics endpoint bind port/address (bare port auto-prefixed) | `8090` |
| `config.healthProbeBindAddress` | Health probe bind port/address (bare port auto-prefixed) | `8081` |
| `config.subnetIDs` | Comma-separated allowed subnet IDs | `""` |
| `config.allowSharedENITagging` | Allow tagging shared ENIs (WARNING) | `false` |
| `config.enableENICache` | Enable in-memory ENI cache | `true` |
| `config.enableCacheConfigMap` | Enable ConfigMap cache persistence | `false` |
| `config.cacheBatchInterval` | Batch interval for ConfigMap cache persistence | `2s` |
| `config.cacheBatchSize` | Batch size for ConfigMap cache persistence | `20` |
| `config.awsRateLimitQPS` | AWS API rate limit (QPS) | `10` |
| `config.awsRateLimitBurst` | AWS API burst limit | `20` |
| `config.pprofBindAddress` | Pprof profiling endpoint (0=disabled) | `"0"` |
| `config.tagNamespace` | Tag namespacing control ('enable' = use pod namespace prefix) | `""` |
| `config.podRateLimitQPS` | Per-pod reconciliation rate limit (QPS) | `0.1` |
| `config.podRateLimitBurst` | Per-pod rate limit burst size | `1` |
| `config.rateLimiterCleanupInterval` | Cleanup interval for stale per-pod rate limiters | `1m` |

### Security

| Parameter | Description | Default |
|-----------|-------------|---------|
| `securityGroupPolicy.enabled` | Enable SecurityGroupPolicy CRD for controller pods | `false` |
| `securityGroupPolicy.groupIds` | Security group IDs to attach (1-5 groups) | `[]` |
| `networkPolicy.enabled` | Enable NetworkPolicy for pod network isolation | `false` |

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

### Environment Variables and ConfigMap

This chart now generates a ConfigMap (by default) containing controller configuration
values as environment variables using the `ENI_TAGGER_*` prefix. These values come from
`.Values.config` and `.Values.env` and are injected into the controller via `envFrom`.

You can provide additional environment variables using `env` and `envFrom` as usual:

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

To disable the generated configmap and keep manual control, set:

```yaml
configMap:
  create: false
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
      "Sid": "ENITaggingPermissions",
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeNetworkInterfaces",
        "ec2:CreateTags",
        "ec2:DeleteTags"
      ],
      "Resource": "*",
      "Condition": {
        "StringEquals": {
          "aws:RequestedRegion": ["us-west-2"]
        }
      }
    },
    {
      "Sid": "HealthCheckPermissions",
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeAccountAttributes"
      ],
      "Resource": "*"
    }
  ]
}
```

**Required Permissions:**
- `ec2:DescribeNetworkInterfaces`: Query ENI details by Pod IP address
- `ec2:CreateTags`: Apply tags to ENIs
- `ec2:DeleteTags`: Remove tags from ENIs when Pods are deleted
- `ec2:DescribeAccountAttributes`: Startup health check to verify AWS API connectivity

**Optional Conditions:**
- `aws:RequestedRegion`: Restrict to specific AWS regions
- `ec2:ResourceTag/*`: Limit to ENIs with specific tags

**Note:** The `Resource: "*"` is required because ENI IDs are not known at policy creation time. Consider using SCP or resource tags for additional security
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

### Enable Tag Namespacing

Enable automatic pod namespace-based tag namespacing for multi-tenant environments:

```yaml
# values.yaml
config:
  tagNamespace: enable  # Use pod's Kubernetes namespace as tag prefix
```

With namespacing enabled, a pod in the `production` namespace with annotation:
```yaml
eni-tagger.io/tags: '{"CostCenter":"123","Team":"Platform"}'
```

Will result in ENI tags:
```
production:CostCenter=123, production:Team=Platform
```

## Versioning

This chart follows [Semantic Versioning 2.0.0](https://semver.org/):

- **Chart version = App version = Release tag**: All versions are synchronized for simplicity (e.g., chart 0.1.1 = app 0.1.1 = release v0.1.1)
- **MAJOR** (X.0.0): Breaking changes to chart structure or application behavior
- **MINOR** (0.X.0): New features added in a backward-compatible manner
- **PATCH** (0.0.X): Backward-compatible bug fixes

**When upgrading the chart**, always check the [CHANGELOG.md](../../CHANGELOG.md) for breaking changes and migration instructions.

## Upgrading

### Upgrade to a specific version:

```bash
# Using OCI Registry (recommended)
helm upgrade k8s-eni-tagger oci://ghcr.io/prabhu-mannu/charts/k8s-eni-tagger \
  --version 0.1.1 \
  --namespace kube-system \
  --values values.yaml

# Using Helm Repository
helm repo update
helm upgrade k8s-eni-tagger k8s-eni-tagger/k8s-eni-tagger \
  --namespace kube-system \
  --values values.yaml
```

### Check current version:

```bash
helm list -n kube-system
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
- CHANGELOG: https://github.com/prabhu-mannu/k8s-eni-tagger/blob/main/CHANGELOG.md
