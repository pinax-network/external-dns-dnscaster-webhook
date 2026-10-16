update_settings(
    max_parallel_updates = 5,
    k8s_upsert_timeout_secs = 60,
    suppress_unused_image_warnings= None,
)

allow_k8s_contexts('colima')
allow_k8s_contexts('local')

load('ext://restart_process', 'docker_build_with_restart')
load('ext://helm_resource', 'helm_resource', 'helm_repo')

# Gateway API CRDs must be established before cilium creates its GatewayClass.
# Registry CRD for `registry: crd`. Helm only installs crds/ on first install, so
# apply it here to keep `helm upgrade` on an existing release working.
local_resource(
    'crds',
    cmd=' && '.join([
        'kubectl apply --server-side' +
        ' -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.5.1/experimental-install.yaml' +
        ' -f https://raw.githubusercontent.com/kubernetes-sigs/external-dns/v0.23.0/config/crd/standard/dnsrecords.externaldns.k8s.io.yaml',
        'kubectl wait --for=condition=Established crd --all',
    ]),
)

### Deploying cilium
helm_repo(
    name="cilium-repo",
    url="https://helm.cilium.io",
)
helm_resource(
    name="cilium-install",
    chart="cilium-repo/cilium",
    namespace="kube-system",
    flags=[
        '--values=./hack/cilium/values.yaml',
        '--version=1.20.2',
    ],
    resource_deps=['cilium-repo', 'crds']
)

# Tilt marks cilium-install ready before the agent is. The agent only turns ready
# once the operator has registered the CRDs these manifests use.
local_resource(
    'cilium-manifests',
    cmd='kubectl -n kube-system rollout status daemonset/cilium --timeout=5m && kubectl apply -f ./hack/cilium/manifests.yaml',
    resource_deps=['cilium-install'],
    deps=['./hack/cilium/manifests.yaml']
)

### Deploying monitoring
helm_repo(
    name="monitoring",
    url="https://prometheus-community.github.io/helm-charts",
)
helm_resource(
    name="monitoring-install",
    chart="monitoring/kube-prometheus-stack",
    namespace="kube-system",
    # The chart's admission webhook hook jobs block the install until pods can run,
    # and cilium-manifests waits for the cilium agent.
    resource_deps=['monitoring', 'cilium-manifests'],
    flags=[
        '--values=./hack/monitoring/values.yaml',
    ],
)

local_resource(
    "dashboard-apply",
    "kubectl apply -k ./hack/monitoring",
    resource_deps=["monitoring-install"],
)

###############################################
# Using local build of external-dns-dnscaster #
###############################################
IMG = 'localhost:5001/external-dns-dnscaster'

def binary():
    return 'CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w -X main.version=dev -X main.commit=dev" ./cmd/webhook/main.go'

local_resource(
    'recompile', 
    cmd=binary(),
    deps=['./cmd', './pkg', './internal'],
)

docker_build_with_restart(
    IMG,
    '.',
    dockerfile='tilt.Dockerfile',
    entrypoint=['/main'], 
    live_update=[
        sync('./main', '/main'),
        ],
)

k8s_yaml(local('sops -d ./hack/external-dns/dnscaster-api-key.yaml'))
k8s_resource(new_name='dnscaster-api-key', objects=['dnscaster-api-key:secret'])

helm_repo(
    name="external-dns-repo",
    url="https://kubernetes-sigs.github.io/external-dns/",
)

helm_resource(
    name="external-dns-install",
    chart="external-dns-repo/external-dns",
    namespace="kube-system",
    flags=[
        '--values=./hack/external-dns/values.yaml',
        '--version=1.23.0',
    ],
    image_deps=[IMG],
    image_keys=[('provider.webhook.image.repository', 'provider.webhook.image.tag')],
    # serviceMonitor.enabled renders a ServiceMonitor, so the Prometheus Operator
    # CRDs must be installed before this chart is rendered. The image copies the
    # binary built by `recompile`.
    resource_deps=['external-dns-repo', 'monitoring-install', 'crds', 'dnscaster-api-key', 'recompile'],
)

### Deploying dnscaster-exporter
k8s_yaml(kustomize('./hack/dnscaster-exporter'))
k8s_resource(
    'dnscaster-exporter',
    objects=['dnscaster-exporter:servicemonitor'],
    resource_deps=['monitoring-install', 'dnscaster-api-key'],
)

### Deploying examples
k8s_yaml(listdir('./example', recursive=True))
k8s_resource(
    'nginx-deployment',
    new_name='examples',
    objects=['gateway:gateway', 'gateway-api:httproute', 'ingress:ingress', 'example-a-record:dnsendpoint'],
    # LoadBalancer IPs come from the cilium pool, and the DNSEndpoint CRD from the
    # external-dns chart.
    resource_deps=['cilium-manifests', 'external-dns-install'],
)
