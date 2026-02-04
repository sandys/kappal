// kappal.libsonnet - Convert Docker Compose spec to Kubernetes manifests
{
  // Helper to get with default
  get(obj, key, default=null)::
    if std.objectHas(obj, key) then obj[key] else default,

  // Convert environment array to K8s env format
  envToK8s(env)::
    if env == null then []
    else [{ name: e.name, value: std.toString(e.value) } for e in env],

  // Convert ports array to container ports
  portsToContainerPorts(ports)::
    if ports == null then []
    else [{
      containerPort: p.target,
      protocol: std.asciiUpper($.get(p, 'protocol', 'tcp')),
    } for p in ports],

  // Convert ports to service ports
  portsToServicePorts(ports)::
    if ports == null then []
    else [{
      name: 'port-' + std.toString(i),
      port: if $.get(ports[i], 'published', 0) > 0 then ports[i].published else ports[i].target,
      targetPort: ports[i].target,
      protocol: std.asciiUpper($.get(ports[i], 'protocol', 'tcp')),
    } for i in std.range(0, std.length(ports) - 1)],

  // Generate volume mounts for a service
  volumeMounts(svc)::
    local vols = $.get(svc, 'volumes', []);
    local secrets = $.get(svc, 'secrets', []);
    local configs = $.get(svc, 'configs', []);

    // Regular volume mounts
    [
      {
        name: 'vol-' + std.toString(i),
        mountPath: vols[i].target,
        [if $.get(vols[i], 'read_only', false) then 'readOnly']: true,
      }
      for i in std.range(0, std.length(vols) - 1)
    ] +
    // Secret mounts
    [
      {
        name: 'secret-' + s.source,
        mountPath: '/run/secrets/' + $.get(s, 'target', s.source),
        subPath: s.source,
        readOnly: true,
      }
      for s in secrets
    ] +
    // Config mounts
    [
      {
        name: 'config-' + c.source,
        mountPath: $.get(c, 'target', '/' + c.source),
        subPath: c.source,
        readOnly: true,
      }
      for c in configs
    ],

  // Generate volumes for a service
  volumes(svc)::
    local vols = $.get(svc, 'volumes', []);
    local secrets = $.get(svc, 'secrets', []);
    local configs = $.get(svc, 'configs', []);

    // Regular volumes
    [
      {
        name: 'vol-' + std.toString(i),
        [if $.get(vols[i], 'type', 'volume') == 'bind' then 'hostPath']: {
          path: vols[i].source,
        },
        [if $.get(vols[i], 'type', 'volume') != 'bind' then 'persistentVolumeClaim']: {
          claimName: vols[i].source,
        },
      }
      for i in std.range(0, std.length(vols) - 1)
    ] +
    // Secret volumes
    [
      {
        name: 'secret-' + s.source,
        secret: {
          secretName: s.source,
        },
      }
      for s in secrets
    ] +
    // Config volumes
    [
      {
        name: 'config-' + c.source,
        configMap: {
          name: c.source,
        },
      }
      for c in configs
    ],

  // Generate Deployment for a service
  deployment(projectName, serviceName, svc):: {
    apiVersion: 'apps/v1',
    kind: 'Deployment',
    metadata: {
      name: serviceName,
      namespace: projectName,
      labels: {
        'kappal.io/project': projectName,
        'kappal.io/service': serviceName,
      },
    },
    spec: {
      replicas: $.get(svc, 'replicas', 1),
      selector: {
        matchLabels: {
          'kappal.io/project': projectName,
          'kappal.io/service': serviceName,
        },
      },
      template: {
        metadata: {
          labels: {
            'kappal.io/project': projectName,
            'kappal.io/service': serviceName,
          } + (
            if std.length($.get(svc, 'networks', [])) > 0
            then { 'kappal.io/network': svc.networks[0] }
            else {}
          ),
        },
        spec: {
          containers: [{
            name: serviceName,
            image: svc.image,
            imagePullPolicy: 'IfNotPresent',
            [if std.length($.get(svc, 'ports', [])) > 0 then 'ports']: $.portsToContainerPorts(svc.ports),
            [if std.length($.get(svc, 'environment', [])) > 0 then 'env']: $.envToK8s(svc.environment),
            [if std.length($.get(svc, 'command', [])) > 0 then 'command']: svc.command,
            [if std.length($.get(svc, 'entrypoint', [])) > 0 then 'command']: svc.entrypoint,
            [if std.length($.volumeMounts(svc)) > 0 then 'volumeMounts']: $.volumeMounts(svc),
          }],
          [if std.length($.volumes(svc)) > 0 then 'volumes']: $.volumes(svc),
        },
      },
    },
  },

  // Generate Service for a compose service (only if ports are exposed)
  service(projectName, serviceName, svc)::
    if std.length($.get(svc, 'ports', [])) == 0 then {}
    else {
      apiVersion: 'v1',
      kind: 'Service',
      metadata: {
        name: serviceName,
        namespace: projectName,
        labels: {
          'kappal.io/project': projectName,
          'kappal.io/service': serviceName,
        },
      },
      spec: {
        type: 'LoadBalancer',
        externalTrafficPolicy: 'Local',
        selector: {
          'kappal.io/project': projectName,
          'kappal.io/service': serviceName,
        },
        ports: $.portsToServicePorts(svc.ports),
      },
    },

  // Generate PVC for a named volume
  pvc(projectName, volumeName, volumeSpec):: {
    apiVersion: 'v1',
    kind: 'PersistentVolumeClaim',
    metadata: {
      name: volumeName,
      namespace: projectName,
      labels: {
        'kappal.io/project': projectName,
      },
    },
    spec: {
      accessModes: ['ReadWriteOnce'],
      resources: {
        requests: {
          storage: '1Gi',
        },
      },
      storageClassName: 'local-path',
    },
  },

  // Generate Secret
  secret(projectName, secretName, secretSpec):: {
    apiVersion: 'v1',
    kind: 'Secret',
    metadata: {
      name: secretName,
      namespace: projectName,
      labels: {
        'kappal.io/project': projectName,
      },
    },
    type: 'Opaque',
    // Note: actual data must be populated separately
    data: {},
  },

  // Generate ConfigMap
  configMap(projectName, configName, configSpec):: {
    apiVersion: 'v1',
    kind: 'ConfigMap',
    metadata: {
      name: configName,
      namespace: projectName,
      labels: {
        'kappal.io/project': projectName,
      },
    },
    // Note: actual data must be populated separately
    data: {},
  },

  // Generate NetworkPolicy for network isolation
  networkPolicy(projectName, networkName):: {
    apiVersion: 'networking.k8s.io/v1',
    kind: 'NetworkPolicy',
    metadata: {
      name: networkName,
      namespace: projectName,
      labels: {
        'kappal.io/project': projectName,
      },
    },
    spec: {
      podSelector: {
        matchLabels: {
          'kappal.io/network': networkName,
        },
      },
      policyTypes: ['Ingress'],
      ingress: [{
        from: [{
          podSelector: {
            matchLabels: {
              'kappal.io/network': networkName,
            },
          },
        }],
      }],
    },
  },

  // Generate Namespace
  namespace(projectName):: {
    apiVersion: 'v1',
    kind: 'Namespace',
    metadata: {
      name: projectName,
      labels: {
        'kappal.io/project': projectName,
      },
    },
  },

  // Convert entire compose project to K8s manifests
  project(spec)::
    local projectName = spec.name;
    local services = $.get(spec, 'services', {});
    local volumes = $.get(spec, 'volumes', {});
    local secrets = $.get(spec, 'secrets', {});
    local configs = $.get(spec, 'configs', {});
    local networks = $.get(spec, 'networks', {});

    // Namespace
    [$.namespace(projectName)] +

    // Secrets
    [$.secret(projectName, name, secrets[name]) for name in std.objectFields(secrets)] +

    // ConfigMaps
    [$.configMap(projectName, name, configs[name]) for name in std.objectFields(configs)] +

    // PVCs
    [$.pvc(projectName, name, volumes[name]) for name in std.objectFields(volumes)] +

    // NetworkPolicies (skip 'default' network)
    [
      $.networkPolicy(projectName, name)
      for name in std.objectFields(networks)
      if name != 'default'
    ] +

    // Deployments
    [$.deployment(projectName, name, services[name]) for name in std.objectFields(services)] +

    // Services (only for services with ports)
    [
      $.service(projectName, name, services[name])
      for name in std.objectFields(services)
      if std.length($.get(services[name], 'ports', [])) > 0
    ],
}
