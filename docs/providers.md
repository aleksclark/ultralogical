# Adding an environment provider

A provider is where an org's environments run. This is the walkthrough for
adding a new kind, written by building the one in `envprovider/static/` and
keeping it small enough to read in one sitting.

For what the shipped kinds do and how registration behaves, see
[agent_docs/providers.md](../agent_docs/providers.md).

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

## The walkthrough provider

`envprovider/static/` runs one environment as one Bezalel process inside an
unprivileged Linux namespace sandbox, with the environment's workspace
bind-mounted at the declared workdir. It deliberately drives no remote control
plane: it exists so the shape of a provider is readable without the noise of a
real API client, and it is not a deployment target.

It is under 200 lines of code, checked by
`TestA109_StaticProviderStaysUnderTheDocumentedSize`. The check counts code
rather than raw file length, because this repository requires comments that
explain why a decision was made and an example is exactly the place that
reasoning belongs. The whole file is bounded too, so the allowance cannot hide
unbounded growth.

Four decisions in it are worth copying:

1. **A deterministic identity per environment.** The static provider uses one
   directory per environment id. Kubernetes derives object names from the id,
   and Nomad derives a job id. That identity is what makes adoption,
   reconciliation, and leak detection exact instead of heuristic.
2. **Configuration is validated when the provider is built, not when it is
   used.** `static.New` refuses a Bezalel binary that does not exist, so the
   failure appears at registration rather than at every later provision.
3. **A vanished resource is `EnvFailed`, not merely unready.** Something
   outside the platform removed it, and reconciliation has to be able to see
   the difference. Reporting "still starting" forever is how an environment
   sits stale.
4. **Terminate and restart are not the same operation.** Restart stops the
   process and leaves the workspace; terminate removes both. Collapsing them is
   what breaks a `restart_preserves_workspace` claim.

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
