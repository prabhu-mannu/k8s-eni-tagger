# Research Findings: Annotation Format & Health Probes

## 1. Annotation Format: JSON vs Comma-Separated

### Current State
- **Implementation**: Requires JSON format `{"CostCenter":"1234","Team":"Platform"}`
- **Previous docs**: Incorrectly showed comma-separated `CostCenter=1234,Team=Platform`

### UX Comparison

| Aspect | Comma-Separated | JSON | Recommendation |
|--------|----------------|------|----------------|
| **Readability** | ✅ Simple `key=value` | ❌ Verbose with quotes | Support both |
| **YAML escaping** | ✅ `"key=val"` | ❌ Needs `'{"k":"v"}'` | Support both |
| **kubectl UX** | ✅ Less error-prone | ❌ Quote hell | Support both |
| **Standardization** | ❌ Custom parser | ✅ Built-in JSON | Keep JSON primary |
| **Complex values** | ❌ Can't handle `=` or `,` | ✅ Any string | JSON wins |
| **K8s patterns** | Used by: Prometheus | Used by: Istio, cert-manager | JSON standard |

### Real-World Examples

**Comma-separated (simple annotations):**
```yaml
prometheus.io/scrape: "true"
prometheus.io/port: "8080"
```

**JSON (structured data):**
```yaml
# Istio sidecar injection
sidecar.istio.io/inject: '{"containers":["app"]}'

# cert-manager
cert-manager.io/issuer: '{"kind":"Issuer","name":"letsencrypt"}'
```

### Recommendation: **Support Both Formats**

#### Benefits:
1. **Better UX** for simple cases: `CostCenter=1234,Team=Platform`
2. **Backwards compatible** with JSON for complex cases
3. **Kubernetes-native** approach (many controllers do this)

#### Implementation:
```go
func parseTags(tagStr string) (map[string]string, error) {
    tagStr = strings.TrimSpace(tagStr)
    if tagStr == "" {
        return make(map[string]string), nil
    }

    var tags map[string]string
    
    // Try JSON first
    if err := json.Unmarshal([]byte(tagStr), &tags); err == nil {
        return validateAndReturnTags(tags)
    }

    // Fallback to comma-separated format
    tags = parseCommaSeparated(tagStr)
    return validateAndReturnTags(tags)
}
```

---

## 2. Health Probe AWS API Bombardment

### Current Implementation

**In `main.go`:**
```go
// Readiness probe - CALLS AWS API!
awsChecker := health.NewAWSChecker(ec2HealthClient)
mgr.AddReadyzCheck("aws-connectivity", awsChecker.Check)

// Liveness probe - Just a ping
mgr.AddHealthzCheck("healthz", healthz.Ping)
```

**In Helm chart:**
```yaml
livenessProbe:
  httpGet:
    path: /healthz  # healthz.Ping - no AWS call
  periodSeconds: 20  # 3 requests/min

readinessProbe:
  httpGet:
    path: /readyz  # Calls AWS DescribeAccountAttributes!
  periodSeconds: 10  # 6 requests/min
```

### Problem: Excessive AWS API Calls

**For N replicas:**
- Readiness checks: `N × 6 calls/min = N × 360 calls/hour`
- Example with 3 replicas: **1,080 API calls/hour** or **25,920/day**

**Issues:**
1. ❌ **Wastes rate limit quota** (configured at 10 QPS = 600/min for actual work)
2. ❌ **Adds latency** to probe (AWS roundtrip every 10s)
3. ❌ **False negatives** if AWS has transient issues
4. ❌ **Wrong semantics**: Controller readiness ≠ AWS availability

### Probe Philosophy

| Probe | Purpose | Should Check |
|-------|---------|--------------|
| **Liveness** | "Am I deadlocked?" | Process is responsive |
| **Readiness** | "Can I serve traffic?" | Controller is leader-elected and watching |
| **Startup** | "Have I initialized?" | AWS connectivity, permissions (one-time) |

### Recommended Fix

#### 1. Move AWS check to Startup Probe (one-time validation)
```yaml
startupProbe:
  httpGet:
    path: /healthz/aws  # New endpoint for AWS validation
  failureThreshold: 30
  periodSeconds: 10
  # Gives 5 minutes to establish AWS connectivity
  # Only runs once at startup
```

#### 2. Keep Readiness simple (no AWS calls)
```yaml
readinessProbe:
  httpGet:
    path: /readyz  # Just checks if manager is ready
  periodSeconds: 10
```

#### 3. Keep Liveness simple
```yaml
livenessProbe:
  httpGet:
    path: /healthz
  periodSeconds: 20
```

### Implementation Changes

**In `main.go`:**
```go
// Startup-time AWS validation only
if err := mgr.AddHealthzCheck("aws", awsChecker.Check); err != nil {
    setupLog.Error(err, "unable to add AWS health check")
    os.Exit(1)
}

// Readiness: no AWS calls, just manager readiness
if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
    setupLog.Error(err, "unable to set up ready check")
    os.Exit(1)
}
```

**In Helm chart:**
```yaml
startupProbe:
  httpGet:
    path: /healthz  # Validates AWS on startup
    port: health
  failureThreshold: 30
  periodSeconds: 10
  
readinessProbe:
  httpGet:
    path: /readyz  # No AWS calls
    port: health
  periodSeconds: 10
```

### Impact

**Before:**
- 3 replicas × 6 readiness checks/min = **1,080 AWS API calls/hour**
- Rate limit impact: ~2.5% of 10 QPS quota wasted on probes

**After:**
- 3 replicas × 1 startup check = **3 AWS API calls total** (at startup)
- Rate limit impact: ~0% ongoing waste
- **360x reduction** in AWS API calls

### References

- [Kubernetes Probe Best Practices](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/)
- [Controller-Runtime Health Checks](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/healthz)
- [AWS EC2 API Rate Limits](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/throttling.html)
