# Phase E2 inventory

## Renames

| Old | New |
|-----|-----|
| env.go / DevEnv / EnvID / EnvSpec | resource.go / Resource / ResourceID / DevEnvSpec (kind schema) |
| EnvStore / Envs() | ResourceStore / Resources() |
| envwork | resourcework |
| envprovider | provider |
| testkit/envconverge | testkit/resourceconverge |
| EnvProvider / EnvAdopter / EnvResourceLister | ResourceProvider / ResourceAdopter / ResourceLister |
| ProviderHandle struct | json.RawMessage + handlefmt Encode/Decode |
| env.provision etc. jobs | resource.provision / terminate / restart / reconcile |
| EventKindEnv* / EnvEventPayload | EventKindResource* / ResourceEventPayload |
| proto env.proto EnvService | resource.proto ResourceService |
| CapabilityRestartPreservesWorkspace | CapabilityRestartPreservesState |
| loop EnvTools | ResourceTools (+ provision_resource family, env aliases) |
| http env_handler | resource_handler |

## Additions

- Resource.Kind column + migration 00010_resources.sql
- ResourceKindNullResource + provider/nullresource
- Core vs ToolSurface provider contracts
- provider/handlefmt for durable handle wire format
- e2e/resource_kinds_test.go (dev_env + null_resource)
