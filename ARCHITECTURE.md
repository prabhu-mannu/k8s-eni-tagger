# Architecture

The **k8s-eni-tagger** follows the standard Kubernetes Controller pattern.

## High-Level Overview

```mermaid
graph TD
    User[User] -->|Annotates| Pod[Pod]
    Pod -->|Event| Controller[k8s-eni-tagger Controller]

    subgraph Controller Logic
        Controller -->|1. Watch| Cache[Informer Cache]
        Controller -->|2. Reconcile| Reconciler[Pod Reconciler]
        Reconciler -->|3. Check| ENICache[ENI Cache]
        Reconciler -->|4. Parse| Annotation[Annotation Parser]
        Reconciler -->|5. Resolve| AWS[AWS Client]
        ENICache -.->|Persist| ConfigMap[Cache ConfigMap]
    end

    AWS -->|DescribeNetworkInterfaces| EC2[AWS EC2 API]
    AWS -->|CreateTags/DeleteTags| EC2

    Reconciler -->|6. Update Status| Pod
    Reconciler -->|7. Emit Event| Events[Kubernetes Events]
```

## Components

1.  **Manager**: Scaffolds the controller, handles leader election, and manages the lifecycle.
2.  **Pod Reconciler**: The core logic loop.
    - **Watch**: Listens for Pod Create/Update events.
    - **Filter**: Ignores irrelevant Pods (no annotation, hostNetwork).
    - **Sync**: Calculates the difference between desired tags (annotation) and actual tags (state).
3.  **AWS Client**: Wrapper around AWS SDK v2 with **Rate Limiting**.
    - Handles API calls (`DescribeNetworkInterfaces`, `CreateTags`, `DeleteTags`).
    - **Token Bucket Rate Limiter**: Controls QPS to prevent AWS API throttling.
    - Implements retry logic with exponential backoff.
    - Instruments metrics for latency and errors.
4.  **ENI Cache**:
    - Caches ENI IDs resolved from Pod IPs.
    - **Lifecycle-based**: Cache entries are invalidated only when the Pod is deleted (not TTL-based). This ensures consistency and reduces unnecessary AWS API calls.
    - Optional **ConfigMap Persistence**: Preserves cache across controller restarts to reduce API calls on startup.
5.  **Metrics Server**: Exposes Prometheus metrics (`/metrics`).
6.  **Health Probes**: Exposes Liveness (`/healthz`) and Readiness (`/readyz`) endpoints.

## Security & IAM Permissions

The controller requires the following AWS IAM permissions to function:

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

**Note**: The controller uses the default AWS credential chain (IRSA, instance profile, etc.). Ensure the service account or node has the appropriate IAM role attached.

## Data Flow

1.  User creates a Pod with `eni-tagger.io/tags: '{"Team":"Dev"}'`.
2.  Controller receives an event.
3.  Controller checks if Pod has an IP. If not, it waits.
4.  Controller checks the ENI cache first. If not cached, it calls AWS `DescribeNetworkInterfaces` with the Pod IP to find the ENI ID.
5.  Controller compares "Team=Dev" with the last applied tags.
6.  Controller calls AWS `CreateTags` on the ENI.
7.  Controller updates Pod status to `Synced` and emits a `Normal` event.
8.  On Pod deletion, the controller removes the cache entry and cleans up finalizers.
