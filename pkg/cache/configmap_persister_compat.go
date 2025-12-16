package cache

// This file is kept for backward compatibility.
// The actual implementation is in configmap_persister_sharded.go

// configMapPersister is an alias for configMapPersisterSharded
type configMapPersister = configMapPersisterSharded

// keep a reference to the alias to avoid "type is unused" tooling diagnostics
// The blank identifier assignment ensures the alias remains in the compiled package
var _ configMapPersister = configMapPersisterSharded{}
