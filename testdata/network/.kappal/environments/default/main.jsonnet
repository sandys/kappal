local kappal = import '../lib/kappal.libsonnet';
local spec = import '../manifests/spec.json';

kappal.project(spec)
