# Provider instances

Provider instances are org-scoped registrations selecting where Bezalel
environments run. Supported kinds are `local_docker`, `byo_k8s`,
`hosted_eks`, `byo_nomad`, and `tunnel_local`.

Registration is allowlisted and validates JSON before persistence. Remote
mode requires a reachable control endpoint with `/health`; CI uses explicit
`{"mode":"loopback"}` adapters that execute the real local-Docker provider
behind each distinct kind, so lifecycle/MCP behavior remains real while no
external credentials are needed. Hosted EKS instances receive the `hosted`
usage rate class; BYO/tunnel kinds receive `byo`.

`ultra-env-agent` is the outbound-only local provider control process. Run it
with `--token`, then expose its authenticated control endpoint with
`cloudflared tunnel --url ...`. The web provider onboarding is implemented
with shadcn/ui in the required dark theme; desktop provider state uses GPUI
and the same dark theme contract.
