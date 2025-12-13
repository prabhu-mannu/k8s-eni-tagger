# ENI Tagging Scenarios & Controller Behavior

This document provides a comprehensive guide to understanding how the k8s-eni-tagger controller processes Pod annotations and syncs ENI tags with AWS. It covers design decisions, operational scenarios, and expected behaviors.

---

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Design Rationale](#design-rationale)
3. [Controller Lifecycle](#controller-lifecycle)
4. [Tag Synchronization Scenarios](#tag-synchronization-scenarios)
5. [State Management](#state-management)
6. [Best Practices](#best-practices)
7. [Troubleshooting](#troubleshooting)

---

## Architecture Overview

### Core Components

The k8s-eni-tagger controller operates across three layers:

```
┌─────────────────────────────────────────────────────────────┐
│ Kubernetes Layer                                             │
│ - Pod objects with annotations                              │
│ - Finalizers for cleanup                                    │
│ - Status annotations for state tracking                     │
└─────────────────────────────────────────────────────────────┘
                           ↕
┌─────────────────────────────────────────────────────────────┐
│ Controller Layer                                             │
│ - Pod event watching & filtering                            │
│ - Annotation parsing & validation                           │
│ - Tag diff calculation                                      │
│ - ENI cache (in-memory + optional ConfigMap persistence)   │
└─────────────────────────────────────────────────────────────┘
                           ↕
┌─────────────────────────────────────────────────────────────┐
│ AWS Layer                                                    │
│ - ENI lookups via EC2 API                                   │
│ - Tag creation/deletion                                     │
│ - Rate-limited (default: 10 QPS)                            │
└─────────────────────────────────────────────────────────────┘
```

### Data Flow

```
User creates/updates Pod with annotation
                    ↓
Kubernetes API stores in etcd
                    ↓
Watch stream sends Pod event to controller
                    ↓
Predicate filter evaluates (annotation changed?)
                    ↓
Reconciliation runs: Parse → Compare → Apply → Status
                    ↓
AWS EC2 API receives tag operations
                    ↓
ENI tags updated; Pod status annotations updated
```

---

## Design Rationale

### Why Cache ENI Lookups?

#### Problem
AWS EC2 API calls are:
- **Rate-limited** (account-wide limits)
- **Slow** (100ms+ latency)
- **Expensive** for large clusters with Pod churn

#### Solution: ENI Cache

**In-Memory Cache**
- Stores ENI→IP mappings during controller runtime
- Eliminates redundant AWS calls for the same Pod IP
- Cleared on controller restart (acceptable trade-off)

**Optional ConfigMap Persistence**
- Survives controller restarts and leader failovers
- Warm-starts the cache from previous state
- Reduces AWS call surge after restarts
- Defaults to the controller's own namespace (no extra config needed)
- Can be disabled for resource-constrained environments

#### Why Not Store Everything on the Pod?

Storing ENI data as Pod annotations seems simpler but has critical limitations:

| Approach | Pros | Cons |
|----------|------|------|
| **Pod Annotation** | Visible, queryable | Pod is ephemeral; loses state on deletion; requires RBAC to update Pods; size limits; API server load |
| **In-Memory Cache** | Fast, efficient | Lost on restart (mitigated by ConfigMap) |
| **ConfigMap Cache** | Persists state; survives restarts | Single point of contention; eventual consistency |
| **Hybrid (Current)** | Fast lookup + durability + low API load | Slight complexity |

---

## Controller Lifecycle

### Startup Sequence

```
1. Parse Configuration
   ├─ AWS credentials/region
   ├─ Rate limits (QPS, burst)
   ├─ Cache settings (enabled, ConfigMap namespace)
   └─ Controller behavior (max concurrent reconciles, dry-run)

2. Connect to Kubernetes
   ├─ Load in-cluster credentials or kubeconfig
   ├─ Initialize manager
   └─ Setup event recorder

3. Initialize AWS Client
   ├─ Create EC2 SDK client
   ├─ Apply rate limiter
   └─ Validate IAM permissions (test call)

4. Initialize ENI Cache
   ├─ Create in-memory cache (empty map)
   ├─ If ConfigMap enabled:
   │  └─ Query K8s API for ConfigMap "eni-tagger-cache"
   │     ├─ Parse JSON entries
   │     └─ Populate in-memory map
   └─ Start batch persistence worker goroutine

5. Start Controller
   ├─ Open streaming watch on Pod events
   ├─ Start reconciliation loop
   ├─ Launch rate limiter cleanup goroutine
   ├─ Expose metrics (/metrics) & health endpoints
   └─ Block and run until signal

Result: Controller ready to process Pod events
```

### Restart Behavior

When controller restarts (crash, upgrade, etc.):

```
┌──────────────────────────────────────────────────┐
│ Controller Process Dies                          │
│ (Leader election released, in-memory cache lost) │
└──────────────────────────────────────────────────┘
                    ↓
┌──────────────────────────────────────────────────┐
│ New Controller Instance Starts                   │
│ 1. Loads ENI cache from ConfigMap                │
│ 2. Reconnects to Pod watch stream                │
│ 3. Replays buffered Pod events (~5K events)      │
└──────────────────────────────────────────────────┘
                    ↓
┌──────────────────────────────────────────────────┐
│ Reconciliation Resumes                           │
│ - Predicates filter events (only annotated Pods) │
│ - Tags re-applied (idempotent, safe)            │
│ - Cache warmed up by reconciliation              │
└──────────────────────────────────────────────────┘
                    ↓
Result: Pod tags remain consistent; no data loss
```

**Key Guarantees:**
- Pods and their tags in AWS remain unchanged.
- ConfigMap cache prevents re-hitting AWS for the same IPs.
- Event stream ensures no missed Pod changes (within buffer limits).

---

## Tag Synchronization Scenarios

### Scenario 1: No Annotation (Skip)

```yaml
Pod: my-pod (no eni-tagger.io/tags annotation)

Flow:
  Pod event → Predicate checks for annotation key
  ↓
  Annotation not found → Predicate returns false
  ↓
  Reconciliation skipped (zero cost)

AWS Calls:    0
Action:       None
```

---

### Scenario 2: First Annotation Applied (Tag ENI)

```yaml
Before:
  Pod: my-pod
    Annotations: {}

After (User applies):
  Pod: my-pod
    Annotations:
      eni-tagger.io/tags: '{"Team":"Platform","Cost":"1000"}'
```

**Controller Flow:**

```
1. Predicate Detection
   oldAnnotation = ""
   newAnnotation = '{"Team":"Platform","Cost":"1000"}'
   ├─ Annotation != → Reconcile ✓

2. Fetch Pod & ENI Info
   ├─ Fetch Pod from API
   ├─ Extract Pod IP: 10.0.0.5
   └─ Get ENI for IP: eni-12345

3. Parse Tags
   currentTags      = {"Team":"Platform","Cost":"1000"}
   lastAppliedTags  = {} (first time)

4. Calculate Diff
   diff.toAdd    = {"Team":"Platform","Cost":"1000"}  (all new)
   diff.toRemove = []

5. AWS Operations
   TagENI("eni-12345", {
     "Team": "Platform",
     "Cost": "1000",
     "__eni-tagger-hash": "abc123"
   })

6. Update Pod State
   pod.Annotations["eni-tagger.io/last-applied-tags"]  = '{"Team":"Platform","Cost":"1000"}'
   pod.Annotations["eni-tagger.io/last-applied-hash"]  = "abc123"
   pod.Status.Conditions[0]                            = Synced/True

Result:
  AWS Calls:     1 × TagENI
  ENI Tags:      Team=Platform, Cost=1000, __eni-tagger-hash=abc123
  Pod Status:    True/Synced
```

---

### Scenario 3: Same Key, Different Value (Update)

```yaml
Before:
  Pod: my-pod
    eni-tagger.io/tags: '{"Team":"Platform","Cost":"1000"}'

After (User updates):
  Pod: my-pod
    eni-tagger.io/tags: '{"Team":"Dev","Cost":"1000"}'
                             ↑ Changed Platform → Dev
```

**Controller Flow:**

```
1. Predicate Detection
   oldAnnotation = '{"Team":"Platform","Cost":"1000"}'
   newAnnotation = '{"Team":"Dev","Cost":"1000"}'
   ├─ Different → Reconcile ✓

2. Parse Tags
   currentTags      = {"Team":"Dev","Cost":"1000"}
   lastAppliedTags  = {"Team":"Platform","Cost":"1000"}

3. Calculate Diff
   For each key in currentTags:
     "Team": "Dev" vs "Platform" → Values differ
       ├─ Add to toAdd
     "Cost": "1000" vs "1000" → Same value
       ├─ Skip
   
   diff.toAdd    = {"Team":"Dev"}    (value changed)
   diff.toRemove = []                 (no keys removed)

4. AWS Operations
   TagENI("eni-12345", {
     "Team": "Dev",                    ← Overwrites "Platform"
     "__eni-tagger-hash": "def456"
   })
   # No UntagENI - key "Cost" unchanged, just left as-is

5. Update Pod State
   pod.Annotations["eni-tagger.io/last-applied-tags"]  = '{"Team":"Dev","Cost":"1000"}'

Result:
  AWS Calls:     1 × TagENI (single call, not 2)
  ENI Tags:      Team=Dev (updated), Cost=1000 (unchanged)
  Efficiency:    Only changed value sent to AWS
```

**Key Insight:** AWS's `CreateTags` is idempotent. Applying `Team=Dev` to an ENI that already has `Team=Platform` simply overwrites it with a single API call.

---

### Scenario 4: New Key Added (Add Tag)

```yaml
Before:
  Pod: my-pod
    eni-tagger.io/tags: '{"Team":"Platform"}'

After (User adds):
  Pod: my-pod
    eni-tagger.io/tags: '{"Team":"Platform","Cost":"1000"}'
                                               ↑ New key
```

**Controller Flow:**

```
1. Parse Tags
   currentTags      = {"Team":"Platform","Cost":"1000"}
   lastAppliedTags  = {"Team":"Platform"}

2. Calculate Diff
   "Team": "Platform" vs "Platform" → Same, skip
   "Cost": "1000" → Not in lastAppliedTags, add
   
   diff.toAdd    = {"Cost":"1000"}    (new key)
   diff.toRemove = []

3. AWS Operations
   TagENI("eni-12345", {
     "Cost": "1000",
     "__eni-tagger-hash": "ghi789"
   })

Result:
  AWS Calls:     1 × TagENI
  ENI Tags:      Team=Platform (unchanged), Cost=1000 (new)
```

---

### Scenario 5: Key Removed (Delete Tag)

```yaml
Before:
  Pod: my-pod
    eni-tagger.io/tags: '{"Team":"Platform","Cost":"1000"}'

After (User removes):
  Pod: my-pod
    eni-tagger.io/tags: '{"Team":"Platform"}'
                         (Cost removed)
```

**Controller Flow:**

```
1. Parse Tags
   currentTags      = {"Team":"Platform"}
   lastAppliedTags  = {"Team":"Platform","Cost":"1000"}

2. Calculate Diff
   for k in lastAppliedTags:
     "Team" → Present in current, keep
     "Cost" → Not in current, remove
   
   diff.toAdd    = []
   diff.toRemove = ["Cost"]    (key no longer wanted)

3. AWS Operations
   UntagENI("eni-12345", ["Cost"])

Result:
  AWS Calls:     1 × UntagENI
  ENI Tags:      Team=Platform (unchanged), Cost (deleted)
```

---

### Scenario 6: No Change (Idempotent)

```yaml
Before:
  Pod: my-pod
    eni-tagger.io/tags: '{"Team":"Platform","Cost":"1000"}'

After (Same value, no change):
  Pod: my-pod
    eni-tagger.io/tags: '{"Team":"Platform","Cost":"1000"}'
```

**Controller Flow:**

```
1. Predicate Detection
   oldAnnotation = '{"Team":"Platform","Cost":"1000"}'
   newAnnotation = '{"Team":"Platform","Cost":"1000"}'
   
   Strings are identical → Predicate returns false
   ├─ Reconciliation skipped entirely
   ├─ Zero AWS calls
   └─ No status update

Result:
  AWS Calls:     0
  Action:        None (predicate optimization)

Alternatively, if reconciliation somehow runs:
  Parse & Compare: diff.toAdd = {}, diff.toRemove = []
  Hash check:      desiredHash == lastAppliedHash
  ├─ Early exit with "Tags already in sync"
  ├─ Zero AWS calls
  ├─ Status updated to reflect current state
  └─ Ensures safety even if predicate misses edge cases
```

**Guarantee:** Applying the same annotation 100 times is safe and costs zero AWS API calls.

---

### Scenario 7: Multiple Changes (Complex)

```yaml
Before:
  eni-tagger.io/tags: '{"Team":"Platform","Cost":"1000","Env":"Prod"}'

After (User makes complex changes):
  eni-tagger.io/tags: '{"Team":"Dev","Env":"Dev","Owner":"Alice"}'

Changes:
  - "Team": "Platform" → "Dev"     (updated)
  - "Env": "Prod" → "Dev"           (updated)
  - "Cost": removed                 (deleted)
  - "Owner": added                  (new)
```

**Controller Flow:**

```
1. Calculate Diff
   currentTags      = {"Team":"Dev","Env":"Dev","Owner":"Alice"}
   lastAppliedTags  = {"Team":"Platform","Cost":"1000","Env":"Prod"}
   
   toAdd:
     "Team": "Dev"       ← Value changed
     "Env": "Dev"        ← Value changed
     "Owner": "Alice"    ← New key
   
   toRemove:
     "Cost"              ← Not in current

   diff.toAdd    = {"Team":"Dev","Env":"Dev","Owner":"Alice"}
   diff.toRemove = ["Cost"]

2. AWS Operations (Two calls)
   # Call 1: Add/update multiple tags atomically
   TagENI("eni-12345", {
     "Team": "Dev",
     "Env": "Dev",
     "Owner": "Alice",
     "__eni-tagger-hash": "xyz999"
   })

   # Call 2: Remove old keys
   UntagENI("eni-12345", ["Cost"])

Result:
  AWS Calls:     2 (1 tag + 1 untag)
  ENI Tags:      
    Before: Team=Platform, Cost=1000, Env=Prod
    After:  Team=Dev, Env=Dev, Owner=Alice
  Efficiency:    Minimal AWS calls for maximum changes
```

---

### Scenario 8: Hash Conflict Detection

```
CONFLICT CASE: Another controller is modifying the same ENI

Before reconciliation:
  Pod last-applied-hash:  "hash-from-us-123"
  ENI __eni-tagger-hash:  "hash-from-other-456"  ← Different!
```

**Controller Flow:**

```
Decision Matrix (from checkHashConflict):

1. ENI hash empty?
   └─ Safe to claim (nobody has claimed it)

2. ENI hash == desired hash?
   └─ Already synced (we did this)

3. ENI hash == our last applied?
   └─ We own it, safe to update

4. ENI hash != our last applied?
   └─ CONFLICT! Another entity modified it

Resolution:
   If AllowSharedENITagging = false (safe mode):
     └─ Return error: "hash conflict detected"
        ├─ Reconciliation fails
        ├─ Pod status set to False/Conflict
        ├─ Kubernetes event: "HashConflict"
        └─ User must investigate

   If AllowSharedENITagging = true (risky mode):
     └─ Ignore conflict, proceed with tagging
        ├─ Risk: May cause tag thrashing
        ├─ Use only when sharing ENI across multiple controllers
        └─ Not recommended for standard EKS
```

---

## State Management

### Pod Status Annotations

The controller maintains three hidden annotations on the Pod for state tracking:

```yaml
pod.Metadata.Annotations:
  eni-tagger.io/tags: |
    # User-provided desired tags
    {"Team":"Platform","Cost":"1000"}

  eni-tagger.io/last-applied-tags: |
    # What we actually applied to AWS last time
    {"Team":"Platform","Cost":"1000"}

  eni-tagger.io/last-applied-hash: |
    # Hash of last-applied-tags (detects conflicts)
    abc123def456
```

### Why Track Last Applied State?

**Purpose:** Calculate minimal diffs and detect external modifications.

**Example:**
```
Scenario: User changes annotation, then we crash, then user changes again

Change 1: {} → {"Team":"Platform"}
  ├─ Apply to AWS
  └─ Save: last-applied = {"Team":"Platform"}

[Controller crashes]

Change 2: {"Team":"Platform"} → {"Team":"Dev"}
  ├─ On restart, we load last-applied = {"Team":"Platform"}
  ├─ We see desired = {"Team":"Dev"}
  ├─ Diff: Team changed Platform→Dev
  └─ We know exactly what to update (no redundant queries)
```

---

## Best Practices

### 1. Use ConfigMap Persistence in Production

```yaml
# Enable for resilience across restarts
controller:
  enableCacheConfigMap: true
  podNamespace: kube-system  # ConfigMap location
```

**Benefit:** Warm cache after restarts reduces AWS call surge.

---

### 2. Set Appropriate Rate Limits

```yaml
# Default: 10 QPS, 20 burst
# Tune based on cluster size

# Small cluster (< 100 Pods): Default is fine
controller:
  awsRateLimitQPS: 10
  awsRateLimitBurst: 20

# Large cluster (> 1000 Pods): Increase gradually
controller:
  awsRateLimitQPS: 20
  awsRateLimitBurst: 50
```

**Monitor:** Check metrics `aws_api_calls_total`, `aws_api_errors_total`.

---

### 3. Use Subnet Filtering for Multi-Tenant Clusters

```yaml
# Restrict tagging to specific subnets (security)
controller:
  subnetIDs:
    - subnet-app-pods
    - subnet-system-pods
  # Pods on other subnets won't be tagged (safe default)
```

---

### 4. Never Use `--allow-shared-eni-tagging` by Default

```yaml
# Standard EKS: Each Pod has dedicated ENI
allowSharedENITagging: false  # ← Safe, default

# Only use true if:
#  - Explicitly sharing ENIs across Pods
#  - Have external process also tagging ENIs
#  - Accept risk of tag thrashing
allowSharedENITagging: true   # ← Risky
```

---

### 5. Validate Annotation Format Before Deploying

```bash
# Valid JSON
kubectl annotate pod my-pod \
  eni-tagger.io/tags='{"Team":"Dev","Cost":"100"}' --overwrite

# Invalid (will be rejected)
kubectl annotate pod my-pod \
  eni-tagger.io/tags='not-json' --overwrite
# Result: Pod status False/InvalidTags
```

---

### 6. Monitor Metrics

**Key Metrics to Watch:**

```prometheus
# Cache performance
cache_hits_total          # Should be high after warmup
cache_misses_total        # Should stabilize
cache_size                # Monitor growth

# AWS API
aws_api_calls_total       # Track burst patterns
aws_api_errors_total      # Watch for rate limiting
aws_api_duration_seconds  # Detect slow calls

# Reconciliation
reconciliation_duration_seconds   # Track latency
reconciliation_errors_total       # Watch for failures
pod_tags_synced_total             # Overall success rate
```

---

## Troubleshooting

### Issue: "Tags Not Applied to ENI"

**Checklist:**

1. **Pod has annotation?**
   ```bash
   kubectl get pod -o jsonpath='{.metadata.annotations.eni-tagger\.io/tags}'
   ```
   - If empty: Apply annotation and retry.

2. **Annotation format valid?**
   ```bash
   kubectl get pod -o jsonpath='{.metadata.annotations.eni-tagger\.io/tags}' | jq .
   ```
   - If error: Fix JSON format.

3. **Pod has IP?**
   ```bash
   kubectl get pod -o jsonpath='{.status.podIP}'
   ```
   - If empty: Pod still starting, wait.

4. **ENI lookup succeeded?**
   ```bash
   kubectl describe pod my-pod
   # Check events for "ENILookupFailed"
   ```
   - If failed: Check network, AWS credentials, subnet.

5. **Hash conflict?**
   ```bash
   kubectl get pod -o jsonpath='{.status.conditions[?(@.reason=="Conflict")]}'
   ```
   - If conflict: Another controller modifying same ENI.

6. **Check controller logs:**
   ```bash
   kubectl logs -n kube-system deploy/k8s-eni-tagger -f | grep "my-pod"
   ```

---

### Issue: "AWS Rate Limiting"

**Symptoms:**
```
aws_api_errors_total increasing
reconciliation_duration_seconds > 5s
```

**Solution:**
1. Check QPS setting: `controller.awsRateLimitQPS`
2. Reduce concurrent reconciles: `controller.maxConcurrentReconciles`
3. Check if cluster has Pod churn spike
4. Monitor: `aws_api_calls_total` metric

---

### Issue: "Controller Keeps Restarting"

**Check:**
1. IAM permissions (EC2 API access)
2. Kubernetes API connectivity
3. Pod logs: `kubectl logs -n kube-system deploy/k8s-eni-tagger --previous`

---

### Issue: "ConfigMap Persistence Not Working"

**Verify:**
```bash
# ConfigMap should exist
kubectl get cm eni-tagger-cache -n kube-system

# Check contents
kubectl get cm eni-tagger-cache -n kube-system -o yaml

# Check logs for load errors
kubectl logs -n kube-system deploy/k8s-eni-tagger | grep -i configmap
```

---

## Appendix: Configuration Reference

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--enable-eni-cache` | bool | true | Enable in-memory ENI cache |
| `--enable-cache-configmap` | bool | false | Persist cache to ConfigMap |
| `--cache-batch-interval` | duration | 2s | How often to flush ConfigMap updates |
| `--cache-batch-size` | int | 20 | Cache entries per ConfigMap write |
| `--aws-rate-limit-qps` | float | 10 | AWS API queries per second |
| `--aws-rate-limit-burst` | int | 20 | AWS API burst requests |
| `--max-concurrent-reconciles` | int | 1 | Parallel Pod reconciliations |
| `--pod-rate-limit-qps` | float | 0 | Per-Pod reconciliation QPS (0=disabled) |
| `--pod-rate-limit-burst` | int | 0 | Per-Pod reconciliation burst |
| `--allow-shared-eni-tagging` | bool | false | Allow tagging shared ENIs (risky) |
| `--subnet-ids` | []string | empty | Allowed subnets for tagging (filter) |
| `--dry-run` | bool | false | Simulate without AWS changes |
| `--annotation-key` | string | eni-tagger.io/tags | Pod annotation key |

---

## Summary

The k8s-eni-tagger controller provides a robust, production-ready solution for automatically tagging AWS ENIs based on Kubernetes Pod annotations. Its design prioritizes:

- **Efficiency:** Minimal AWS API calls via caching and diff calculation
- **Safety:** Hash-based conflict detection and idempotent operations
- **Resilience:** ConfigMap persistence and graceful restart recovery
- **Observability:** Detailed status annotations and Kubernetes events

Understanding these scenarios and design principles enables confident deployment and troubleshooting in production environments.
