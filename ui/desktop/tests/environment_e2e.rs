//! GPUI application-path evidence for environments and usage.
//!
//! These tests provision real Bezalel containers through the window's actions
//! and assert on the rendered environment and usage panels.

mod support;

use gpui::TestAppContext;
use support::{await_rendered, open_app, pump, rendered, selector};
use ultralogical_desktop::DesktopWindow;

const FRAME_ATTEMPTS: usize = 1200;

/// The window renders environment lifecycle progress and the real output of a
/// command executed through its action.
#[gpui::test]
async fn shows_environment_lifecycle_and_exec(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let session = client
        .create_session(&org_id, "GPUI environment")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);

    let env_id = client
        .provision_env(&session.id, "gpui-main")
        .await
        .expect("provision env");
    // Lifecycle progress is rendered, not merely reached: a pre-ready phase
    // must be painted before the ready phase. Which pre-ready phase the window
    // observes depends on how fast the worker moves, so either qualifies.
    let mut phases: Vec<String> = Vec::new();
    for _ in 0..FRAME_ATTEMPTS {
        if let Some(phase) =
            window.read_with(cx, |view: &DesktopWindow, _| {
                view.state.env_by_name("gpui-main").map(|env| env.phase.clone())
            })
        {
            if rendered(cx, selector(format!("env:gpui-main:{phase}:1"))) && !phases.contains(&phase)
            {
                phases.push(phase.clone());
            }
            if phase == "ready" {
                break;
            }
        }
        pump(cx);
    }
    assert!(
        phases.iter().any(|p| p == "requested" || p == "provisioning"),
        "window never painted a pre-ready lifecycle phase: {phases:?}"
    );
    await_rendered(cx, "env:gpui-main:ready:1", FRAME_ATTEMPTS);

    let output = client
        .exec_preview(&env_id, "echo gpui-environment")
        .await
        .expect("exec preview");
    assert!(output.contains("gpui-environment"), "exec output = {output:?}");
    await_rendered(cx, "exec-output:gpui-environment", FRAME_ATTEMPTS);

    client.terminate_env(&env_id).await.expect("terminate env");
    await_rendered(cx, "env:gpui-main:terminated:1", FRAME_ATTEMPTS);
}

/// A7.4 — restarting through the window's rendered control rotates the token
/// epoch, and the window paints the new epoch.
#[gpui::test]
async fn shows_environment_restart_epoch(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let session = client
        .create_session(&org_id, "GPUI environment restart")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);

    let env_id = client
        .provision_env(&session.id, "gpui-restart")
        .await
        .expect("provision env");
    await_rendered(cx, "env:gpui-restart:ready:1", FRAME_ATTEMPTS);
    // The restart control itself is rendered.
    await_rendered(cx, "restart:gpui-restart", FRAME_ATTEMPTS);

    // Drive the window's own restart action, the same one the rendered
    // control invokes.
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.request_restart(env_id.clone(), cx)
    });
    await_rendered(cx, "env:gpui-restart:ready:2", FRAME_ATTEMPTS);

    // The restarted environment still answers with its rotated token.
    let output = client
        .exec_preview(&env_id, "echo after-gpui-restart")
        .await
        .expect("exec preview after restart");
    assert!(output.contains("after-gpui-restart"), "exec output = {output:?}");
    await_rendered(cx, "exec-output:after-gpui-restart", FRAME_ATTEMPTS);

    client.terminate_env(&env_id).await.expect("terminate env");
}

/// A7.6 — org usage is a rendered surface, and terminating an environment
/// closes its rendered interval.
#[gpui::test]
async fn shows_org_usage_totals(cx: &mut TestAppContext) {
    let (window, mut cx, mut client, org_id) = open_app(cx).await;
    let cx = &mut cx;

    let session = client
        .create_session(&org_id, "GPUI usage")
        .await
        .expect("create session");
    let stream = client.subscribe(&session.id, 0).await.expect("subscribe");
    window.update(cx, |view: &mut DesktopWindow, cx| {
        view.open_session(session.id.clone(), stream, cx)
    });
    await_rendered(cx, "connection:live", FRAME_ATTEMPTS);

    let env_id = client
        .provision_env(&session.id, "gpui-usage")
        .await
        .expect("provision env");
    await_rendered(cx, "env:gpui-usage:ready:1", FRAME_ATTEMPTS);

    let (usage, total) = client.usage(&org_id).await.expect("usage");
    let open_interval = usage
        .iter()
        .find(|i| i.env_id == env_id)
        .expect("metering opened for the provisioned environment")
        .clone();
    assert!(open_interval.open, "interval should be open while the env is ready");
    window.update(cx, |view: &mut DesktopWindow, cx| view.set_usage(usage, total, cx));
    await_rendered(
        cx,
        selector(format!("usage:{}:open:{}", open_interval.env_id, open_interval.seconds)),
        FRAME_ATTEMPTS,
    );
    await_rendered(cx, selector(format!("usage-total:{total}")), FRAME_ATTEMPTS);

    client.terminate_env(&env_id).await.expect("terminate env");
    await_rendered(cx, "env:gpui-usage:terminated:1", FRAME_ATTEMPTS);

    // Poll usage through the window until the interval renders as closed.
    let mut closed = None;
    for _ in 0..FRAME_ATTEMPTS {
        let (usage, total) = client.usage(&org_id).await.expect("usage");
        if let Some(interval) = usage.iter().find(|i| i.env_id == env_id && !i.open).cloned() {
            window.update(cx, |view: &mut DesktopWindow, cx| view.set_usage(usage, total, cx));
            closed = Some(interval);
            break;
        }
        window.update(cx, |view: &mut DesktopWindow, cx| view.set_usage(usage, total, cx));
        pump(cx);
    }
    let closed = closed.expect("usage interval never closed after termination");
    await_rendered(
        cx,
        selector(format!("usage:{}:closed:{}", closed.env_id, closed.seconds)),
        FRAME_ATTEMPTS,
    );
}
