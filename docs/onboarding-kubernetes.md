# Onboarding a Kubernetes cluster

This guide registers your own cluster so your tenant's resources run
in it. Every command here is executed by the acceptance suite against a real
cluster, so a step that stopped working would fail CI rather than waste your
afternoon.

You need `kubectl` access to a cluster and the `core` CLI.

## 1. Check what the platform will need

Dev-env resources are Pods with a Secret and a Service, so the credentials you
register must be able to create those in one namespace.

```sh
kubectl auth can-i create pods --namespace ultracore-envs
kubectl auth can-i create services --namespace ultracore-envs
kubectl auth can-i create secrets --namespace ultracore-envs
```

Three `yes` answers mean registration will succeed. Registration checks this
itself, so you can skip straight to it and read the error instead.

## 2. Register the cluster

```sh
core provider register byo-cluster \
  --kind byo_k8s \
  --config '{"kubeconfig": "'"$(kubectl config view --raw --minify | sed 's/"/\"/g' | tr -d '\n')"'",
             "namespace": "ultracore-envs"}'
```

Registration performs a read-only probe of your cluster before storing
anything. It creates nothing, so a failed attempt leaves no trace, and a
cluster that cannot be reached is refused rather than saved as a provider that
has never answered.

## 3. Confirm what the platform found

```sh
core provider list --json
```

Each registration reports the capabilities its control plane actually has.
`serves_tool_endpoint` must be present, or agents cannot reach their
resources.

## 4. Use it

```sh
core provider show byo-cluster --json
```

A resource created against this provider now runs as a Pod in your
cluster. `kubectl get pods -n ultracore-envs -l app.kubernetes.io/managed-by=ultracore`
lists them.

## Reaching resources from outside the cluster

Workers running outside the cluster cannot use in-cluster DNS. Set
`endpoint_mode` to `nodeport` and give the platform a reachable host:

```json
{"kubeconfig": "...", "namespace": "ultracore-envs",
 "endpoint_mode": "nodeport", "endpoint_host": "cluster.example.com",
 "node_port_range": [30080, 30099]}
```

The range must be one your nodes actually publish. Bounding it matters: an
unbounded assignment produces endpoints your workers cannot reach.

## Removing it

```sh
core provider remove byo-cluster
```

This is refused while the provider still hosts resources. Terminate them
first; otherwise their records would survive with nothing able to reach or
clean up the resources behind them.
