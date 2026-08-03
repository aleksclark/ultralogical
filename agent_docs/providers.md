# Provider instances

A provider instance is an org-scoped registration saying where that org's
environments run. Supported kinds are `local_docker`, `byo_k8s`, `hosted_eks`,
`byo_nomad`, `tunnel_local`, and `static`.

Every kind is a real adapter driving its own control plane. There is no alias:
a test fails the build if any adapter imports local Docker, and the shared
conformance contract requires each run to prove the adapter created resources
in its own control plane.

| Kind | What one environment is | Where it runs |
|---|---|---|
| `local_docker` | a container plus a named workspace volume | the machine running the worker |
| `byo_k8s` | a Pod, its token Secret, and a Service | the org's own cluster |
| `hosted_eks` | the same, inside a per-org namespace with RBAC, a NetworkPolicy, and a quota | the platform's cluster |
| `byo_nomad` | a Nomad job whose allocation publishes the tool port | the org's own Nomad cluster |
| `tunnel_local` | a container on the user's machine, reached through their outbound tunnel | the user's machine |
| `static` | a Bezalel process with its workspace bind-mounted at the declared workdir | the machine running the worker (walkthrough provider; needs `ULTRA_BEZALEL_BINARY`) |

## Registration

`RegisterProvider` builds the adapter from the registration's own
configuration and probes the real control plane before persisting anything.
The probe is read-only, so a failed attempt leaves nothing behind in an
operator's cluster. A registration that cannot be reached is refused rather
than stored as a provider that has never answered, and an unknown
configuration field is rejected rather than ignored.

Adapters are built per registration, not per kind. Two orgs registering
Kubernetes reach their own clusters.

## Capabilities

What a registration can do is discovered by probing, not inferred from its
kind: two clusters registered under one kind can differ. Capabilities are
stored with the registration, so a later decision never depends on the control
plane being reachable at that moment.

| Capability | Meaning |
|---|---|
| `serves_tool_endpoint` | environments publish the authenticated tool endpoint that health readiness and flow setup commands need |
| `restart_preserves_workspace` | a restart keeps the workspace |
| `tolerates_disconnect` | losing transport suspends rather than fails an environment |
| `adopts_orphans` | an interrupted provisioning finds its own resource instead of creating a second |
| `enumerates_resources` | the provider can list what it owns, which is what makes a leak check positive evidence |
| `namespace_isolation` | each org's environments sit in a hard boundary |
| `resource_quota` | concurrent environments and their requests are capped |

Capabilities change *how* the conformance suite verifies a behavior, never
*whether* it does. Everything in `ultra.CoreProviderContract` is
unconditional, and a manifest naming one of those as optional is itself a
failure. Unsupported capabilities carry a reason, which is what lets both
applications explain a flow refused against a provider.

## Hosted isolation

A hosted org gets a namespace created together with its boundary: a service
account with no cluster rules, a NetworkPolicy admitting only that namespace
plus the platform's own reachable ranges, and a ResourceQuota. Platform ranges
exclude the cluster's pod network, or a range broad enough to admit the
workers would also re-admit every neighbouring org. A range that would defeat
isolation is refused at registration.

## Tunnel agent

```sh
ultra-env-agent --token <registration-token> --secret <signing-secret>
cloudflared tunnel --url http://127.0.0.1:8099
```

The agent owns a real local-Docker provider and serves an authenticated
control API. The platform signs every request over its path, body, and
timestamp, so holding the tunnel URL is not enough to run commands on the
user's machine, a signature for one operation cannot authorize another, and a
captured request stops working. The agent refuses to start without a signing
secret. Revoking a lease releases the environments the agent holds rather than
only refusing new ones.

Losing the transport suspends environments instead of failing them, and
reconnecting resumes the same workspace.

## Onboarding

[docs/onboarding-kubernetes.md](../docs/onboarding-kubernetes.md) walks an
operator through registering their own cluster. Every `ultra` command it
documents is executed against a real cluster by
`TestA109_KubernetesOnboardingGuideIsExecutable`, so a guide that drifts from
what works fails CI rather than a reader's afternoon.

## Deployment configuration

| Variable | Effect |
|---|---|
| `ULTRA_PROVIDER_KINDS` | restricts which kinds this deployment offers |
| `ULTRA_BEZALEL_IMAGE` | the environment image adapters run |
| `ULTRA_HOSTED_INGRESS_CIDRS` | ranges hosted environments accept platform traffic from |
| `ULTRA_K8S_ENDPOINT_MODE` | `cluster` for in-cluster workers, `nodeport` for workers outside |
| `ULTRA_K8S_ENDPOINT_HOST` | host used with node-port endpoints |
| `ULTRA_K8S_NODEPORT_LOW` / `_HIGH` | bounds the node ports assigned, for deployments forwarding a fixed range |
