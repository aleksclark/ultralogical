//! GPUI application-path evidence for provider registration (A10.7).
//!
//! Registration reaches a real control plane, so these tests do too. Every
//! assertion is against what the window painted.

mod support;

use gpui::TestAppContext;
use support::{await_rendered, open_app, pump, rendered, selector};

use ultralogical_desktop::DesktopWindow;

const FRAME_ATTEMPTS: usize = 600;

/// kubeconfig returns the test cluster's configuration, or skips.
fn kubeconfig() -> Option<String> {
    if let Ok(path) = std::env::var("ULTRA_TEST_KUBECONFIG") {
        if let Ok(body) = std::fs::read_to_string(&path) {
            return Some(body);
        }
    }
    let cluster = std::env::var("ULTRA_TEST_KIND_CLUSTER").unwrap_or_else(|_| "ultra-test".into());
    let out = std::process::Command::new("kind")
        .args(["get", "kubeconfig", "--name", &cluster])
        .output()
        .ok()?;
    if !out.status.success() {
        return None;
    }
    String::from_utf8(out.stdout).ok()
}

/// A10.7 — an operator registers where environments run and the window shows
/// the registration with what its control plane can actually do.
#[gpui::test]
async fn registers_provider_and_shows_capabilities(cx: &mut TestAppContext) {
    // A test that quietly returned when no cluster was available would be
    // indistinguishable from one that proved something. CI always provides a
    // cluster, so its absence is a failure.
    let kubeconfig = kubeconfig().expect(
        "no kind cluster available; provider evidence requires one (set ULTRA_TEST_KUBECONFIG          or run: kind create cluster --name ultra-test)",
    );
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;
    await_rendered(cx, "window:dark", FRAME_ATTEMPTS);
    await_rendered(cx, "provider-panel", FRAME_ATTEMPTS);

    let config = serde_json::json!({
        "kubeconfig": kubeconfig,
        "namespace": "ultra-desktop-registration",
        "endpoint_mode": "nodeport",
        "endpoint_host": "127.0.0.1",
    })
    .to_string();
    let registered = client
        .register_provider(&org_id, "byo_k8s", "desktop-cluster", &config)
        .await
        .expect("register a reachable cluster");
    assert!(
        registered.supports("serves_tool_endpoint"),
        "a reachable cluster reported no tool endpoint capability: {:?}",
        registered.capabilities
    );

    let providers = client.list_providers(&org_id).await.expect("list providers");
    window.update(cx, |view: &mut DesktopWindow, cx| view.set_providers(providers, cx));

    // The registration is painted with its kind and how it is metered.
    await_rendered(cx, "provider:desktop-cluster:byo_k8s:byo", FRAME_ATTEMPTS);
    // A capability the cluster has, and one it does not, are both shown: an
    // operator needs the second to explain a refused flow.
    await_rendered(
        cx,
        "capability:desktop-cluster:serves_tool_endpoint:yes",
        FRAME_ATTEMPTS,
    );
    assert!(
        rendered(cx, "capability:desktop-cluster:restart_preserves_workspace:no"),
        "the window hides the capabilities this cluster lacks"
    );
}

/// A10.7 — a registration that cannot reach its control plane is refused, and
/// the window shows why rather than leaving the operator guessing.
#[gpui::test]
async fn shows_why_a_registration_was_refused(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;
    await_rendered(cx, "window:dark", FRAME_ATTEMPTS);

    let unreachable = serde_json::json!({
        "kubeconfig": "apiVersion: v1\nkind: Config\nclusters:\n- name: c\n  cluster:\n    server: http://127.0.0.1:1\ncontexts:\n- name: c\n  context:\n    cluster: c\n    user: u\ncurrent-context: c\nusers:\n- name: u\n  user: {}\n",
    })
    .to_string();
    let refused = client
        .register_provider(&org_id, "byo_k8s", "desktop-unreachable", &unreachable)
        .await
        .err()
        .expect("an unreachable cluster must be refused");
    let message = refused.to_string();
    assert!(
        message.contains("unreachable"),
        "the refusal does not say the control plane was unreachable: {message}"
    );

    // An error the operator cannot see is not a handled error.
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.set_error(message.clone(), cx)
    });
    await_rendered(cx, selector(format!("error:{message}")), FRAME_ATTEMPTS);

    // Nothing was registered.
    let providers = client.list_providers(&org_id).await.expect("list providers");
    assert!(
        !providers.iter().any(|p| p.name == "desktop-unreachable"),
        "a refused registration was stored"
    );
}

/// A1.7 — an inference credential is stored with its gateway fields and then
/// rendered by identity alone. The secret is write-only, so the window must be
/// able to show that a credential exists without ever showing what it is.
#[gpui::test]
async fn stores_credential_with_gateway_fields(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;
    await_rendered(cx, "credential-panel", FRAME_ATTEMPTS);

    let canary = std::env::var("ULTRA_CANARY_KEY").expect("the harness seeds a canary key");
    client
        .put_credential(
            &org_id,
            "desktop-gateway",
            &canary,
            "https://gateway.example.invalid/v1",
            r#"{"cf-aig-collect-log-payload":"false"}"#,
        )
        .await
        .expect("store a credential with gateway fields");

    let credentials = client.list_credentials(&org_id).await.expect("list credentials");
    assert!(
        credentials.iter().any(|c| c.name == "desktop-gateway"),
        "the stored credential is missing from the list"
    );
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.set_credentials(credentials, cx)
    });
    await_rendered(cx, "credential:inference:openai:desktop-gateway", FRAME_ATTEMPTS);

    // Nothing the window holds may contain the secret: a credential panel that
    // could render the key would be a place for it to leak.
    let painted = window.read_with(cx, |view: &DesktopWindow, _| {
        view.state
            .credentials
            .iter()
            .map(|c| format!("{}:{}", c.kind, c.name))
            .collect::<Vec<_>>()
            .join(",")
    });
    assert!(
        !painted.contains(&canary),
        "the credential surface exposed secret material"
    );
}

/// A10.7 — the window says where an environment actually runs, and a provider
/// that still hosts one cannot be removed. An operator diagnosing a fault
/// needs the first, and must not be able to orphan environments with the
/// second.
#[gpui::test]
async fn names_the_hosting_provider_and_refuses_to_orphan(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;
    await_rendered(cx, "window:dark", FRAME_ATTEMPTS);

    let session = client
        .create_session(&org_id, "GPUI provider ownership")
        .await
        .expect("create session");
    let env_id = client
        .provision_env(&session.id, "hosted-here")
        .await
        .expect("provision an environment");

    // Wait until the environment is reported with its host, which is what the
    // native entrypoint refreshes on every environment event.
    let mut hosts = Vec::new();
    for _ in 0..FRAME_ATTEMPTS {
        hosts = client.list_envs(&session.id).await.expect("list environments");
        if hosts.iter().any(|h| !h.provider_name.is_empty()) {
            break;
        }
        pump(cx);
    }
    window.update(cx, |view: &mut DesktopWindow, cx| view.set_env_hosts(hosts.clone(), cx));

    let host = hosts
        .iter()
        .find(|h| h.env_id == env_id)
        .expect("the provisioned environment is missing from the list");
    assert_eq!(
        host.provider_name, "default",
        "the environment does not name the registration hosting it"
    );
    assert_eq!(host.provider_kind, "local_docker");
    await_rendered(
        cx,
        selector(format!(
            "env-host:{}:{}:{}",
            host.name, host.provider_name, host.provider_state
        )),
        FRAME_ATTEMPTS,
    );

    // Removing that provider is refused while it still hosts the environment,
    // and the reason says so.
    let providers = client.list_providers(&org_id).await.expect("list providers");
    let default = providers
        .iter()
        .find(|p| p.name == "default")
        .expect("the default provider is missing");
    let refused = client
        .delete_provider(&org_id, &default.id)
        .await
        .err()
        .expect("removing a provider that still hosts environments must be refused");
    let message = refused.to_string();
    assert!(
        message.contains("still hosts"),
        "the refusal does not explain that environments are still hosted: {message}"
    );

    // The window surfaces it, and the provider is still there.
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.set_error(message.clone(), cx)
    });
    await_rendered(cx, selector(format!("error:{message}")), FRAME_ATTEMPTS);
    let still = client.list_providers(&org_id).await.expect("list providers");
    assert!(
        still.iter().any(|p| p.name == "default"),
        "a refused removal deleted the provider anyway"
    );
}
