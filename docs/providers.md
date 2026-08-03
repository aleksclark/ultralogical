# Adding an environment provider

A provider is where an org's environments run. This is the walkthrough for
adding a new kind. Start with `envprovider/static`: it is the smallest
adapter that still passes the shared conformance contract unmodified (under
200 lines of code), and its CI leg is what proves the seam stays
implementable. `envprovider/nomad` is the next step up when you need a real
control plane, and `envprovider/k8s` shows hosted policy layered on the same
seam.

For what the shipped kinds do and how registration behaves, see
[agent_docs/providers.md](../agent_docs/providers.md).

## The worked example

`envprovider/static` is intentionally small. One environment is one Bezalel
process in an unprivileged Linux namespace, with the workspace bind-mounted
at the declared workdir. Read `static.go` end to end, then:

```sh
# Needs unprivileged user namespaces (the CI leg permits them on Ubuntu 24.04).
go test ./envprovider/static/... -count=1 -v -timeout 15m
```

A worker offers the kind when a Bezalel binary is configured:

```sh
export ULTRA_BEZALEL_BINARY=/path/to/bezalel
# Optional: narrow the deployment to the example alone.
export ULTRA_PROVIDER_KINDS=static
```

Registration then names a root directory for per-environment state:

```sh
ultra provider register walkthrough --kind static \
  --config '{"root":"/var/lib/ultra-static"}'
```

## The contract

A provider implements `ultra.EnvProvider`: five methods over an opaque,
versioned handle you define.

| Method | Obligation |
|---|---|
| `Provision` | create the resource and return a handle that can find it again |
| `Status` | report the resource's real condition, including that it is gone |
| `Endpoint` | publish the authenticated tool endpoint, discovered rather than guessed |
| `Restart` | replace the runtime with one carrying a rotated token |
| `Terminate` | release everything, idempotently |

Three optional seams change how the suite verifies you, never whether it does:

| Seam | Why you would implement it |
|---|---|
| `ultra.EnvAdopter` | provisioning is create-then-persist, so a crash between the two would otherwise create a second resource |
| `ultra.EnvResourceLister` | makes the leak check a positive statement instead of an absence of evidence |
| `ultra.CapabilityProber` | reports what your control plane can actually do, rather than having it inferred from your kind |

Everything in `ultra.CoreProviderContract()` is unconditional. A capability
manifest naming one of those as optional is itself a failure, asserted by the
suite before it runs a step.

## Four decisions worth copying

1. **A deterministic identity per environment.** Kubernetes derives object
   names from the environment id, and Nomad derives a job id. That identity is
   what makes adoption, reconciliation, and leak detection exact instead of
   heuristic.
2. **Configuration is validated when the provider is built, not when it is
   used.** A bad kubeconfig or an unreachable Nomad address fails at
   registration rather than at every later provision.
3. **A vanished resource is `EnvFailed`, not merely unready.** Something
   outside the platform removed it, and reconciliation has to be able to see
   the difference. Reporting "still starting" forever is how an environment
   sits stale.
4. **An unreachable *host* is `EnvSuspended`, not failed.** The resource still
   exists on a machine that will come back, so the platform pauses metering and
   resumes rather than telling every other surface the work was destroyed.

## Adding your kind

1. **Write the adapter** in `envprovider/<kind>/`, with a `Config` whose JSON
   fields are the registration's stored configuration. Registration decoding is
   strict: an unknown field is rejected rather than ignored, so a misspelled
   namespace cannot silently send environments somewhere unexpected.
2. **Implement `Probe`** if the control plane can be asked what it supports.
   Return an error only when it is unreachable or refuses you; a reachable
   control plane missing a feature reports that feature unsupported *with a
   reason*. The reason is what lets both applications explain a flow refused
   against your provider.
3. **Run the shared suite** with `conformance.RunWith`, declaring only the
   capabilities you genuinely have and supplying an `Inspect` callback that
   reads your own control plane:

   ```go
   conformance.RunWith(t, factory, conformance.Options{
       Capabilities: ultra.ProviderCapabilities{Kind: "mykind", Supported: []ultra.ProviderCapability{
           ultra.CapabilityEnumeratesResources,
           ultra.CapabilityServesToolEndpoint,
       }},
       Inspect: func(t *testing.T, ctx context.Context, envID ultra.EnvID) []string {
           // Read the resources back through the control plane's own API.
           return provider.Resources(ctx, envID)
       },
   })
   ```

   `Inspect` is not optional and it is not decoration. Every behavioral
   assertion in the suite would also pass if your adapter quietly delegated the
   work to some other runtime, so proving the resources exist in your own
   control plane is the step that separates an implementation from an alias.

4. **Register the kind** in `envprovider/wiring.go` so every binary that can
   host environments offers it, rather than each `main` wiring it by hand.

5. **Prove convergence.** A resource destroyed out of band must move the
   environment to a terminal state without leaving a duplicate behind.
   `TestA102_KubernetesReconcilesExternallyDeletedPod` and
   `TestA104_NomadReconcilesExternallyStoppedAllocation` are the pattern: drive
   the real durable lifecycle, delete the resource with the control plane's own
   client, then assert on the persisted environment and on what the control
   plane still holds.

## What the suite will not let you skip

Provision, health, authenticated discovery, bash, exact editing, LSP,
background jobs, per-call deadlines, token rejection, restart with token
rotation, terminate, repeated terminate, leak detection, provider-native
resource proof, and distinct endpoints under concurrent provisioning.

If one of those cannot pass, the honest move is to fix the adapter or to not
claim the kind. Weakening the suite is explicitly disallowed.
