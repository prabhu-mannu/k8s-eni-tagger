# k8s-eni-tagger

![Build Status](https://github.com/prabhu-mannu/k8s-eni-tagger/actions/workflows/test.yaml/badge.svg)
![Release](https://img.shields.io/github/v/release/prabhu-mannu/k8s-eni-tagger)
![Go Version](https://img.shields.io/github/go-mod/go-version/prabhu-mannu/k8s-eni-tagger)
![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)

**k8s-eni-tagger** is a Kubernetes controller that automatically tags AWS Elastic Network Interfaces (ENIs) associated with Pods based on Pod annotations. This allows you to propagate metadata from Kubernetes Pods to AWS resources for cost allocation, security groups, or automation.

## 🚀 Features

- **Automatic Tagging**: Watches for `eni-tagger.io/tags` annotation on Pods.
- **ENI Resolution**: Automatically finds the ENI associated with the Pod's IP.
- **Reconciliation**: Ensures tags are always in sync (adds missing, removes obsolete).
- **Safety**:
  - **Dry-Run Mode**: Preview changes without modifying AWS resources.
  - **Namespace Filtering**: Restrict operations to specific namespaces.
  - **Retry Logic**: Robust handling of AWS API throttling and errors.
- **Observability**:
  - **Prometheus Metrics**: Tracks API latency, operation counts, and active workers.
  - **Health Checks**: Readiness probe verifies AWS connectivity.

### Rate Limiting Tuning

The controller interacts with the AWS EC2 API, which has strict rate limits.

- `--aws-rate-limit-qps`: Defaults to 10. Increase this for large clusters with high pod churn, but beware of `RequestLimitExceeded` errors.
- `--aws-rate-limit-burst`: Defaults to 20. Allows short bursts of activity (e.g., node startup).
- **Recommendation**: For >1000 pods, consider increasing QPS to 20 or 30, and Burst to 50. Monitor the `aws_api_latency_seconds` metric.

## 📦 Installation

### Prerequisites

- Kubernetes Cluster (EKS recommended)
- kubectl configured to access your cluster
- AWS IAM Permissions:

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

**Note**: Attach this policy to the service account (via IRSA) or the node IAM role.

### Deploy with Helm (Recommended)

**Method 1: OCI Registry (Fastest)**
```bash
helm install k8s-eni-tagger oci://ghcr.io/prabhu-mannu/charts/k8s-eni-tagger \
  --version 0.3.0 \
  --namespace kube-system --create-namespace
```

**Method 2: Helm Repository**
```bash
helm repo add k8s-eni-tagger https://prabhu-mannu.github.io/k8s-eni-tagger
helm repo update
helm install k8s-eni-tagger k8s-eni-tagger/k8s-eni-tagger --namespace kube-system
```

**Method 3: GitHub Release (Tarball)**
```bash
helm install k8s-eni-tagger https://github.com/prabhu-mannu/k8s-eni-tagger/releases/download/v0.2.1/k8s-eni-tagger-0.3.0.tgz \
  --namespace kube-system --create-namespace
```

### Deploy with Manifests

```bash
kubectl apply -f https://github.com/prabhu-mannu/k8s-eni-tagger/releases/download/v0.2.1/manifests.yaml
```

Or locally:

```bash
kubectl apply -f deploy/manifests.yaml
```

### Configuration Flags

| Flag                          | Default              | Description                                                                  |
| ----------------------------- | -------------------- | ---------------------------------------------------------------------------- |
| `--annotation-key`            | `eni-tagger.io/tags` | Annotation key to watch for tags.                                            |
| `--watch-namespace`           | `""` (all)           | Namespace to watch. If empty, watches all.                                   |
| `--max-concurrent-reconciles` | `1`                  | Number of concurrent worker threads.                                         |
| `--dry-run`                   | `false`              | Enable dry-run mode (no AWS changes).                                        |
| `--metrics-bind-address`      | `:8090`              | Address to bind Prometheus metrics.                                          |
| `--health-probe-bind-address` | `:8081`              | Address to bind health probes.                                               |
| `--subnet-ids`                | `""`                 | Comma-separated list of allowed Subnet IDs (or via `ENI_TAGGER_SUBNET_IDS`). |
| `--allow-shared-eni-tagging`  | `false`              | Allow tagging of shared ENIs (e.g., standard EKS nodes). Use with caution.   |
| `--enable-eni-cache`          | `true`               | Enable in-memory ENI caching (lifecycle-based).                              |
| `--enable-cache-configmap`    | `false`              | Enable ConfigMap persistence for ENI cache to survive restarts.              |
| `--aws-rate-limit-qps`        | `10`                 | AWS API rate limit (requests per second).                                    |
| `--aws-rate-limit-burst`      | `20`                 | AWS API rate limit burst.                                                    |
| `--pprof-bind-address`        | `0` (disabled)       | Address to bind pprof endpoint.                                              |

## 📖 Usage

Annotate your Pod with `eni-tagger.io/tags` (or your configured key). The value should be a comma-separated list of `key=value` pairs.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-app
  annotations:
    eni-tagger.io/tags: "CostCenter=1234,Team=Platform,Environment=Production"
spec:
  containers:
    - name: nginx
      image: nginx
```

The controller will apply the following tags to the Pod's ENI:

- `CostCenter`: `1234`
- `Team`: `Platform`
- `Environment`: `Production`

## 🏗️ Architecture

The controller uses the **Kubernetes Controller Runtime** library.

1.  **Watch**: Listens for Pod events (Create, Update).
2.  **Filter**: Ignores Pods without the target annotation or those using HostNetwork.
3.  **Reconcile**:
    - Parses the annotation.
    - Resolves the Pod IP to an ENI ID using `ec2:DescribeNetworkInterfaces`.
    - Calculates the diff between desired tags and current tags (using a `last-applied` annotation for state tracking).
    - Calls `ec2:CreateTags` or `ec2:DeleteTags` as needed.
4.  **Status**: Updates Pod status conditions (`eni-tagger.io/tagged`).

## 🤝 Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for details on how to contribute.

## 📄 License

Apache 2.0 - See [LICENSE](LICENSE) for details.
