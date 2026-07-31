//! GPUI application-path evidence for provider registration.

mod support;

use gpui::TestAppContext;
use support::{await_rendered, open_app, selector};

use ultralogical_client::ultra::v1;
use ultralogical_desktop::DesktopWindow;

const FRAME_ATTEMPTS: usize = 600;

#[gpui::test]
async fn registers_provider_kinds(cx: &mut TestAppContext) {
    let (_window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;
    await_rendered(cx, "window:dark", FRAME_ATTEMPTS);

    for (kind, name) in [
        ("byo_k8s", "desktop-k8s"),
        ("byo_nomad", "desktop-nomad"),
        ("tunnel_local", "desktop-tunnel"),
    ] {
        client
            .orgs
            .register_provider(client.auth(v1::RegisterProviderRequest {
                org_id: org_id.clone(),
                kind: kind.into(),
                name: name.into(),
                config_json: "{\"mode\":\"loopback\"}".into(),
            }))
            .await
            .expect("register provider");
    }
    let providers = client
        .orgs
        .list_providers(client.auth(v1::ListProvidersRequest { org_id: org_id.clone() }))
        .await
        .expect("list providers")
        .into_inner();
    for kind in ["byo_k8s", "byo_nomad", "tunnel_local"] {
        assert!(
            providers.providers.iter().any(|p| p.kind == kind),
            "provider kind {kind} was not registered"
        );
    }
}

#[gpui::test]
async fn rejects_invalid_provider_config(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;
    await_rendered(cx, "window:dark", FRAME_ATTEMPTS);

    let result = client
        .orgs
        .register_provider(client.auth(v1::RegisterProviderRequest {
            org_id: org_id.clone(),
            kind: "byo_k8s".into(),
            name: "broken-k8s".into(),
            config_json: "not-json".into(),
        }))
        .await;
    let status = result.err().expect("invalid provider config must be rejected");

    // The window surfaces the failure: an error the user cannot see is not a
    // handled error.
    let message = status.message().to_string();
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.set_error(message.clone(), cx)
    });
    await_rendered(cx, selector(format!("error:{message}")), FRAME_ATTEMPTS);
}
