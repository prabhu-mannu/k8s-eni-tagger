{{/* Common helper templates for k8s-eni-tagger chart */}}

{{- define "k8s-eni-tagger.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "k8s-eni-tagger.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "k8s-eni-tagger.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "k8s-eni-tagger.selectorLabels" -}}
app.kubernetes.io/name: {{ include "k8s-eni-tagger.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "k8s-eni-tagger.labels" -}}
helm.sh/chart: {{ include "k8s-eni-tagger.chart" . }}
{{ include "k8s-eni-tagger.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "k8s-eni-tagger.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "k8s-eni-tagger.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "k8s-eni-tagger.leaderElectionEnabled" -}}
{{- if or (gt (int .Values.replicaCount) 1) (default false .Values.config.enableLeaderElection) }}
true
{{- else }}
false
{{- end }}
{{- end }}

{{/* ConfigMap name for env injection */}}
{{- define "k8s-eni-tagger.envConfigMapName" -}}
{{- printf "%s-config" (include "k8s-eni-tagger.fullname" .) }}
{{- end }}

{{/* Build ConfigMap data from config values and extra env map. */}}
{{- define "k8s-eni-tagger.envBuildConfigMapData" -}}
{{- $root := . -}}
{{- $c := .Values.config -}}
{{- $leader := or (gt (int .Values.replicaCount) 1) (default false $c.enableLeaderElection) -}}
{{- $data := dict }}
{{- /* Required / always-present settings */}}
{{- $_ := set $data "ENI_TAGGER_ANNOTATION_KEY" $c.annotationKey }}
{{- $_ := set $data "ENI_TAGGER_MAX_CONCURRENT_RECONCILES" $c.maxConcurrentReconciles }}
{{- $_ := set $data "ENI_TAGGER_DRY_RUN" $c.dryRun }}
{{- $_ := set $data "ENI_TAGGER_METRICS_BIND_ADDRESS" $c.metricsBindAddress }}
{{- $_ := set $data "ENI_TAGGER_HEALTH_PROBE_BIND_ADDRESS" $c.healthProbeBindAddress }}
{{- $_ := set $data "ENI_TAGGER_ALLOW_SHARED_ENI_TAGGING" $c.allowSharedENITagging }}
{{- $_ := set $data "ENI_TAGGER_ENABLE_ENI_CACHE" $c.enableENICache }}
{{- $_ := set $data "ENI_TAGGER_ENABLE_CACHE_CONFIGMAP" $c.enableCacheConfigMap }}
{{- $_ := set $data "ENI_TAGGER_CACHE_BATCH_INTERVAL" $c.cacheBatchInterval }}
{{- $_ := set $data "ENI_TAGGER_CACHE_BATCH_SIZE" $c.cacheBatchSize }}
{{- $_ := set $data "ENI_TAGGER_AWS_RATE_LIMIT_QPS" $c.awsRateLimitQPS }}
{{- $_ := set $data "ENI_TAGGER_AWS_RATE_LIMIT_BURST" $c.awsRateLimitBurst }}
{{- $_ := set $data "ENI_TAGGER_PPROF_BIND_ADDRESS" $c.pprofBindAddress }}
{{- $_ := set $data "ENI_TAGGER_POD_RATE_LIMIT_QPS" $c.podRateLimitQPS }}
{{- $_ := set $data "ENI_TAGGER_POD_RATE_LIMIT_BURST" $c.podRateLimitBurst }}
{{- $_ := set $data "ENI_TAGGER_RATE_LIMITER_CLEANUP_INTERVAL" $c.rateLimiterCleanupInterval }}
{{- /* AWS health latch max successes: supports 0 to disable latching */}}
{{- $awsHealthMax := 3 -}}
{{- if kindIs "invalid" $c.awsHealthMaxSuccesses }}
  {{- /* Value is not provided, use default of 3 */}}
{{- else }}
  {{- $awsHealthMax = $c.awsHealthMaxSuccesses -}}
{{- end }}
{{- if lt (int $awsHealthMax) 0 }}
  {{- $awsHealthMax = 0 -}}
{{- end }}
{{- $_ := set $data "ENI_TAGGER_AWS_HEALTH_MAX_SUCCESSES" $awsHealthMax }}

{{- /* Derived leader election: only emit env when it would be true */}}
{{- if $leader }}
{{- $_ := set $data "ENI_TAGGER_LEADER_ELECT" true }}
{{- end }}

{{- /* Optional strings: include only when non-empty for cleaner UX */}}
{{- if $c.subnetIDs }}
{{- $_ := set $data "ENI_TAGGER_SUBNET_IDS" $c.subnetIDs }}
{{- end }}
{{- if $c.tagNamespace }}
{{- $_ := set $data "ENI_TAGGER_TAG_NAMESPACE" $c.tagNamespace }}
{{- end }}
{{- if $c.watchNamespace }}
{{- $_ := set $data "ENI_TAGGER_WATCH_NAMESPACE" $c.watchNamespace }}
{{- end }}

{{- /* Merge user-provided extra env */}}
{{- if .Values.env }}
{{- $data = merge $data .Values.env }}
{{- end }}

{{- range $key := keys $data | sortAlpha }}
{{ $key }}: {{ tpl (printf "%v" (index $data $key)) $root | quote }}
{{- end }}
{{- end }}
