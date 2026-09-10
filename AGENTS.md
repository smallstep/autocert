# AGENTS.md

Guidance for AI coding agents working in this repository. Claude Code loads it through the one-line `@AGENTS.md` import in `CLAUDE.md`.

## Overview

Autocert is a Kubernetes mutating admission webhook that injects TLS certificates into
annotated pods. It adds an init container (bootstrapper) that exchanges a one-time token
for a certificate from a `step-ca` instance, and a sidecar (renewer) that keeps the
certificate renewed, both writing to a shared `emptyDir` volume at
`/var/run/autocert.step.sm`. Module: `github.com/smallstep/autocert`. Depends on
`github.com/smallstep/certificates` (CA client, `ca.Provisioner`, `ca.BootstrapServer`),
`github.com/smallstep/cli-utils`, `go.step.sm/crypto`, and `k8s.io/api` /
`k8s.io/apimachinery`. The only Go program is `controller/`; the other three images are
shell wrappers around the `step` CLI.

## Commands

```bash
make build      # CGO_ENABLED=0 go build ./controller -> bin/autocert
make test       # gotestsum -- -short -coverprofile=coverage.out ./...
make lint       # golangci-lint (config fetched from smallstep/workflows) + govulncheck
make fmt        # goimports -l -w on all .go files
make bootstrap  # installs golangci-lint, govulncheck, gotestsum
make all        # build lint test
```

```bash
# Run a single test (all tests live in controller/)
go test -run TestShouldMutate ./controller/

# Build the four container images (multi-stage; only docker is required)
docker build -t smallstep/autocert-controller:latest -f controller/Dockerfile .
docker build -t smallstep/autocert-bootstrapper:latest -f bootstrapper/Dockerfile .
docker build -t smallstep/autocert-renewer:latest -f renewer/Dockerfile .
docker build -t smallstep/autocert-init:latest -f init/Dockerfile .
make docker-registry   # local registry on :5000 for docker-dev
```

`go build ./...` and `make test` run in well under a minute with no external
dependencies (no Docker, no cluster, no env vars). `make generate` exists but there are
no `go:generate` directives, so it is a no-op. There is no way to run the controller
outside a cluster: `main` requires an in-cluster service account
(`/var/run/secrets/kubernetes.io/serviceaccount/`) and a reachable CA.

CI (`.github/workflows/ci.yml`) calls the shared `smallstep/workflows` `goCI.yml`
(golangci-lint, govulncheck, CodeQL, test, build; latest Go only). Tag pushes matching `v*` run `release.yml`, which
runs goreleaser for the `autocert` binary and builds/pushes all four images for
`linux/amd64,linux/arm64`.

## Architecture

```
autocert/
├── controller/            # The admission webhook (Go, package main)
│   ├── main.go            # Config, JSON-patch construction, /mutate + /healthz handler
│   ├── client.go          # Minimal in-cluster Kubernetes REST client (secrets create/delete)
│   ├── main_test.go       # Unit tests (stdlib testing, table-driven, reflect.DeepEqual)
│   └── Dockerfile         # Copies ONLY go.mod, go.sum, client.go, main.go -- add new files here
├── bootstrapper/          # Init container image: bootstrapper.sh runs `step ca certificate`
├── renewer/               # Sidecar image: `step ca renew --daemon`
├── init/                  # One-shot installer image: autocert.sh (step ca init + kubectl apply)
├── install/               # Manifests: 01-step-ca.yaml, 02-autocert.yaml, 03-rbac.yaml
├── examples/hello-mtls/   # mTLS client/server examples (go, go-grpc, node, py, nginx, envoy, curl)
├── Makefile
├── .goreleaser.yml        # Builds ./controller as `autocert`, linux_amd64 only
├── INSTALL.md             # Manual install walkthrough
└── RUNBOOK.md             # Admin tasks (recover passwords, label namespaces, uninstall)
```

### Request flow

1. Kubernetes calls `POST /mutate` on the controller (`:4443`, TLS served via
   `ca.BootstrapServer` using a cert the controller obtains from the CA at startup) for
   pod CREATE in namespaces labeled `autocert.step.sm=enabled`.
2. `mutate` decodes a `v1beta1.AdmissionReview`; `shouldMutate` requires the
   `autocert.step.sm/name` annotation, skips pods already marked
   `autocert.step.sm/status=injected`, and (when `restrictCertificatesToNamespace` is
   set) rejects names that resolve to another namespace.
3. `patch` builds an RFC 6902 JSON patch: `mkBootstrapper` mints a bootstrap token with
   `provisioner.Token(commonName, sans...)`, stores it in a generated Secret labeled
   `autocert.step.sm/token=true` (via `createTokenSecret`), and prepends the init
   container; `mkRenewer` appends the sidecar unless `autocert.step.sm/bootstrapper-only`
   is set; the certs volume and mount are added to every container.
4. The bootstrapper container receives the token as `STEP_TOKEN` (a `secretKeyRef` into
   that Secret), runs `step ca certificate` and `step ca root`, and exits; the renewer
   runs for the pod's lifetime. The controller deletes the Secret best-effort from a
   goroutine after `tokenLifetime` (5 minutes); stragglers are labeled so they can be
   bulk-deleted with `kubectl` (see RUNBOOK.md).

Pod annotations understood by the controller (all under `autocert.step.sm/`): `name`,
`sans`, `duration`, `init-first`, `bootstrapper-only`, `owner`, `mode`, `status`.

## Conventions

- **Config**: single YAML file passed as the only CLI argument (`./server <config>`),
  unmarshalled into `Config` with `sigs.k8s.io/yaml`. Fields: `address`, `service`,
  `logFormat`, `caUrl`, `certLifetime`, `bootstrapper`/`renewer` (full `corev1.Container`
  specs), `certsVolume`, `restrictCertificatesToNamespace`, `clusterDomain`,
  `rootCAPath`, `provisionerPasswordPath`. Defaults live in the `Get*` methods. The
  canonical config is the ConfigMap in `install/02-autocert.yaml`.
- **Logging**: logrus (`log "github.com/sirupsen/logrus"`), JSON or text per `logFormat`.
- **Errors**: `github.com/pkg/errors` (`errors.Wrap`) and `cli-utils/errs` helpers; no
  `fmt.Errorf("%w")`.
- **No CLI framework**: `main` reads `os.Args[1]` directly; there are no flags.
- **Kubernetes client**: hand-rolled REST client in `client.go`, not `client-go`. Only
  the Secrets API is used (create/delete), which is all `install/03-rbac.yaml` grants.
- **Testing**: stdlib `testing` only, no testify or mocks. Tests cover pure functions
  (`shouldMutate`, `mkRenewer`, `Config` getters); anything touching the CA or the
  Kubernetes API is untested.
- **Images**: `bootstrapper`, `renewer`, and `init` Dockerfiles are `FROM
  smallstep/step-cli:<pinned>` and contain no Go; behavior changes there mean editing the
  shell scripts or the `step` invocation.

## Environment variables (controller)

| Variable | Purpose |
|----------|---------|
| `PROVISIONER_NAME` | Name of the JWK provisioner used to mint tokens (set to `autocert` by the install manifest) |
| `PROVISIONER_KID` | Optional key ID; disambiguates provisioners with the same name |
| `NAMESPACE` | Namespace the controller runs in (required; from `metadata.namespace` downward API) |
| `STEPPATH` | Step config root; the image sets `/home/step`, with CA config/certs mounted from ConfigMaps |
| `KUBERNETES_SERVICE_HOST` / `KUBERNETES_SERVICE_PORT` | Injected by Kubernetes; used by `NewInClusterK8sClient` |

The provisioner password is read from `provisionerPasswordPath` (default
`/home/step/password/password`, mounted from the `autocert-password` Secret), not from
an environment variable.
