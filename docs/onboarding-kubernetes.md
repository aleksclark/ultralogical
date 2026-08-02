# Onboarding a Kubernetes cluster

This guide registers your own cluster so your organization's environments run
in it. Every command here is executed by the acceptance suite against a real
cluster, so a step that stopped working would fail CI rather than waste your
afternoon.

You need `kubectl` access to a cluster and the `ultra` CLI.

## 1. Check what the platform will need

Environments are Pods with a Secret and a Service, so the credentials you
register must be able to create those in one namespace.

```sh
kubectl auth can-i create pods --namespace ultralogical-envs
kubectl auth can-i create services --namespace ultralogical-envs
kubectl auth can-i create secrets --namespace ultralogical-envs
```

Three `yes` answers mean registration will succeed. Registration checks this
itself, so you can skip straight to it and read the error instead.

## 2. Register the cluster

```sh
ultra provider register byo-cluster \
  --kind byo_k8s \
  --config '{"kubeconfig": "'"$(kubectl config view --raw --minify | sed 's/"/\"/g' | tr -d '\n')"'",
             "namespace": "ultralogical-envs"}'
```

Registration performs a read-only probe of your cluster before storing
anything. It creates nothing, so a failed attempt leaves no trace, and a
cluster that cannot be reached is refused rather than saved as a provider that
has never answered.

## 3. Confirm what the platform found

```sh
ultra provider list --json
```

Each registration reports the capabilities its control plane actually has.
`serves_tool_endpoint` must be present, or agents cannot reach their
environments and any flow requiring readiness checks will be refused with that
reason.

## 4. Use it

```sh
ultra provider show byo-cluster --json
```

An environment created against this provider now runs as a Pod in your
cluster. `kubectl get pods -n ultralogical-envs -l app.kubernetes.io/managed-by=ultralogical`
lists them.

## Reaching environments from outside the cluster

Workers running outside the cluster cannot use in-cluster DNS. Set
`endpoint_mode` to `nodeport` and give the platform a reachable host:

```json
{"kubeconfig": "...", "namespace": "ultralogical-envs",
 "endpoint_mode": "nodeport", "endpoint_host": "cluster.example.com",
 "node_port_range": [30080, 30099]}
```

The range must be one your nodes actually publish. Bounding it matters: an
unbounded assignment produces endpoints your workers cannot reach.

## Removing it

```sh
ultra provider remove byo-cluster
```

This is refused while the provider still hosts environments. Terminate them
first; otherwise their records would survive with nothing able to reach or
clean up the resources behind them.
