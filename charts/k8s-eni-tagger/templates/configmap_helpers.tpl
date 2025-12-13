{{/*
Helpers specifically for generated ConfigMap and env vars. Kept separate to avoid collisions
with chart's standard _helpers.tpl.
*/}}
{{- define "k8s-eni-tagger.envConfigMapName" -}}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- printf "%s-%s-config" .Release.Name $name }}
{{- end }}

{{- define "k8s-eni-tagger.envBuildConfigMapData" -}}
{{- $c := .Values.config }}
{{- $e := .Values.env }}
ENI_TAGGER_ANNOTATION_KEY: {{ $c.annotationKey | quote }}
ENI_TAGGER_WATCH_NAMESPACE: {{ $c.watchNamespace | quote }}
ENI_TAGGER_MAX_CONCURRENT_RECONCILES: {{ $c.maxConcurrentReconciles | quote }}
ENI_TAGGER_DRY_RUN: {{ $c.dryRun | quote }}
ENI_TAGGER_METRICS_BIND_ADDRESS: {{ $c.metricsBindAddress | quote }}
ENI_TAGGER_HEALTH_PROBE_BIND_ADDRESS: {{ $c.healthProbeBindAddress | quote }}
ENI_TAGGER_SUBNET_IDS: {{ $c.subnetIDs | quote }}
ENI_TAGGER_ALLOW_SHARED_ENI_TAGGING: {{ $c.allowSharedENITagging | quote }}
ENI_TAGGER_ENABLE_ENI_CACHE: {{ $c.enableENICache | quote }}
ENI_TAGGER_ENABLE_CACHE_CONFIGMAP: {{ $c.enableCacheConfigMap | quote }}
ENI_TAGGER_CACHE_BATCH_INTERVAL: {{ $c.cacheBatchInterval | quote }}
ENI_TAGGER_CACHE_BATCH_SIZE: {{ $c.cacheBatchSize | quote }}
ENI_TAGGER_AWS_RATE_LIMIT_QPS: {{ $c.awsRateLimitQPS | quote }}
ENI_TAGGER_AWS_RATE_LIMIT_BURST: {{ $c.awsRateLimitBurst | quote }}
ENI_TAGGER_PPROF_BIND_ADDRESS: {{ $c.pprofBindAddress | quote }}
ENI_TAGGER_TAG_NAMESPACE: {{ $c.tagNamespace | quote }}
ENI_TAGGER_POD_RATE_LIMIT_QPS: {{ $c.podRateLimitQPS | quote }}
ENI_TAGGER_POD_RATE_LIMIT_BURST: {{ $c.podRateLimitBurst | quote }}
ENI_TAGGER_RATE_LIMITER_CLEANUP_INTERVAL: {{ $c.rateLimiterCleanupInterval | quote }}
{{- if $e }}
{{- range $key, $value := $e }}
{{ $key }}: {{ $value | quote }}
{{- end }}
{{- end }}
{{- end }}
