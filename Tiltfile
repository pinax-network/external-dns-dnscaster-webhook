update_settings(
    max_parallel_updates = 5,
    k8s_upsert_timeout_secs = 60,
    suppress_unused_image_warnings= None,
)

allow_k8s_contexts('colima')
allow_k8s_contexts('local')

load('ext://restart_process', 'docker_build_with_restart')
load('ext://helm_resource', 'helm_resource', 'helm_repo')

k8s_yaml(local('wget -qO- https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.5.1/experimental-install.yaml'))

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
        '--version=1.19.1',
    ],
    resource_deps=['cilium-repo']
)

local_resource(
    'cilium-manifests',
    cmd='kubectl apply -f ./hack/cilium/manifests.yaml',
    resource_deps=['cilium-install'],
    deps=['./hack/cilium/manifests.yaml']
)

###############################################
# Using local build of external-dns-dnscaster #
###############################################
IMG = 'localhost:5001/external-dns-dnscaster'

def binary():
    return 'CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=dev -X main.commit=dev" ./cmd/webhook/main.go'

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

helm_repo(
    name="external-dns-repo",
    url="https://kubernetes-sigs.github.io/external-dns/",
)

helm_resource(
    name="external-dns-install",
    chart="external-dns-repo/external-dns",
    namespace="default",
    flags=[
        '--values=./hack/external-dns/values.yaml',
        '--version=1.20.0',
    ],
    image_deps=[IMG],
    image_keys=[('provider.webhook.image.repository', 'provider.webhook.image.tag')],
)
